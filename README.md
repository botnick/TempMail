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
```

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
| **Audit Log** | Audit Trail | ดูประวัติการกระทำทั้งหมด |
| **Settings** | System Settings | ตั้งค่า Webhook, TTL, Rate Limit + Export/Import Config |

### Settings (Redis-based — ไม่ต้อง redeploy)

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

---

## Security Tokens (`.env`)

| Token | ใช้โดย | หน้าที่ |
|-------|--------|--------|
| `API_TOKEN` | mail-edge + frontend → api | Authentication สำหรับทุก API call |
| `ADMIN_API_KEY` | Admin login | รหัสผ่าน admin panel (username: `admin`) |
| `ADMIN_PANEL_PATH` | Browser | URL path สำหรับเข้า admin panel |


---

## REST API Endpoints

### Public (ผ่าน `EXTERNAL_API_KEY`)

| Method | Path | คำอธิบาย |
|--------|------|---------|
| POST | `/api/mailbox` | สร้าง mailbox ชั่วคราว |
| GET | `/api/mailbox/:id` | ดูข้อมูล mailbox |
| DELETE | `/api/mailbox/:id` | ลบ mailbox |
| GET | `/api/mailbox/:id/messages` | ดูรายการ messages |
| GET | `/api/messages/:id` | ดูเนื้อหา message |
| GET | `/api/domains` | รายการ domains ที่ใช้ได้ |

### Internal (ผ่าน `INTERNAL_API_TOKEN`)

| Method | Path | คำอธิบาย |
|--------|------|---------|
| POST | `/internal/ingest` | mail-edge ส่งเมลที่รับเข้ามา |

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
│   │   ├── admin.go       # Admin endpoints
│   │   ├── ingest.go      # Mail ingest from mail-edge
│   │   └── sdk.go         # Public SDK endpoints
│   └── main.go            # Server entry, routes, middleware
├── mail-edge/             # SMTP server (Go)
│   ├── main.go            # SMTP listener
│   └── handler.go         # Mail processing + Rspamd
├── worker/                # Background jobs (Go)
│   └── main.go            # Cleanup expired mailboxes/messages
├── shared/                # Shared packages
│   ├── models/models.go   # Database models (GORM)
│   ├── db/db.go           # DB + Redis connections
│   ├── logger/logger.go   # Structured logging (Zap)
│   └── apiutil/           # HTTP utilities
├── docker/                # Dockerfiles
├── docker-compose.yml     # Service orchestration
├── deploy.sh              # One-click deployment script
├── add-node.sh            # Add secondary node
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

---

## License

MIT
