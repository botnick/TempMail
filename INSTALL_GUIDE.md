# TempMail Platform — Complete Operations Guide

> **Backend mail service** — receive, filter, store, and serve temporary emails via API.  
> Plug `API_URL + API_KEY` into your main web application.

---

## Table of Contents

- [Architecture Overview](#architecture-overview)
- [Case 1: First Install (Single Node)](#case-1-first-install-single-node)
- [Case 2: Add Node (Scale Up)](#case-2-add-node-scale-up)
- [Case 3: Remove Node (Scale Down)](#case-3-remove-node-scale-down)
- [API Reference](#api-reference)
- [Admin Panel](#admin-panel)
- [Configuration Reference](#configuration-reference)
- [Maintenance & Troubleshooting](#maintenance--troubleshooting)

---

## Architecture Overview

```
Internet ──:25──▶ [mail-edge] ──▶ [Rspamd] ──multipart──▶ [api:4000]
                                                             │
                                                  ┌──────────┼──────────┐
                                               [Postgres]  [Redis]   [R2]
                                                             │
                                                         [worker]
                                                      (cron cleanup)

Your Web App ──HTTPS──▶ Nginx ──▶ [api:4000/v1/*]  (X-API-Key)
Admin        ──HTTPS──▶ Nginx ──▶ [api:4000/SECRET_PATH/]  (login)
```

| Service | Role | Technology |
|---------|------|-----------|
| **mail-edge** | SMTP receiver (port 25), spam check, forward to API | Go + go-smtp |
| **api** | REST API for SDK + Admin + mail ingest | Go + Fiber |
| **worker** | Background cleanup: expire mailboxes, delete messages + R2 objects | Go + Asynq |
| **Rspamd** | Spam filtering (DKIM, SPF, RBL, bayesian) | Rspamd |
| **Postgres** | Database: domains, mailboxes, messages, attachments | PostgreSQL 15 |
| **Redis** | Active mailbox set, Asynq job queue, caching | Redis 7 |
| **R2** | Object storage for raw emails + attachments | Cloudflare R2 |

### Security Features

- **Rspamd fail-close** — if Rspamd is unreachable, SMTP returns `451` (retry later), never accepts unchecked mail
- **Multipart/form-data** transfer — no base64 encoding overhead for email data
- **Configurable attachment caps** — `MAX_ATTACHMENTS` + `MAX_ATTACHMENT_SIZE_MB`
- **Spam warning fields** — `isSpam` + `quarantineAction` in API response for frontend badges
- **Smart rate limiting** — per-IP on public routes only, auth-protected routes have no IP limit
- **Admin brute-force protection** — 20 req/min per IP on admin routes
- **HTML sanitization** — bluemonday strips XSS/scripts from email HTML bodies
- **Automatic R2 cleanup** — expired messages + all attachments deleted from R2 hourly

---

## Case 1: First Install (Single Node)

### 1.1 Prerequisites

| Requirement | Minimum | Recommended |
|------------|---------|-------------|
| OS | Ubuntu 22.04 LTS | Ubuntu 22.04/24.04 |
| CPU | 1 vCPU | 2+ vCPU |
| RAM | 1 GB | 2+ GB |
| Disk | 20 GB SSD | 40+ GB SSD |
| Domain | 1 domain | multiple supported |
| Cloudflare R2 | 1 bucket | — |

### 1.2 Server Preparation

```bash
# Update OS and set timezone
sudo apt update && sudo apt upgrade -y
sudo timedatectl set-timezone UTC
sudo reboot

# Add swap (for 1GB RAM servers)
sudo fallocate -l 2G /swapfile
sudo chmod 600 /swapfile && sudo mkswap /swapfile && sudo swapon /swapfile
echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab

# Install Docker + Git
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER && newgrp docker
sudo apt install -y git

# Verify
docker --version        # 24.x+
docker compose version  # v2.x+
```

### 1.3 DNS Configuration

Add these records at your DNS provider:

| Type | Name | Value | Priority |
|------|------|-------|----------|
| **A** | `mail.example.com` | Your server IP | — |
| **MX** | `example.com` | `mail.example.com` | 10 |

```bash
# Verify DNS (wait 5-60 minutes for propagation)
dig +short mail.example.com A      # → your server IP
dig +short example.com MX          # → 10 mail.example.com.
```

### 1.4 Cloudflare R2 Setup

1. Dashboard → R2 → **Create bucket** → Name: `tempmail-archives`
2. R2 → **Manage API Tokens** → Create → Permission: Object Read & Write → Scope: `tempmail-archives`
3. Save: **Account ID**, **Access Key ID**, **Secret Access Key**

### 1.5 Clone & Deploy

```bash
cd /opt
sudo git clone https://your-repo/tempmail-mailserver.git mailserver
sudo chown -R $USER:$USER /opt/mailserver
cd /opt/mailserver

# One-click deploy (generates .env with secure random tokens)
chmod +x deploy.sh
./deploy.sh
```

**Save the output immediately** — it shows:
- `EXTERNAL_API_KEY` — for your web app
- `ADMIN_API_KEY` — for admin panel login
- `ADMIN_PANEL_PATH` — secret URL path for admin panel

### 1.6 Verify Installation

```bash
# All containers running
docker compose ps

# API health check
curl http://localhost:4000/health
# → {"status":"ok"}

# SMTP check
telnet mail.example.com 25
# → 220 ESMTP

# Create a test mailbox
curl -X POST http://localhost:4000/v1/mailbox/create \
  -H "X-API-Key: YOUR_EXTERNAL_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"localPart":"test","tenantId":"admin","ttlHours":24}'
# → {"id":"...","address":"test@example.com","expiresAt":"..."}
```

### 1.7 Nginx + SSL + Firewall

```bash
sudo apt install -y nginx certbot python3-certbot-nginx ufw

# Create Nginx config
sudo tee /etc/nginx/sites-available/tempmail-api <<'EOF'
server {
    listen 80;
    server_name api.example.com;
    location / {
        proxy_pass http://127.0.0.1:4000;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 300;
        client_max_body_size 40M;
    }
}
EOF

# Enable and get SSL
sudo ln -s /etc/nginx/sites-available/tempmail-api /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
sudo certbot --nginx -d api.example.com

# Firewall
sudo ufw allow 22/tcp    # SSH first!
sudo ufw allow 25/tcp    # SMTP
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
```

---

## Case 2: Add Node (Scale Up)

### Architecture After Adding a Node

```
                    MX priority 10          MX priority 20
Internet ──:25──▶ [Node 1: mail-edge] ──┐
Internet ──:25──▶ [Node 2: mail-edge] ──┤
                                        ▼
                           [Node 1: api + DB + Redis + worker]
```

> **Node 2 runs ONLY mail-edge** — no database, no worker, no API.  
> Your web app keeps using the same API URL and key — nothing changes.

### 2.1 Prepare Primary Server

```bash
# On PRIMARY server — expose Redis for Node 2

# Option A: Direct (private network only)
# Edit docker-compose.yml, add under redis:
# ports:
#   - "NODE2_IP:6379:6379"

# Option B: WireGuard VPN (recommended for public networks)
sudo apt install -y wireguard
wg genkey | tee /etc/wireguard/private.key | wg pubkey > /etc/wireguard/public.key

# Create /etc/wireguard/wg0.conf on primary:
# [Interface]
# PrivateKey = <PRIMARY_PRIVATE_KEY>
# Address = 10.0.0.1/24
# ListenPort = 51820
#
# [Peer]
# PublicKey = <NODE2_PUBLIC_KEY>
# AllowedIPs = 10.0.0.2/32

sudo wg-quick up wg0 && sudo systemctl enable wg-quick@wg0

# Open firewall for Node 2
sudo ufw allow from NODE2_IP to any port 4000   # API
sudo ufw allow from NODE2_IP to any port 6379   # Redis
sudo ufw allow from NODE2_IP to any port 11333  # Rspamd
```

### 2.2 Deploy on New Node

```bash
# On NODE 2 (new VPS)
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER && newgrp docker
sudo apt install -y git

cd /opt
git clone https://your-repo/tempmail-mailserver.git mailserver
cd /opt/mailserver

chmod +x add-node.sh
./add-node.sh

# It will ask for:
# Redis URL:         redis://:YOUR_REDIS_PASSWORD@PRIMARY_IP:6379
# Internal API URL:  http://PRIMARY_IP:4000/internal/mail/ingest
# Internal API Token: YOUR_INTERNAL_TOKEN
# Rspamd URL:        http://PRIMARY_IP:11333
```

### 2.3 Add DNS

| Type | Name | Value | Priority |
|------|------|-------|----------|
| **A** | `mail2.example.com` | Node 2 IP | — |
| **MX** | `example.com` | `mail2.example.com` | 20 |

**Result:**
- Priority 10 (Node 1) = primary, tried first
- Priority 20 (Node 2) = failover if Node 1 is down
- **Web app unchanged** — same API URL + key

### 2.4 Verify

```bash
# On Node 2
docker compose -f docker-compose.node.yml ps     # mail-edge = Up
telnet mail2.example.com 25                       # → 220 ESMTP

# On primary: check forwarded mail
docker compose logs api --tail 20 | grep "ingested"
```

---

## Case 3: Remove Node (Scale Down)

> **Zero downtime** — Node 2 has no state, mail auto-routes to remaining nodes.

### 3.1 Steps

```bash
# Step 1: Remove DNS records (wait 1 hour for propagation)
# Delete: MX example.com → mail2.example.com
# Delete: A mail2.example.com → IP
dig +short example.com MX  # Confirm mail2 is gone

# Step 2: Stop containers on the node being removed (after DNS propagated)
ssh node2
cd /opt/mailserver
docker compose -f docker-compose.node.yml down

# Step 3: Clean up primary server
sudo ufw delete allow from NODE2_IP to any port 6379
sudo ufw delete allow from NODE2_IP to any port 4000
sudo ufw delete allow from NODE2_IP to any port 11333

# If using WireGuard: remove [Peer] from /etc/wireguard/wg0.conf
sudo wg-quick down wg0 && sudo wg-quick up wg0

# Step 4: (Optional) Destroy VPS
docker system prune -a --volumes -f
rm -rf /opt/mailserver
```

### 3.2 Verification Checklist

| Check | Command | Expected |
|-------|---------|----------|
| Web app works | `curl https://api.example.com/health` | `{"status":"ok"}` |
| Create mailbox | `curl -X POST .../v1/mailbox/create` | 201 Created |
| Receive mail | Send test email | Appears in inbox |
| Admin panel | Open browser | Dashboard loads |
| DNS clean | `dig +short example.com MX` | No removed node |
| Firewall clean | `sudo ufw status` | No old node rules |

---

## API Reference

**Base URL:** `https://api.example.com`  
**Auth Header:** `X-API-Key: YOUR_EXTERNAL_API_KEY`

### Create Mailbox

```bash
curl -X POST https://api.example.com/v1/mailbox/create \
  -H "X-API-Key: YOUR_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "localPart": "john",
    "tenantId": "user_123",
    "ttlHours": 48
  }'
```

Response:
```json
{
  "id": "mb_abc123",
  "address": "john@example.com",
  "expiresAt": "2026-03-14T06:00:00Z"
}
```

### List Messages

```bash
curl https://api.example.com/v1/mailbox/mb_abc123/messages \
  -H "X-API-Key: YOUR_KEY"
```

Response:
```json
{
  "mailboxId": "mb_abc123",
  "count": 2,
  "messages": [
    {
      "id": "msg_xyz",
      "from": "noreply@service.com",
      "subject": "Verify your email",
      "spamScore": 0.5,
      "isSpam": false,
      "quarantineAction": "ACCEPT",
      "hasHtml": true,
      "receivedAt": "2026-03-12T10:30:00Z",
      "expiresAt": "2026-03-14T10:30:00Z"
    }
  ]
}
```

> **Note:** Use `isSpam: true` to show a spam warning badge in your UI.

### Read Full Message

```bash
curl https://api.example.com/v1/message/msg_xyz \
  -H "X-API-Key: YOUR_KEY"
```

### Delete Mailbox

```bash
curl -X DELETE https://api.example.com/v1/mailbox/mb_abc123 \
  -H "X-API-Key: YOUR_KEY"
```

### List Domains

```bash
curl https://api.example.com/v1/domains \
  -H "X-API-Key: YOUR_KEY"
```

### Frontend Integration Example

```javascript
const API_URL = 'https://api.example.com';
const API_KEY = 'YOUR_EXTERNAL_API_KEY';

const headers = { 'X-API-Key': API_KEY, 'Content-Type': 'application/json' };

// Create mailbox
const { id, address } = await fetch(`${API_URL}/v1/mailbox/create`, {
  method: 'POST', headers,
  body: JSON.stringify({ localPart: 'john', tenantId: 'user_1', ttlHours: 24 })
}).then(r => r.json());

// Poll for messages
const { messages } = await fetch(`${API_URL}/v1/mailbox/${id}/messages`, { headers })
  .then(r => r.json());

// Display with spam warning
messages.forEach(msg => {
  if (msg.isSpam) console.warn(`⚠️ Spam detected: ${msg.subject}`);
});
```

---

## Admin Panel

**URL:** `https://api.example.com/YOUR_ADMIN_PANEL_PATH/`  
**Login:** Enter your `ADMIN_API_KEY`

| Tab | Functions |
|-----|----------|
| **Dashboard** | Total domains, active mailboxes, messages, spam blocked, today's traffic |
| **Domains** | Add / delete email domains |
| **Mailboxes** | Search, view, delete mailboxes |
| **Messages** | Search messages across all mailboxes |
| **Audit Log** | Track all admin actions |
| **Settings** | Adjust system configuration |

---

## Configuration Reference

All settings in `.env`:

| Variable | Default | Description |
|----------|---------|-------------|
| `POSTGRES_USER` | tempmail | Database username |
| `POSTGRES_PASSWORD` | (generated) | Database password |
| `POSTGRES_DB` | tempmail_db | Database name |
| `REDIS_PASSWORD` | (generated) | Redis password |
| `INTERNAL_API_TOKEN` | (generated) | mail-edge → api auth |
| `EXTERNAL_API_KEY` | (generated) | Web app → api auth |
| `ADMIN_API_KEY` | (generated) | Admin panel login |
| `ADMIN_PANEL_PATH` | (generated) | Secret admin URL path |
| `FRONTEND_URL` | *(empty)* | CORS origin — ใส่เฉพาะเมื่อ browser เรียก API ตรง ถ้าเรียกจาก server-side ไม่ต้องใส่ |
| `R2_ACCOUNT_ID` | — | Cloudflare R2 account |
| `R2_ACCESS_KEY_ID` | — | R2 access key |
| `R2_SECRET_ACCESS_KEY` | — | R2 secret key |
| `R2_BUCKET_NAME` | tempmail-archives | R2 bucket name |
| `SPAM_REJECT_THRESHOLD` | 15 | Score above this = reject |
| `MAX_MESSAGE_SIZE_MB` | 25 | Max email size |
| `MAX_ATTACHMENTS` | 10 | Max attachments per email |
| `MAX_ATTACHMENT_SIZE_MB` | 10 | Max single attachment size |
| `LOG_LEVEL` | info | Logging level |
| `LOG_MAX_AGE_DAYS` | 14 | Log retention days |

---

## Maintenance & Troubleshooting

### Common Commands

```bash
# View logs
docker compose logs -f api          # API logs
docker compose logs -f mail-edge    # SMTP logs
docker compose logs -f worker       # Cleanup logs

# Restart a service
docker compose restart api

# Rebuild after code changes
docker compose build --no-cache api
docker compose up -d api

# Database backup
docker compose exec postgres pg_dump -U tempmail tempmail_db > backup.sql

# Check disk usage
docker system df
```

### Common Issues

| Problem | Cause | Fix |
|---------|-------|-----|
| Port 25 blocked | Cloud provider blocks SMTP | Request SMTP unblock or use a relay |
| Emails not arriving | DNS not propagated | Wait + verify `dig +short domain MX` |
| 451 spam check error | Rspamd container down | `docker compose restart rspamd` |
| R2 upload fails | Bad credentials | Verify R2 keys in `.env` |
| Admin panel 404 | Wrong path | Check `ADMIN_PANEL_PATH` in deploy output |
| Rate limit on SDK | Old global limiter | Update code — SDK routes have no IP limit |

### Health Monitoring

```bash
# Quick health check script
#!/bin/bash
API="http://localhost:4000"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" $API/health)
if [ "$HTTP_CODE" != "200" ]; then
  echo "ALERT: API is down! Status: $HTTP_CODE"
  # Add your alerting here (email, Slack, etc.)
fi
```
