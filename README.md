# TempMail — Disposable Temporary Email Backend

> **Standalone SMTP server** ที่รับเมลจริงจากอินเทอร์เน็ต → กรอง Spam → เก็บให้เว็บหลักดึงผ่าน **REST API**

*Current Version: **v3.2.0** (Internal API Key Bypass, Attachment Auth, Reader Tab Fix)*

---

## 📖 Documentation

| ไฟล์ | รายละเอียด |
|------|-----------|
| **[README.md](README.md)** | ภาพรวมระบบ, สถาปัตยกรรม, การติดตั้ง, configuration (ไฟล์นี้) |
| **[INSTALL_GUIDE.md](INSTALL_GUIDE.md)** | คู่มือติดตั้งแบบ step-by-step (ภาษาอังกฤษ) |
| **[INSTALL_GUIDE_TH.html](INSTALL_GUIDE_TH.html)** | คู่มือติดตั้งแบบ visual — เปิดใน browser (ภาษาไทย) |
| **[API_INTEGRATION.md](API_INTEGRATION.md)** | คู่มือ API สำหรับ developer — endpoints, auth, examples |
| **[API_INTEGRATION_TH.html](API_INTEGRATION_TH.html)** | คู่มือ API + deploy.sh — เปิดใน browser (ภาษาไทย) |
| **[API_TESTER.html](API_TESTER.html)** | เครื่องมือทดสอบ API แบบ visual — เปิดใน browser ทดสอบได้เลย |
| **[ARCHITECTURE.md](ARCHITECTURE.md)** | เอกสารสถาปัตยกรรมเชิงลึก, async queue, scaling path |

---

### 🚀 What's New in v3.0.0
**⚡ Async Mail Processing — Major Architecture Change**
- **SMTP → Redis Queue → Worker**: Email ingestion ทำแบบ async ผ่าน Asynq/Redis queue
- **SMTP response <5ms**: Mail-edge แค่ validate + Rspamd + enqueue แล้วตอบ OK ทันที
- **25,000+ concurrent emails**: Redis buffer ล้านๆ messages, worker ประมวลผลแบบ parallel
- **Worker concurrency 50**: ตั้งค่าได้ผ่าน `WORKER_CONCURRENCY` (scale ได้ถึง 200+)
- **Redis pool 200 connections**: รองรับ burst traffic ระดับประเทศ
- **Strict priority queue**: Ingest queue (priority 60) > Maintenance queue (priority 10)
- **Unified Dockerfile**: รวม 3 Dockerfiles เป็นไฟล์เดียว multi-stage, build เร็วขึ้น 3x

**🖼️ Email Viewer Improvements**
- **Inline image display**: รูปภาพ CID ในเมล์แสดงผลได้โดยตรง
- **Client-side body decode**: Auto-detect Base64/Quoted-Printable แล้ว decode ฝั่ง admin UI

**🔒 Security Fixes**
- **Webhook JSON injection**: ป้องกัน injection ผ่าน proper JSON marshaling
- **Asynq Timeout**: แก้ไข timeout จาก 120ns → 120 seconds

### 📡 What's New in v3.1.0
**⚡ Admin Panel UX Overhaul**
- **SSE Real-time Events**: Messages tab อัพเดทอัตโนมัติไม่ต้อง polling — ใช้ Server-Sent Events ผ่าน Redis pub/sub
- **Skeleton Loading**: Proper shimmer skeleton rows ตรงตามโครงสร้างตารางจริงทุก tab
- **Dynamic Attachment Preview**: แสดง inline preview — รูปภาพ, PDF (iframe), video, audio ในหน้า admin
- **Premium Action Buttons**: Gradient fills, hover lift animations, emoji icons
- **Dashboard Grid Balance**: 4-column metrics, 3-column services — ไม่แหว่ง
- **Smooth Transitions**: FadeIn animation สำหรับ table rows, silent SSE refresh ไม่กระพริบ

**📋 Complete Audit Trail**
- **mail_ingested**: ทุกเมลที่ถูก process จะถูกบันทึก audit log พร้อม from/to/subject/spam score
- **mailbox_expired**: ทุก mailbox ที่หมดอายุจะถูกบันทึกพร้อม TTL timestamp
- **retention_cleanup**: ทุกรอบ cleanup จะถูกบันทึกพร้อมจำนวน messages/attachments/R2 objects ที่ลบ

**🚀 Performance**
- **Font preloading**: dns-prefetch + preconnect + preload สำหรับ Google Fonts
- **Deferred script loading**: app.js โหลดแบบ defer ไม่ block initial paint

### 🔑 What's New in v3.2.0
**⚡ Internal API Key Bypass**
- **Internal Mode toggle**: API Keys tab → ⚡ toggle สำหรับ mark key ว่าเป็น internal
- **Rate limit bypass**: Internal keys ไม่ถูก rate limiting — เหมาะสำหรับ background workers / internal services
- **Redis meta sync**: `isInternal` flag sync ไปใน `system:api_key_meta` hash สำหรับ O(1) lookup
- **∞ Unlimited display**: Admin panel แสดง "∞ Unlimited" สำหรับ internal keys

**🔒 Security Fixes**
- **Attachment auth**: endpoint ต้องการ `Authorization: Bearer` header — direct URL access ถูกปฏิเสธ
- **Reader tab fix**: แก้ `about:blank` sandbox block โดยใช้ `blob:` URL แทน

---

## Architecture Overview

```
Internet (SMTP)
    │
    ▼
┌─────────────────────────────────────────────────────────┐
│  mail-edge:25                                            │
│  ┌─────────┐  ┌──────────┐  ┌───────────────────────┐  │
│  │ Accept   │→│ Rspamd   │→│ Redis Queue (Asynq)    │  │
│  │ SMTP     │  │ Spam     │  │ enqueue: mail:ingest  │  │
│  │ session  │  │ Check    │  │ → SMTP OK (<5ms)      │  │
│  └─────────┘  └──────────┘  └───────────────────────┘  │
└─────────────────────────────────────────────────────────┘
                                        │
                                        ▼
┌─────────────────────────────────────────────────────────┐
│  worker (50 concurrent goroutines)                       │
│  ┌──────────┐  ┌────────┐  ┌──────────┐  ┌──────────┐  │
│  │ Parse    │→│ Upload  │→│ Insert   │→│ Webhook  │  │
│  │ MIME     │  │ to R2   │  │ to DB    │  │ notify   │  │
│  │ RFC822   │  │ .eml +  │  │ message  │  │ (async)  │  │
│  │ + attach │  │ attach  │  │ + attach │  │          │  │
│  └──────────┘  └────────┘  └──────────┘  └──────────┘  │
└─────────────────────────────────────────────────────────┘
                                        │
                    ┌───────────────────┼───────────────────┐
                    ▼                   ▼                   ▼
              ┌──────────┐      ┌──────────┐       ┌──────────┐
              │ PostgreSQL│      │  Redis    │       │ R2 (S3)  │
              │ messages  │      │  queue +  │       │ raw .eml │
              │ mailboxes │      │  cache    │       │ attach   │
              └──────────┘      └──────────┘       └──────────┘
                                        ▲
                                        │
              ┌─────────────────────────┘
              │
┌─────────────────────────────────────────────────────────┐
│  api:4000 (REST API + Admin Panel)                       │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐              │
│  │ SDK API  │  │ Admin    │  │ Static   │              │
│  │ /v1/*    │  │ Panel    │  │ admin-ui │              │
│  │ X-API-Key│  │ /admin/* │  │ HTML/JS  │              │
│  └──────────┘  └──────────┘  └──────────┘              │
└─────────────────────────────────────────────────────────┘
              ▲
              │ HTTPS
┌─────────────────────────┐
│  Your Web App / Frontend │
└─────────────────────────┘
```

### Service Roles
| Service | หน้าที่ | เทคโนโลยี | Port |
|---------|---------|-----------|------|
| **mail-edge** | SMTP receiver → Rspamd spam check → enqueue Redis queue | Go + go-smtp + Asynq | `25` |
| **api** | REST API (SDK + Admin) | Go + Fiber | `4000` |
| **worker** | Async mail processor (MIME parse + R2 upload + DB insert) + retention cleanup | Go + Asynq | — |
| **postgres** | Database หลัก (domains, mailboxes, messages, attachments, audit) | PostgreSQL 15 | `5432` |
| **redis** | Asynq job queue + active mailbox set + cache + settings | Redis 7 | `6379` |
| **rspamd** | Spam filtering (DKIM, SPF, RBL, bayesian, fuzzy hash) | Rspamd | `11333` |

### Inbound Email Flow (Step-by-Step)
```
1. SMTP Connection     → mail-edge รับ connection จาก sender
2. RCPT TO Validation  → ตรวจ Redis set O(1) — mailbox ไม่มี/หมดอายุ → reject ทันที
3. DATA Reception      → stream email เข้า buffer (max 25MB default)
4. Rspamd Spam Check   → ส่ง raw email ให้ Rspamd ตรวจ score
                         - action=reject หรือ score>threshold → SMTP 550 ปฏิเสธ
                         - action=accept → ACCEPT
                         - action=greylist/add header → QUARANTINE (เก็บแต่ทำเครื่องหมาย)
5. Async Enqueue       → สร้าง Asynq task {from, to, raw_email, spam_score, action}
                         ส่งเข้า Redis queue "ingest" — SMTP ตอบ 250 OK ทันที (<5ms)
6. Worker Dequeue      → worker หยิบ task จาก queue
7. MIME Parse          → parse RFC822: Subject, TextBody, HTMLBody, Attachments
8. HTML Sanitize       → bluemonday UGC policy ลบ XSS/JS ออกจาก HTML body
9. R2 Upload           → upload raw .eml + ไฟล์แนบทั้งหมดขึ้น Cloudflare R2
10. DB Insert          → insert message + attachment records เข้า PostgreSQL
11. Webhook Fire       → POST ไปที่ webhook URL (ถ้าตั้งค่าไว้) แบบ fire-and-forget
```

### Queue Architecture
| Queue | Priority | เนื้อหา | Retry | Timeout |
|-------|----------|---------|-------|---------|
| `ingest` | **60** (สูงสุด) | mail:ingest tasks — email ใหม่ทุกฉบับ | 5 ครั้ง | 120 วินาที |
| `maintenance` | 10 (ต่ำ) | retention cleanup, mailbox expiry | 3 ครั้ง | — |

> **StrictPriority = true**: Worker จะประมวลผล `ingest` queue จนหมดก่อน แล้วค่อยทำ `maintenance`

---

## Performance & Capacity

### Throughput Estimates
| สถานการณ์ | ผู้ใช้พร้อมกัน | เมล์/คน | Total emails | สถานะ |
|-----------|-------------|---------|-------------|-------|
| ปกติ | 500 | 1-2 | 500-1,000 | ✅ สบายมาก |
| ช่วงพีค | 2,000 | 1-3 | 2,000-6,000 | ✅ รองรับได้ |
| หนักมาก | 5,000 | 1-5 | 5,000-25,000 | ✅ Redis buffer + worker async |
| Stress/DDoS | 10,000+ | N/A | 100,000+ burst | ✅ ระบบไม่ล่ม — queue ยาวขึ้น worker ค่อยๆ ทำ |

### ทำไมระบบไม่ล่ม?
1. **mail-edge** แค่ enqueue — ไม่ทำอะไรหนัก (<5ms ต่อเมล์)
2. **Redis** buffer ได้ **ล้านๆ** tasks — ไม่มี memory limit ที่ practical
3. **Worker** ทำ 50 tasks พร้อมกัน (configurable, scale ได้ถึง 200+)
4. ต่อให้ burst 100k emails → mail-edge accept หมดใน <1 วินาที → worker ค่อยๆ process
5. **Backpressure ที่ถูกต้อง**: ถ้า worker ช้า → queue ยาวขึ้น → ไม่กระทบ SMTP throughput

### Connection Pools
| Resource | Pool Size | Min Idle | Purpose |
|----------|----------|----------|---------|
| **PostgreSQL** | 100 | 10 | DB connections for API + Worker |
| **Redis** | 200 | 30 | Queue ops + cache + mailbox validation |

---

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
| `R2_ACCOUNT_ID` | Cloudflare R2 account ID |
| `R2_ACCESS_KEY_ID` | R2 access key |
| `R2_SECRET_ACCESS_KEY` | R2 secret key |
| `R2_BUCKET_NAME` | R2 bucket name (default: `tempmail-archives`) |
| `RSPAMD_URL` | Rspamd service URL |
| `RSPAMD_PASSWORD` | Rspamd password (optional) |
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
| `SMTP_RATE_LIMIT` | `50` | SMTP per-IP rate limit (connections/min) |
| `RSPAMD_TIMEOUT` | `10s` | Rspamd scan timeout |
| `WORKER_CONCURRENCY` | `50` | Worker concurrent goroutines |
| `RETENTION_CRON` | `@hourly` | Message cleanup schedule |
| `MAILBOX_EXPIRE_CRON` | `*/5 * * * *` | Mailbox expiry check schedule |
| `SPAM_REJECT_THRESHOLD` | `15` | Spam score threshold (env override) |

---

## Security

### API Keys (Admin Panel → API Keys tab)
- **ไม่มี API_TOKEN ใน `.env`** — จัดการจาก Admin Panel 100%
- First boot → **auto-generate default key** → แสดงใน docker logs ครั้งเดียว
- สร้าง/Revoke keys จาก Admin Panel → API Keys tab
- Middleware validate ผ่าน **Redis** (SHA-256 hash set) → O(1)
- **Internal Mode**: mark key ว่า internal → bypass rate limiting → เหมาะสำหรับ background workers

```
Admin creates key → raw shown ONCE → SHA-256 hash stored in DB
         ↓
SyncAPIKeysToRedis():
  system:api_key_hashes  ← SHA-256 hashes (auth check)
  system:api_key_meta    ← isInternal flag per key (rate limit bypass)
         ↓
Request → hash → SISMEMBER → 200/401
          isInternal=true → skip rate limiting
```

### Authentication
| Token | ใช้โดย | หน้าที่ |
|-------|--------|--------|
| **API Key** (Redis) | Frontend web app → api | Auth สำหรับ SDK API calls (`X-API-Key` header) |
| `ADMIN_API_KEY` (.env) | Admin login | รหัสผ่าน admin panel |
| `ADMIN_PANEL_PATH` (.env) | Browser | URL path ลับสำหรับเข้า admin panel |

### Rate Limiting
| จุด | Limit | คำอธิบาย |
|-----|-------|---------|
| **SMTP** (mail-edge) | 50 conn/IP/min | ป้องกัน SMTP abuse จาก IP เดียว |
| **Public API** (api) | 60 req/min | SDK endpoint rate limit |
| **Admin Login** (api) | 10 req/min | Brute-force protection |
| **Internal API Key** | ∞ ไม่จำกัด | Bypass สำหรับ trusted services |

---

## Admin Panel

เข้าถึงได้ที่: `http://YOUR_IP:4000/ADMIN_PANEL_PATH/`

| Tab | Feature | คำอธิบาย |
|-----|---------|---------| 
| **Dashboard** | System Status | สถานะ Database, Redis, Rspamd, Worker, Mailserver |
| | Metrics | Mail throughput/hr, storage, spam stats, Go runtime memory/CPU |
| **Domains** | Domain CRUD | เพิ่ม/แก้ไข/ลบ domain, assign node, DNS instructions อัตโนมัติ |
| | DNS Check | ตรวจ MX, A, SPF, DMARC records แบบ real-time |
| **Nodes** | Server Node CRUD | สร้าง/แก้ไข/ลบ server nodes (IP, region) |
| **Filters** | Domain Filter CRUD | บล็อก/อนุญาต sender domains, แก้ไข/ลบ, sync Redis |
| **Mailboxes** | Mailbox Management | ค้นหา/ลบ mailboxes, filter by status, ⚡ Quick Create สำหรับทดสอบ |
| **Messages** | Message Management | ค้นหา/ดู/ลบ messages, Gmail-style reader panel (HTML/Text/Source tabs) |
| **API Keys** | API Key CRUD | สร้าง/แก้ไข/Revoke API keys, toggle ⚡ Internal Mode |
| **Audit Log** | Audit Trail | ดูประวัติการกระทำทั้งหมด, dynamic filter, ค้นหา IP/User/Target |
| **Settings** | System Settings | ตั้งค่า Webhook, TTL, Limits + 🔔 Test Webhook + Export/Import Config |

---

## REST API Endpoints

### SDK — Public API (ผ่าน `X-API-Key` header)
| Method | Path | คำอธิบาย |
|--------|------|---------|
| GET | `/v1/domains` | รายการ domains ที่ใช้ได้ |
| POST | `/v1/mailbox/create` | สร้าง mailbox ชั่วคราว |
| GET | `/v1/mailbox/:id` | ดูข้อมูล mailbox + message count |
| PATCH | `/v1/mailbox/:id` | ต่ออายุ TTL / เปลี่ยนสถานะ |
| DELETE | `/v1/mailbox/:id` | ลบ mailbox (soft delete) |
| GET | `/v1/mailbox/:id/messages` | ดูรายการ messages (ล่าสุด 100 รายการ) |
| GET | `/v1/mailbox/count` | นับ mailbox ของ tenant |
| GET | `/v1/message/:id` | ดูเนื้อหา message ครบ (body + attachments) |
| DELETE | `/v1/message/:id` | ลบ message (hard delete) |
| GET | `/v1/attachment/:id` | ดาวน์โหลด attachment (ต้องการ Bearer token) |

> 📝 ดูรายละเอียดทุก endpoint + code examples ที่ **[API_INTEGRATION.md](API_INTEGRATION.md)**

### Admin (ผ่าน session token จาก login)
| Method | Path | คำอธิบาย |
|--------|------|---------|
| POST | `/admin/login` | Login → ได้ session token |
| GET | `/admin/dashboard` | Dashboard stats + service status |
| GET | `/admin/metrics` | System metrics (throughput, storage, runtime) |
| GET/POST/PUT/DELETE | `/admin/domains[/:id]` | Domain CRUD |
| GET | `/admin/domains/dns-check?domain=xxx` | DNS record verification |
| GET/POST/PUT/DELETE | `/admin/nodes[/:id]` | Node CRUD |
| GET/POST/PUT/DELETE | `/admin/filters[/:id]` | Filter CRUD |
| GET/DELETE | `/admin/mailboxes[/:id]` | Mailbox list + delete |
| POST | `/admin/mailboxes/quick-create` | ⚡ สร้าง mailbox ทดสอบ (1 ชม.) |
| GET/DELETE | `/admin/messages[/:id]` | Message list + view + delete |
| GET/POST/PUT/DELETE | `/admin/api-keys[/:id]` | API Key CRUD (รองรับ isInternal field) |
| GET | `/admin/audit-log` | Audit trail (with dynamic filtering) |
| GET/POST | `/admin/settings` | System settings (Redis-based) |
| GET | `/admin/export` | Export config as JSON |
| POST | `/admin/import` | Import config from JSON |
| POST | `/admin/webhook-test` | 🔔 ทดสอบ webhook |
| GET | `/admin/events?token=xxx` | 📡 SSE real-time events (new_message, mailbox_expired) |

### Internal (Legacy — backward compatible)
| Method | Path | คำอธิบาย |
|--------|------|---------|
| POST | `/internal/mail/ingest` | Legacy: v2.x direct HTTP ingest (v3.0 ใช้ Redis queue แทน) |

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
- **Secondary Node Script**: `add-node.sh` สำหรับติดตั้ง mail-edge ใน server เสริม

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
├── api/                        # REST API server (Go + Fiber)
│   ├── admin-ui/               # Admin panel (HTML + CSS + JS)
│   │   ├── index.html          # HTML shell
│   │   ├── style.css           # Design system (dark theme, glassmorphism)
│   │   └── app.js              # Application logic (SPA, tabs, email reader)
│   ├── handlers/               # API handlers
│   │   ├── admin.go            # Admin endpoints + audit logging
│   │   ├── ingest.go           # Legacy mail ingest (HTTP, v2.x compat)
│   │   └── sdk.go              # Public SDK endpoints (/v1/*)
│   └── main.go                 # Server entry, routes, middleware, CORS
│
├── mail-edge/                  # SMTP server (Go)
│   ├── main.go                 # SMTP listener + Asynq client init
│   └── smtp.go                 # Spam check + async enqueue to Redis queue
│
├── worker/                     # Background jobs (Go + Asynq)
│   ├── main.go                 # Worker server + scheduler + R2 client
│   └── ingest_handler.go       # Async mail processor (MIME→R2→DB→webhook)
│
├── shared/                     # Shared packages (ใช้ร่วมกันทุก module)
│   ├── config/config.go        # 12-factor config (env → struct + defaults)
│   ├── models/models.go        # Database models (GORM auto-migrate)
│   ├── db/db.go                # DB + Redis connections (pooled: PG 100, Redis 200)
│   ├── logger/logger.go        # Structured logging (Zap JSON + file rotation)
│   ├── tasks/tasks.go          # Asynq task definitions (mail:ingest)
│   ├── namegen/                # Human-readable name generator (cool.fox42 style)
│   └── apiutil/                # HTTP response utilities
│
├── docker/                     # Unified multi-stage Dockerfile (3 targets)
│   └── Dockerfile              # base-builder → api/mail-edge/worker → distroless
│
├── docker-compose.yml          # Service orchestration (6 services)
├── deploy.sh                   # One-click deployment script
├── add-node.sh                 # Add secondary mail-edge node
├── remove-node.sh              # Remove secondary node
├── lib.sh                      # Shared shell utilities (version, colors)
├── .env.example                # Configuration template
├── API_INTEGRATION.md          # Developer API guide
├── API_INTEGRATION_TH.html     # API guide (Thai, visual)
├── API_TESTER.html             # Interactive API tester
├── ARCHITECTURE.md             # Deep architecture doc
├── INSTALL_GUIDE.md            # Installation guide (English)
├── INSTALL_GUIDE_TH.html       # Installation guide (Thai, visual)
└── README.md                   # This file
```

---

## Database Models
| Model | ตาราง | คำอธิบาย |
|-------|-------|---------|
| `MailNode` | `mail_nodes` | Server nodes (name, IP, region, status) |
| `Domain` | `domains` | Domains with node assignment + MX config |
| `Mailbox` | `mailboxes` | Temporary email addresses (local_part + domain + TTL) |
| `Message` | `messages` | Received emails (subject, body, spam score, R2 key) |
| `Attachment` | `attachments` | Email attachment metadata + R2 key |
| `DomainFilter` | `domain_filters` | Blocklist/whitelist rules (pattern match) |
| `APIKey` | `api_keys` | API keys (SHA-256 hashed, isInternal flag, managed via admin) |
| `AuditLog` | `audit_logs` | Admin action history (action, target, IP, timestamp) |

---

## Logging
- **Container environment**: stdout only (`LOG_FILE_PATH=stdout`)
- **Structured JSON**: via Zap logger — every log entry is parseable
- **Access logs**: Request method, path, status, latency
- **Mail processing logs**: task_id, queue, from, to, spam_score, size_bytes
- **Timezone**: Default `Asia/Bangkok` (configurable via `TZ` env var)

---

## Scaling Guide

### Phase 1: Single VPS (Current)
ทุกอย่างรันผ่าน `docker-compose.yml` — WORKER_CONCURRENCY=50

### Phase 2: Tune Worker
ปรับ `WORKER_CONCURRENCY=100-200` ตามจำนวน CPU core

### Phase 3: Managed Data
แยก PostgreSQL → AWS RDS, Redis → AWS ElastiCache

### Phase 4: Horizontal Scale
- **mail-edge**: ×N ตัวหลัง Load Balancer (port 25) — stateless, share Redis
- **worker**: ×N ตัว — Asynq กระจาย task อัตโนมัติ
- **api**: ×N ตัวหลัง ALB — stateless

---

## Update (on running server)
```bash
cd /opt/mailserver && git pull origin master && \
rsync -a --delete --exclude '.git' --exclude '.env' ./ /opt/stacks/mailserver/ && \
cd /opt/stacks/mailserver && \
docker compose build --no-cache && docker compose up -d --force-recreate

# View auto-generated API key (first boot after update)
docker compose logs api 2>&1 | grep "API_KEY:"
```

---

## License
MIT
