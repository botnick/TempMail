# TempMail Platform Architecture

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
- **`api`:** Internal REST API (Go Fiber). Owns the database connection via GORM. It receives ingestion payloads from `mail-edge` and creates records.
- **`mail-edge`:** Fast `emersion/go-smtp` edge node. It does *not* talk to PostgreSQL directly. Instead, it queries Redis for fast recipient validation, and talks to `api` to ingest messages.
- **`worker`:** Background retention and mailbox expiration worker via `hibiken/asynq`.
- **`db` / `redis`:** PostgreSQL and Redis data layers.
- **`cloudflare-r2`:** Cloudflare R2 is utilized as the S3-compatible Object Storage for raw mail and attachments, vastly reducing local storage overhead and server burden.

## 2. Internal API Contracts

`mail-edge` talks to `api` via:
`POST http://api:4000/internal/mail/ingest`
**Auth**: `Authorization: Bearer <INTERNAL_API_TOKEN>`

`api` replies `201 Created` or `404 Not Found`.
This pattern allows `mail-edge` to be 100% stateless and scaled infinitely behind an SMTP Load Balancer.

## 3. Inbound Mail & Spam Flow

1. **SMTP Connection**: A sender connects to `mail-edge` :25.
2. **RCPT TO Validation**: `mail-edge` queries Redis (O(1)). If the mailbox is invalid or expired, it drops the connection instantly to prevent DDOS.
3. **Data Reception**: The message is streamed to a buffer.
4. **Spam Assessment**: The buffer is passed to Rspamd for scoring.
5. **Decision**:
   - Score > 10: Reject (SMTP 550)
   - Score > 5: Quarantine
   - Score <= 5: Accept
6. **Ingestion**: `mail-edge` hits the `api` ingest contract. The raw email blob is uploaded to Cloudflare R2. The parsed metadata is saved to PostgreSQL.

## 4. Admin Panel & Background Jobs

**Admin Access** is managed through Role/Permission checking in the Fiber middleware. Mutations are logged to an Audit table.
**Background Jobs** are executed strictly in the `worker` process. Asynq runs periodic tasks to expire Mailboxes and delete old Messages (and their Cloudflare R2 blobs) based on the User's Plan limits.

## 5. Scaling Path (1 Node to Multi-Node)

**Phase 1: 1 VPS (Current Setup)**
Everything runs via `docker-compose.yml`. SQLite cannot be used; PostgreSQL is used from day 1 for concurrent connections.

**Phase 2: Managed Data**
Extract PostgreSQL and Redis from Docker to AWS RDS and AWS ElastiCache. Cloudflare R2 already offloads object storage bandwidth.

**Phase 3: Mail Edge Scale-out**
Place an AWS Network Load Balancer (NLB) or HAProxy on port 25. Run 5 instances of `mail-edge`. They share nothing but the Redis cache and internal API. 

**Phase 4: Web/API Scale-out**
Replicate `api` and `apps/web` behind an Application Load Balancer. Scale Asynq workers out linearly based on lag.
