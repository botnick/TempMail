# TempMail — Installation Guide

> Step-by-step guide for deploying TempMail on a fresh VPS.

---

## Requirements

| Item | Minimum |
|------|---------|
| OS | Ubuntu 22.04 LTS (or Debian 12) |
| RAM | 1 GB |
| CPU | 1 vCPU |
| Disk | 20 GB |
| Ports open | 25/tcp (SMTP), 4000/tcp (API) |
| Domain | A domain you control DNS for |
| Cloudflare R2 | Free tier account with a bucket |

---

## Step 1 — Prepare the Server

```bash
# SSH into your VPS, then:
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER && exit

# SSH back in, then:
sudo apt install -y git
```

---

## Step 2 — Clone the Repository

```bash
cd /opt
sudo git clone https://github.com/botnick/TempMail.git mailserver
sudo chown -R $USER:$USER /opt/mailserver
cd /opt/mailserver
```

---

## Step 3 — Generate `.env`

Open `INSTALL_GUIDE_TH.html` in a browser (or copy it to your laptop first).  
Use the **"Generate .env"** button to create a pre-filled `.env` file with random secrets.

Alternatively, create it manually:

```bash
cat > .env << 'EOF'
# --- Database ---
POSTGRES_USER=tempmail
POSTGRES_PASSWORD=CHANGE_ME_RANDOM
POSTGRES_DB=tempmail_db
DATABASE_URL=host=postgres user=tempmail password=CHANGE_ME_RANDOM dbname=tempmail_db port=5432 sslmode=disable TimeZone=UTC

# --- Redis ---
REDIS_PASSWORD=CHANGE_ME_RANDOM
REDIS_URL=redis://:CHANGE_ME_RANDOM@redis:6379

# --- Admin Panel ---
ADMIN_API_KEY=CHANGE_ME_RANDOM_SECRET       # This is your admin login password
ADMIN_USERNAME=admin
ADMIN_PANEL_PATH=CHANGE_ME_SECRET_SLUG      # e.g. "myadmin7382" — keep this private

# --- Cloudflare R2 (attachment storage) ---
R2_ACCOUNT_ID=your_account_id
R2_ACCESS_KEY_ID=your_r2_key_id
R2_SECRET_ACCESS_KEY=your_r2_secret
R2_BUCKET_NAME=tempmail-archives

# --- Spam Filter ---
RSPAMD_PASSWORD=

# --- Optional ---
FRONTEND_URL=                               # Set if your frontend is on a different origin (enables CORS)
LOG_LEVEL=info
TZ=Asia/Bangkok
EOF
```

> ⚠️ **All `CHANGE_ME_*` values must be replaced with actual random strings before deploying.**

---

## Step 4 — Deploy

```bash
chmod +x deploy.sh
./deploy.sh
```

The script:
1. Validates Docker installation and port availability
2. Runs `docker compose up -d`
3. Waits for health checks (PostgreSQL + Redis)
4. Prints the Admin Panel URL

After the first boot, check the API logs for your auto-generated API key:

```bash
docker compose logs api | grep "API_KEY:"
```

> ⚠️ This key is shown **only once**. Save it immediately.

---

## Step 5 — DNS Setup

For each domain you want to receive mail on:

```
MX   yourdomain.com      →  your-vps-ip   (priority 10)
A    mail.yourdomain.com →  your-vps-ip
```

Optionally add SPF/DKIM records for better deliverability (not required for receiving).

---

## Step 6 — Add Domains in Admin Panel

1. Open `https://your-vps-ip:4000/<ADMIN_PANEL_PATH>/`
2. Login: username = `ADMIN_USERNAME`, password = `ADMIN_API_KEY`
3. Go to **Domains** tab → **Add Domain**
4. Enter your domain name → Save
5. Click **Check DNS** to verify MX records propagated

---

## Updating TempMail

```bash
cd /opt/mailserver
git pull
docker compose up -d --build
```

---

## Adding a Secondary SMTP Node

To run a redundant SMTP receiver on a second VPS:

```bash
# On the NEW VPS (already has Docker + same codebase cloned)
chmod +x add-node.sh
./add-node.sh
```

You will be asked for:

| Prompt | Value |
|--------|-------|
| Redis URL | Copy from primary `.env` → replace `redis` hostname with primary VPS IP |
| Internal API URL | `http://<primary-ip>:4000/internal/mail/ingest` |
| Rspamd URL | `http://<primary-ip>:11333` (optional — leave empty to skip) |
| Mail domain | e.g. `mail2.yourdomain.com` |

Then add a secondary MX record:
```
MX   yourdomain.com  →  secondary-vps-ip  (priority 20)
```

Primary stays at priority 10. Mail falls back to secondary automatically if primary is down.

Allow the secondary node to reach the primary:
```bash
# Run these on the PRIMARY server:
sudo ufw allow from <secondary-vps-ip> to any port 6379   # Redis
sudo ufw allow from <secondary-vps-ip> to any port 4000   # API (for future use)
```

---

## Rotating the Admin Password

```bash
# Edit .env — change ADMIN_API_KEY
nano .env
docker compose restart api
```

---

## Troubleshooting

### Port 25 blocked by cloud provider?

Many VPS providers (GCP, AWS Lightsail, Azure) block port 25 by default. Options:
- Request port 25 unblock from your provider
- Use a separate SMTP relay service for outbound only (TempMail is inbound-only)
- Consider providers that allow port 25 (Vultr, Hetzner, OVH)

### No mail arriving?

1. Check DNS: `dig MX yourdomain.com`
2. Test SMTP: `telnet your-vps-ip 25`
3. Check mail-edge logs: `docker compose logs mail-edge`
4. Check worker logs: `docker compose logs worker`
5. Verify Rspamd spam score isn't rejecting: check `spam_reject_threshold` in Settings

### Admin panel not loading?

1. Check `ADMIN_PANEL_PATH` is set in `.env`
2. Verify API is running: `docker compose ps`
3. Check: `curl http://localhost:4000/health`

### Database migration failed?

```bash
docker compose logs api | grep -i "migrate\|fatal"
```

GORM AutoMigrate runs on every startup — it is additive only (no destructive changes). Safe to re-run.

---

## Backup

```bash
# Backup PostgreSQL
docker compose exec postgres pg_dump -U tempmail tempmail_db > backup-$(date +%Y%m%d).sql

# Restore
cat backup-20260313.sql | docker compose exec -T postgres psql -U tempmail -d tempmail_db
```

Cloudflare R2 handles attachment storage — configure R2's built-in lifecycle rules for retention.
