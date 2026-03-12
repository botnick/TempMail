# TempMail — Disposable Temporary Email Backend

> **Standalone SMTP server** ที่รับเมลจริงจากอินเทอร์เน็ต → กรอง Spam → เก็บให้เว็บหลักดึงผ่าน **REST API**

```
Internet (SMTP) → mail-edge:25 → Rspamd → API → PostgreSQL + R2
                                                  ↑
                                           Frontend Website (REST API)
```

---

## Architecture Overview

| Service | หน้าที่ | Port |
|---------|---------|------|
| **mail-edge** | SMTP server รับเมลจากอินเทอร์เน็ต | `25` |
| **api** | REST API + Admin Panel | `4000` |
| **worker** | Cleanup jobs (mailbox/message TTL) | — |
| **postgres** | Database หลัก | `5432` |
| **redis** | Cache, session, settings, active mailbox tracking | `6379` |
| **rspamd** | Spam filtering | `11333/11334` |

## Quick Start

```bash
# Clone
git clone https://github.com/botnick/TempMail.git
cd TempMail

# Deploy (auto-installs Docker, generates keys, starts everything)
chmod +x deploy.sh && ./deploy.sh
```

> `deploy.sh` จะติดตั้ง Docker, สร้าง `.env` พร้อม security tokens, build + deploy ทุก service ในครั้งเดียว

## Manual Setup

```bash
cp .env.example .env
# Edit .env with your values
docker compose build
docker compose up -d

# View auto-generated API key (first boot only)
docker compose logs api | grep "API_KEY:"
```

---

## Configuration System

### Infrastructure Config (`.env`)

ตั้งค่าเฉพาะโครงสร้างพื้นฐาน — **ไม่มี runtime settings ใน `.env`**

| Variable | คำอธิบาย |
|----------|----------|
| `DATABASE_URL` | PostgreSQL connection string |
| `REDIS_URL` | Redis connection string |
| `ADMIN_API_KEY` | รหัสผ่าน admin panel |
| `ADMIN_USERNAME` | Username admin (default: `admin`) |
| `ADMIN_PANEL_PATH` | URL path สำหรับเข้า admin panel |
| `R2_*` | Cloudflare R2 credentials |
| `RSPAMD_URL` | Rspamd service URL |
| `FRONTEND_URL` | Frontend URL for CORS |
| `LOG_FILE_PATH` | Log destination (default: `stdout`) |

### Runtime Settings (Admin Panel → Settings tab)

จัดการผ่าน Admin Panel — เก็บใน Redis — **ไม่ต้อง redeploy**

| Key | ค่าเริ่มต้น | คำอธิบาย |
|-----|-----------|----------|
| `webhook_url` | _(empty)_ | URL สำหรับ POST แจ้งเตือนเมื่อมีเมลเข้า |
| `webhook_secret` | _(empty)_ | HMAC secret สำหรับยืนยัน webhook |
| `default_mailbox_ttl_hours` | `24` | Mailbox หมดอายุกี่ชั่วโมง |
| `default_message_ttl_hours` | `24` | Message ลบอัตโนมัติกี่ชั่วโมง |
| `cleanup_interval_minutes` | `5` | Worker cleanup ทุกกี่นาที |
| `max_message_size_mb` | `25` | ขนาดอีเมลสูงสุด (MB) |
| `max_attachments` | `10` | ไฟล์แนบสูงสุดต่ออีเมล |
| `max_attachment_size_mb` | `10` | ขนาดไฟล์แนบสูงสุด (MB) |
| `spam_reject_threshold` | `15` | Spam score ที่จะปฏิเสธ |

### Application Config (Environment Variables)

ค่าที่ปรับได้ผ่าน env var โดยไม่ต้อง rebuild — มี default ที่เหมาะสม

| Variable | Default | คำอธิบาย |
|----------|---------|----------|
| `TZ` | `Asia/Bangkok` | Timezone สำหรับ timestamps |
| `PORT` | `4000` | API server port |
| `BODY_LIMIT_MB` | `40` | HTTP body limit (MB) |
| `PUBLIC_RATE_LIMIT` | `60` | Public API rate limit (req/min) |
| `LOGIN_RATE_LIMIT` | `10` | Admin login rate limit (req/min) |
| `API_READ_TIMEOUT` | `30s` | HTTP read timeout |
| `API_WRITE_TIMEOUT` | `30s` | HTTP write timeout |
| `API_IDLE_TIMEOUT` | `120s` | HTTP idle timeout |
| `SMTP_PORT` | `2525` | SMTP internal port |
| `SMTP_DOMAIN` | `tempmail.local` | SMTP server domain |
| `SMTP_MAX_MESSAGE_MB` | `25` | Max email size (MB) |
| `SMTP_MAX_RECIPIENTS` | `50` | Max recipients per email |
| `SMTP_RATE_LIMIT` | `50` | SMTP connections per IP/min |
| `RSPAMD_TIMEOUT` | `10s` | Rspamd scan timeout |
| `INGEST_TIMEOUT` | `30s` | Mail ingest API timeout |
| `WORKER_CONCURRENCY` | `10` | Background worker threads |
| `RETENTION_CRON` | `@hourly` | Message cleanup schedule |
| `MAILBOX_EXPIRE_CRON` | `*/5 * * * *` | Mailbox expiry schedule |

---

## Security

### API Keys (Admin Panel → API Keys tab)

- **ไม่มี API_TOKEN ใน `.env`** — จัดการจาก Admin Panel 100%
- First boot → **auto-generate default key** → แสดงใน docker logs ครั้งเดียว
- สร้าง/Revoke keys จาก Admin Panel → API Keys tab
- Middleware validate ผ่าน **Redis** (SHA-256 hash set) → O(1)
- mail-edge อ่าน raw key จาก Redis → ไม่ต้อง env

### Authentication

| Token | ใช้โดย | หน้าที่ |
|-------|--------|--------|
| **API Key** (Redis) | mail-edge + frontend → api | Auth สำหรับทุก API call |
| `ADMIN_API_KEY` (.env) | Admin login | รหัสผ่าน admin panel |
| `ADMIN_PANEL_PATH` (.env) | Browser | URL path for admin panel |

---

## Admin Panel

เข้าถึงได้ที่: `http://YOUR_IP:4000/ADMIN_PANEL_PATH/`

### Features

| Tab | Feature | คำอธิบาย |
|-----|---------|---------| 
| **Dashboard** | System Status | สถานะ Database, Redis, Rspamd, Worker, Mailserver |
| | Metrics | Mail throughput/hr, storage, spam stats |
| **Domains** | Domain Management | เพิ่ม/ลบ domain, assign ไปที่ node, DNS instructions อัตโนมัติ |
| | DNS Check | ตรวจ MX, A, SPF, DMARC records แบบ real-time |
| **Nodes** | Server Nodes | จัดการ server nodes (IP, region), primary node auto-registered |
| **Filters** | Domain Blocklist/Whitelist | บล็อก/อนุญาต sender domains, sync กับ Redis ทันที |
| **Mailboxes** | Mailbox Management | ดู/ค้นหา/ลบ mailboxes, filter by status |
| **Messages** | Message Management | ค้นหา messages, ดูเนื้อหาอีเมล + attachments |
| **API Keys** | API Key Management | สร้าง/Revoke API keys, ดู prefix + permissions |
| **Audit Log** | Audit Trail | ดูประวัติการกระทำทั้งหมด (action, target, IP, timestamp) |
| **Settings** | System Settings | ตั้งค่า Webhook, TTL, Rate Limit + Export/Import Config |

---

## REST API Endpoints

### Public (ผ่าน API Key)

| Method | Path | คำอธิบาย |
|--------|------|---------|
| POST | `/api/mailbox` | สร้าง mailbox ชั่วคราว |
| GET | `/api/mailbox/:id` | ดูข้อมูล mailbox |
| DELETE | `/api/mailbox/:id` | ลบ mailbox |
| GET | `/api/mailbox/:id/messages` | ดูรายการ messages |
| GET | `/api/messages/:id` | ดูเนื้อหา message |
| GET | `/api/domains` | รายการ domains ที่ใช้ได้ |

### Internal (ผ่าน API Key — Bearer token)

| Method | Path | คำอธิบาย |
|--------|------|---------|
| POST | `/internal/mail/ingest` | mail-edge ส่งเมลที่รับเข้ามา |

### Admin (ผ่าน session token จาก login)

| Method | Path | คำอธิบาย |
|--------|------|---------|
| POST | `/admin/login` | Login → ได้ session token |
| GET | `/admin/dashboard` | Dashboard stats + service status |
| GET | `/admin/metrics` | System metrics (throughput, storage) |
| GET/POST | `/admin/domains` | Domain CRUD |
| GET | `/admin/domains/dns-check?domain=xxx` | DNS record verification |
| GET/POST/DELETE | `/admin/nodes` | Node management |
| GET/POST/DELETE | `/admin/filters` | Domain blocklist/whitelist |
| GET | `/admin/mailboxes` | Mailbox list (pagination + search) |
| GET | `/admin/messages` | Message list (pagination + search) |
| GET | `/admin/messages/:id` | Full message content + attachments |
| GET/POST/DELETE | `/admin/api-keys` | API Key management |
| GET | `/admin/audit-log` | Audit trail |
| GET/POST | `/admin/settings` | System settings (Redis-based) |
| GET | `/admin/export` | Export config as JSON |
| POST | `/admin/import` | Import config from JSON |

---

## DNS Configuration

เมื่อเพิ่ม domain ในหน้า admin + assign node → ระบบแสดง DNS records ที่ต้องตั้งค่าอัตโนมัติ:

| Type | Name | Value | Proxy |
|------|------|-------|-------|
| **MX** | `example.com` | `mail.example.com` | — |
| **A** | `mail.example.com` | `YOUR_SERVER_IP` | **OFF** ⛅ |

> ⚠️ **Cloudflare**: ต้องปิด Proxy (DNS only / grey cloud) สำหรับ `mail.` record เพราะ SMTP port 25 ไม่ผ่าน Cloudflare proxy

### Optional (แนะนำ):
| Type | Name | Value |
|------|------|-------|
| **SPF** | `example.com` | `v=spf1 a mx ~all` |
| **DMARC** | `_dmarc.example.com` | `v=DMARC1; p=none;` |

---

## Node System

ระบบ Node ใช้สำหรับจัดการ multi-server:

- **Primary Node**: Auto-registered เมื่อ API เริ่มครั้งแรก (ดึง public IP อัตโนมัติ)
- **เพิ่ม Node**: ผ่าน Admin Panel → Nodes tab → "+ Add Node"
- **Assign Domain**: เมื่อเพิ่ม domain → เลือก node → ระบบแสดง DNS ที่ต้องตั้ง

> สำหรับ single server: ไม่ต้องสนใจ multi-node — primary node auto-register แล้ว

---

## Domain Filters

บล็อกหรืออนุญาต sender domains:

- **BLOCK**: ปฏิเสธเมลจาก domain นี้ (เสริม Rspamd)
- **ALLOW**: อนุญาตเสมอ (bypass spam check)
- รองรับ pattern: `spam.com`, `*.spam.com`
- Sync กับ Redis ทันทีที่เปลี่ยน — mail-edge ตรวจสอบ O(1)

---

## Export / Import

- **Export**: Settings tab → "Export Config" → ดาวน์โหลด JSON
- **Import**: Settings tab → "Import Config" → upload JSON ที่ export ไว้
- Export รวม: domains, nodes, filters, settings

---

## File Structure

```
mailserver/
├── api/                   # REST API server (Go + Fiber)
│   ├── admin-ui/          # Admin panel (HTML + CSS + JS)
│   │   ├── index.html     # HTML shell
│   │   ├── style.css      # Design system
│   │   └── app.js         # Application logic
│   ├── handlers/          # API handlers
│   │   ├── admin.go       # Admin endpoints + audit logging
│   │   ├── ingest.go      # Mail ingest from mail-edge
│   │   └── sdk.go         # Public SDK endpoints
│   └── main.go            # Server entry, routes, middleware
├── mail-edge/             # SMTP server (Go)
│   ├── main.go            # SMTP listener
│   └── smtp.go            # Mail processing + Rspamd
├── worker/                # Background jobs (Go)
│   └── main.go            # Cleanup expired mailboxes/messages
├── shared/                # Shared packages
│   ├── config/config.go   # 12-factor config (env → struct)
│   ├── models/models.go   # Database models (GORM)
│   ├── db/db.go           # DB + Redis connections
│   ├── logger/logger.go   # Structured logging (Zap)
│   ├── namegen/           # Human-readable name generator
│   └── apiutil/           # HTTP utilities
├── docker/                # Dockerfiles
├── docker-compose.yml     # Service orchestration
├── deploy.sh              # One-click deployment script
├── add-node.sh            # Add secondary node
├── remove-node.sh         # Remove secondary node
├── lib.sh                 # Shared shell utilities
├── .env.example           # Configuration template
└── README.md              # This file
```

---

## Models

| Model | ตาราง | คำอธิบาย |
|-------|-------|---------|
| `MailNode` | `mail_nodes` | Server nodes (name, IP, region) |
| `Domain` | `domains` | Domains with node assignment |
| `Mailbox` | `mailboxes` | Temporary email addresses |
| `Message` | `messages` | Received emails |
| `Attachment` | `attachments` | Email attachments metadata |
| `DomainFilter` | `domain_filters` | Blocklist/whitelist rules |
| `APIKey` | `api_keys` | API keys (hashed, managed via admin) |
| `AuditLog` | `audit_logs` | Admin action history |
| `User` | `users` | User accounts (RBAC) |
| `Role` | `roles` | User roles |
| `Permission` | `permissions` | Granular permissions |
| `Plan` | `plans` | Subscription plans |
| `Subscription` | `subscriptions` | User-plan links |

---

## Logging

- **Container environment**: stdout only (`LOG_FILE_PATH=stdout`)
- **Structured JSON**: via Zap logger
- **Access logs**: Request method, path, status, latency
- **Timezone**: Default `Asia/Bangkok` (configurable via `TZ` env var)

---

## Update (on running server)

```bash
cd /opt/mailserver && git pull origin master && \
sed -i '/^INTERNAL_API_TOKEN=/d; /^EXTERNAL_API_KEY=/d; /^API_TOKEN=/d; /^SPAM_REJECT_THRESHOLD=/d; /^MAX_ATTACHMENTS=/d; /^MAX_ATTACHMENT_SIZE_MB=/d; /^MAX_MESSAGE_SIZE_MB=/d; /^LOG_LEVEL=/d; /^LOG_MAX_/d' /opt/stacks/mailserver/.env && \
rsync -a --delete --exclude '.git' --exclude '.env' ./ /opt/stacks/mailserver/ && \
cd /opt/stacks/mailserver && \
docker compose build --no-cache && docker compose up -d --force-recreate

# View auto-generated API key (first boot after update)
docker compose logs api 2>&1 | grep "API_KEY:"
```

---

## License

MIT
