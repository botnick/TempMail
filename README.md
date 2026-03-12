# TempMail Platform

> **Disposable email infrastructure** — self-hosted, multi-node, real-time.  
> Built with Go · Fiber · Asynq · PostgreSQL · Redis · Cloudflare R2 · Rspamd  
> **v3.2.0** — Internal API key bypass, attachment auth fix, reader tab fix

---

## Overview

TempMail is a complete disposable email backend. You give it a domain (or several), and it instantly provides:

- **Receiving** — SMTP listener on port 25 that accepts mail for any of your domains  
- **Processing** — spam scoring, attachment extraction, Cloudflare R2 storage  
- **API** — REST API for creating mailboxes, listing messages, downloading attachments  
- **Admin Panel** — built-in web UI at a secret URL, real-time SSE updates  
- **Multi-node** — add extra SMTP nodes to the cluster via `add-node.sh`  

---

## Architecture

```
Internet (SMTP)
      │
      ▼
┌─────────────┐   Redis Pub/Sub   ┌──────────────┐
│  mail-edge  │ ─────────────────▶│   api (Fiber)│◀─── SDK / Frontend
│  (SMTP :25) │   Asynq queue     │   (:4000)    │◀─── Admin Panel
└─────────────┘                   └──────┬───────┘
      │ Rspamd                           │
      ▼ (spam score)                     ▼
┌─────────────┐                   ┌──────────────┐
│   rspamd    │                   │   worker     │
│  (:11333)   │                   │  (Asynq)     │
└─────────────┘                   └──────┬───────┘
                                         │
                                   ┌─────┴──────────┐
                                   │  PostgreSQL     │
                                   │  Redis          │
                                   │  Cloudflare R2  │
                                   └─────────────────┘
```

### Mail Ingest Flow

```
1. mail-edge  ← SMTP connection on :25 (external 25 → internal 2525)
2. mail-edge  → rspamd  (spam scan, returns score)
3. mail-edge  → Redis   (Asynq queue: task "mail:ingest")
4. worker     ← Redis   (dequeues task)
5. worker     → R2      (upload raw .eml + attachments)
6. worker     → PostgreSQL (INSERT Message + Attachments)
7. api        → Redis   (Publish "mail:events" for SSE)
8. Admin Panel ← SSE   (real-time notification, no polling)
```

---

## Services

| Service | Image / Build | Port | Role |
|---------|--------------|------|------|
| `postgres` | `postgres:15-alpine` | internal | Primary database |
| `redis` | `redis:7-alpine` | internal | Queue + cache + pub/sub |
| `rspamd` | `rspamd/rspamd:latest` | `127.0.0.1:11334` | Spam filtering |
| `mail-edge` | Go build target `mail-edge` | `0.0.0.0:25→2525` | SMTP receiver |
| `api` | Go build target `api` | `0.0.0.0:4000` | REST API + Admin UI |
| `worker` | Go build target `worker` | — | Background processing |

All services share the `internal` Docker bridge network. Only ports 25 and 4000 are exposed externally.

---

## Quick Start

### Prerequisites

- VPS with **Ubuntu 22.04+** (or any Linux with Docker)
- Domain with DNS access
- Cloudflare R2 bucket (free tier is fine)

### 1. Clone & prepare `.env`

```bash
git clone https://github.com/botnick/TempMail.git /opt/mailserver
cd /opt/mailserver
```

Open `INSTALL_GUIDE_TH.html` in a browser → use the **Generate .env** button → paste output to `.env`.

Key variables you **must** set:

```env
# Database
POSTGRES_USER=tempmail
POSTGRES_PASSWORD=<random>
POSTGRES_DB=tempmail_db
DATABASE_URL=host=postgres user=tempmail password=<pw> dbname=tempmail_db port=5432 sslmode=disable TimeZone=UTC

# Redis
REDIS_PASSWORD=<random>
REDIS_URL=redis://:<password>@redis:6379

# Admin Panel
ADMIN_API_KEY=<random-secret>          # Password for admin panel login
ADMIN_USERNAME=admin                   # Username (default: admin)
ADMIN_PANEL_PATH=<random-slug>         # Secret path, e.g. "myadmin8472"

# Cloudflare R2 (attachment storage)
R2_ACCOUNT_ID=<from CF dashboard>
R2_ACCESS_KEY_ID=<from CF R2 API token>
R2_SECRET_ACCESS_KEY=<from CF R2 API token>
R2_BUCKET_NAME=tempmail-archives

# Spam filtering
RSPAMD_PASSWORD=                       # Leave empty unless you set a password in rspamd config

# Optional
FRONTEND_URL=https://yourapp.com       # Enable CORS for this origin only
LOG_LEVEL=info
TZ=Asia/Bangkok
```

### 2. Deploy

```bash
chmod +x deploy.sh
./deploy.sh
```

The script will:
1. Check prerequisites (Docker, ports 25/4000)
2. Start all containers via `docker compose up -d`
3. Wait for health checks to pass
4. Print the Admin Panel URL and the auto-generated API key

### 3. DNS Setup

For each domain you want to receive mail on, add:

```
MX  yourdomain.com  →  your-server-ip  (priority 10)
A   mail.yourdomain.com  →  your-server-ip
```

Then add the domain in Admin Panel → **Domains** tab.

---

## Admin Panel

Open: `https://yourdomain.com/<ADMIN_PANEL_PATH>/`

Login with:
- **Username**: value of `ADMIN_USERNAME` (default: `admin`)
- **Password**: value of `ADMIN_API_KEY`

The session uses HMAC-signed tokens (not stored in DB). SSE keeps the message list live.

### Admin Panel Tabs

| Tab | What you can do |
|-----|----------------|
| Dashboard | Live stats: messages today, active mailboxes, domains |
| Domains | Add/edit/delete domains, check DNS MX records |
| Nodes | View mail-edge nodes (primary auto-registers on boot) |
| Filters | Block/allow sender patterns (e.g. `*.spam.com`) |
| Mailboxes | Browse, search, quick-create, delete mailboxes |
| Messages | Full message viewer: HTML/Text/Source tabs + attachments |
| API Keys | Create/revoke API keys; toggle **Internal Mode** to bypass rate limits |
| Settings | Webhook URL, TTL, spam threshold, rate limits |

---

## Authentication

There are **two separate auth systems**:

### 1. SDK / API Auth (for your frontend app)

All `/v1/*` and `/internal/*` routes require an **API Key**.

```
X-API-Key: <key>
# or
Authorization: Bearer <key>
```

- Keys are created in Admin Panel → **API Keys**
- Stored as SHA-256 hashes in PostgreSQL
- Hot-synced to Redis (`system:api_key_hashes` set) for O(1) lookup
- On first boot, a **default key** is auto-generated and printed to `docker logs api`

### 2. Admin Panel Auth

`POST /admin/login` → returns a short-lived HMAC-signed session token  
All `/admin/*` routes require `Authorization: Bearer <session_token>`  
SSE endpoint `/admin/events` accepts the token as `?token=` query param (EventSource API limitation)

---

## API Keys Lifecycle

```
Admin creates key → raw key shown ONCE → hash stored in DB
                 ↓
         SyncAPIKeysToRedis() rebuilds Redis sets:
           system:api_key_hashes  ← SHA-256 hash (for auth)
           system:api_key_meta    ← hash → "internal" (for bypass)
                 ↓
SDK sends key → SHA-256 → Redis SISMEMBER check → 200/401
               if isInternal → skip rate limiting, set Locals("is_internal_key")
```

> ⚠️ The raw API key is shown **only once** at creation time. Store it securely.

### Internal Mode

API keys can be marked **Internal** in the Admin Panel → API Keys → Edit → ⚡ Internal Mode toggle.

- Internal keys bypass all rate limiting (suitable for trusted background services, internal tools)
- The `isInternal` flag is synced to Redis `system:api_key_meta` alongside key hashes
- The middleware sets `c.Locals("is_internal_key", true)` on authenticated internal requests
- Rate Limit column shows **∞ Unlimited** for internal keys in the Admin Panel

---

## Multi-Node (Secondary SMTP Nodes)

To add a second VPS as a redundant SMTP receiver:

```bash
# On the NEW VPS (already has Docker + git clone)
chmod +x add-node.sh
./add-node.sh
```

The script will ask for:

| Prompt | Where to find it |
|--------|-----------------|
| Redis URL | Primary `.env` → `REDIS_URL` (replace `redis:6379` with primary VPS IP) |
| Internal API URL | `http://primary-ip:4000/internal/mail/ingest` |
| Rspamd URL | `http://primary-ip:11333` (optional) |
| Mail domain | e.g. `mail2.yourdomain.com` |

It then generates `docker-compose.node.yml` and starts only `mail-edge`.  
Add MX record with **priority 20** (primary stays at 10 for automatic failover).

---

## Environment Variables (Full Reference)

### Required

| Variable | Used by | Description |
|----------|---------|-------------|
| `DATABASE_URL` | api, worker | PostgreSQL DSN |
| `REDIS_URL` | api, worker, mail-edge | Redis connection string |
| `POSTGRES_USER` | postgres | DB username |
| `POSTGRES_PASSWORD` | postgres | DB password |
| `POSTGRES_DB` | postgres | DB name |
| `ADMIN_API_KEY` | api | Admin panel password |
| `ADMIN_PANEL_PATH` | api | Secret URL slug for admin UI |
| `R2_ACCOUNT_ID` | api, worker | Cloudflare R2 account |
| `R2_ACCESS_KEY_ID` | api, worker | R2 API key |
| `R2_SECRET_ACCESS_KEY` | api, worker | R2 secret |
| `R2_BUCKET_NAME` | api, worker | R2 bucket name |

### Optional (with defaults)

| Variable | Default | Description |
|----------|---------|-------------|
| `ADMIN_USERNAME` | `admin` | Admin login username |
| `FRONTEND_URL` | `""` | Enable CORS for this origin |
| `REDIS_PASSWORD` | — | Redis password (set in REDIS_URL too) |
| `RSPAMD_PASSWORD` | `""` | Rspamd web UI password |
| `LOG_LEVEL` | `info` | Log verbosity |
| `TZ` | `Asia/Bangkok` | Timezone |
| `PORT` | `4000` | API server port |
| `BODY_LIMIT_MB` | `40` | Max HTTP request body |
| `PUBLIC_RATE_LIMIT` | `60` | Public route req/min per IP |
| `LOGIN_RATE_LIMIT` | `10` | Login attempts/min per IP |
| `WORKER_CONCURRENCY` | `50` | Asynq worker goroutines |
| `RETENTION_CRON` | `@hourly` | Message cleanup schedule |
| `MAILBOX_EXPIRE_CRON` | `*/5 * * * *` | Mailbox TTL check schedule |

### Runtime Settings (via Admin Panel → Settings tab)

These live in Redis (`system:settings` hash) and can be changed without restart:

| Key | Default | Description |
|-----|---------|-------------|
| `webhook_url` | `""` | POST to this URL on new mail |
| `webhook_secret` | `""` | HMAC-SHA256 secret for webhook |
| `default_mailbox_ttl_hours` | `24` | Mailbox auto-expire |
| `default_message_ttl_hours` | `24` | Message retention |
| `cleanup_interval_minutes` | `5` | Worker cleanup interval |
| `max_message_size_mb` | `25` | Max email size |
| `max_attachments` | `10` | Max attachments per email |
| `max_attachment_size_mb` | `10` | Max single attachment |
| `spam_reject_threshold` | `15` | Score to auto-reject email |

---

## Useful Commands

```bash
# View logs
docker compose logs -f api
docker compose logs -f mail-edge
docker compose logs -f worker

# Restart a service
docker compose restart api

# Pull latest code and rebuild
git pull && docker compose up -d --build

# Secondary node commands
docker compose -f docker-compose.node.yml logs -f
docker compose -f docker-compose.node.yml restart
```

---

## Security Notes

- Admin panel is hidden behind a **secret URL slug** (`ADMIN_PANEL_PATH`)
- API keys stored as **SHA-256 hashes** — raw key never persisted in DB
- **Internal keys** bypass rate limiting; only grant to trusted internal services
- HTML emails are sanitized before display (CSP + `srcdoc` iframe for safe rendering)
- Attachments served with token auth (`Authorization: Bearer`) — direct URL access denied
- CSP, HSTS, X-Frame-Options, X-Content-Type-Options headers on all responses
- Redis and PostgreSQL are on an **internal Docker network** (not exposed)
- Login rate-limited to 10 req/min per IP
- CORS disabled by default (opt-in via `FRONTEND_URL`)

---

## License

MIT
