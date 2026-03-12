# TempMail Platform Architecture (v3.1.0)

This document describes the production-ready architecture of the internal TempMail platform. 
The system runs on a strict internal network model, designed to be horizontally scalable and secure.

## 1. High-Level Architecture & Network Model

The entire mail backend is isolated. The only public touchpoints are:
1. `apps/web` (The main web application running on ports 80/443).
2. `mail-edge` (The SMTP receiver running on port 25 written in Go).

All other services (Data, Queues, Workers, Internal APIs) rest strictly inside a private Docker/VPC network. 
The `api` runs on a private internal port and is consumed over HTTP.

### Service Boundaries
- **`apps/web`:** User Dashboard, Auth, Billing.
- **`api`:** Internal REST API (Go Fiber). Owns the database connection via GORM. Serves SDK and Admin endpoints.
- **`mail-edge`:** Fast `emersion/go-smtp` edge node. It does *not* talk to PostgreSQL or the API directly. It queries Redis for recipient validation then enqueues emails to the **Redis/Asynq queue** for async processing.
- **`worker`:** **Primary mail processor** + background retention / mailbox expiration worker via `hibiken/asynq`. Processes inbound email tasks from the queue (MIME parsing, R2 upload, DB insert) with configurable concurrency (default: 50).
- **`db` / `redis`:** PostgreSQL and Redis data layers.
- **`cloudflare-r2`:** Cloudflare R2 is utilized as the S3-compatible Object Storage for raw mail and attachments, vastly reducing local storage overhead and server burden.

## 2. Async Mail Processing (v3.1.0)

Mail processing uses an **async queue** architecture via Redis/Asynq:

```
mail-edge → Rspamd (spam check) → Redis Queue (Asynq) → SMTP OK (<5ms)
                                         ↓
                              Worker (50 concurrent goroutines)
                                    ↓         ↓          ↓
                              Parse MIME   Upload R2   Insert DB
```

**Shared task definitions** in `shared/tasks/tasks.go`:
- Task type: `mail:ingest`
- Payload: `{from, to, raw_email, spam_score, quarantine_action}`
- Queue: `ingest` (priority 60, strict priority over maintenance queue at 10)
- Max retry: 5, Timeout: 120 seconds per task

**Why async?**
- SMTP connections release in <5ms — no blocking on R2 uploads or DB inserts
- Redis buffers millions of tasks during traffic spikes
- Worker processes independently, scales horizontally
- System absorbs 25,000+ burst emails without dropped connections

## 3. Inbound Mail & Spam Flow

1. **SMTP Connection**: A sender connects to `mail-edge` :25.
2. **RCPT TO Validation**: `mail-edge` queries Redis (O(1)). If the mailbox is invalid or expired, it drops the connection instantly to prevent DDOS.
3. **Data Reception**: The message is streamed to a buffer.
4. **Spam Assessment**: The buffer is passed to Rspamd for scoring.
5. **Decision**:
   - Score > threshold or action=reject: Reject (SMTP 550)
   - Score > soft threshold: Quarantine
   - Otherwise: Accept
6. **Async Enqueue**: `mail-edge` enqueues the raw email + metadata to the Redis `ingest` queue. SMTP responds `250 OK` immediately.
7. **Worker Processing**: Worker dequeues the task, parses MIME, uploads raw .eml + attachments to R2, inserts message + attachment records to PostgreSQL, and fires webhooks.

## 4. Admin Panel & Background Jobs

**Admin Access** is managed through Role/Permission checking in the Fiber middleware. Mutations are logged to an Audit table.
**Background Jobs** are executed strictly in the `worker` process. Asynq runs periodic tasks to expire Mailboxes and delete old Messages (and their Cloudflare R2 blobs) based on retention settings.

**Queue Priorities (StrictPriority mode):**
| Queue | Priority | Purpose |
|-------|----------|---------|
| `ingest` | 60 | New email processing (always first) |
| `maintenance` | 10 | Retention cleanup, mailbox expiry |

## 5. Scaling Path (1 Node to Multi-Node)

**Phase 1: 1 VPS (Current Setup)**
Everything runs via `docker-compose.yml`. Worker concurrency handles 50 concurrent email tasks.

**Phase 2: Managed Data**
Extract PostgreSQL and Redis from Docker to AWS RDS and AWS ElastiCache. Cloudflare R2 already offloads object storage bandwidth.

**Phase 3: Mail Edge Scale-out**
Place an AWS Network Load Balancer (NLB) or HAProxy on port 25. Run N instances of `mail-edge`. They share nothing but the Redis queue — no API dependency.

**Phase 4: Worker Scale-out**
Run multiple `worker` instances. Asynq automatically distributes tasks across workers. Set `WORKER_CONCURRENCY=100` per instance for higher throughput.

**Phase 5: Web/API Scale-out**
Replicate `api` behind an Application Load Balancer. API no longer receives mail ingestion — it only serves SDK + Admin endpoints.
