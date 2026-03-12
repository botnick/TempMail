# tempmail-mailserver

Backend service สำหรับรับอีเมลชั่วคราว ทำงานเป็น standalone SMTP server ที่รับเมลจริงจากอินเทอร์เน็ต กรองสแปมผ่าน Rspamd แล้วเก็บไว้ให้เว็บหลักดึงผ่าน REST API

ใช้งานง่าย — เว็บหลักแค่ต่อ 2 ค่า:

```
TEMPMAIL_API_URL=https://api.yourdomain.com
TEMPMAIL_API_KEY=your_key
```

## สิ่งที่ต้องมี

- VPS ที่เปิด port 25 ได้ (DigitalOcean, Vultr, Hetzner — GCP/Azure บล็อก port 25)
- โดเมน + MX record ชี้มาที่เซิร์ฟเวอร์
- Cloudflare R2 bucket สำหรับเก็บไฟล์แนบ
- Docker + Docker Compose

## จุดเด่นของระบบติดตั้ง (Universal Deploy)

- **รองรับทุก OS:** Ubuntu, Debian, CentOS, RHEL, Fedora, Rocky, AlmaLinux, Arch, Alpine
- **ติดตั้งอัตโนมัติ:** เช็คและติดตั้ง Docker + Docker Compose ให้เองถ้ายังไม่มี
- **Dockge Integration:** มีระบบถามเพื่อติดตั้ง Dockge (Docker UI Manager) ให้อัตโนมัติ เพื่อให้บริหารจัดการ Container หน้าเว็บได้สะดวกที่สุด
- **ปลอดภัย:** สร้างรหัสผ่านและ Token ให้แบบสุ่มทั้งหมด

## ติดตั้ง (One-Click Deploy)

```bash
git clone https://github.com/botnick/TempMail /opt/mailserver && cd /opt/mailserver
chmod +x deploy.sh && ./deploy.sh
```

`deploy.sh` จะจัดการทุกอย่างให้ (เช็ค OS, ลง Docker, ลง Dockge, สร้าง `.env`, Build, และ Deploy) ใช้เวลาประมาณ 5–10 นาที

ตรวจสอบว่าทำงาน:

```bash
curl localhost:4000/health          # {"status":"ok"}
telnet mail.yourdomain.com 25       # 220 ESMTP
```

## โครงสร้าง

```
api/             REST API (Go Fiber) + Admin UI
mail-edge/       SMTP server (Go) รับเมลจากอินเทอร์เน็ต
worker/          Background jobs — ลบเมลหมดอายุ
shared/          packages ที่ใช้ร่วมกัน (db, models, logger, namegen)
docker/          Dockerfiles
```

## API

ทุก request ต้องมี header `X-API-Key`

```
POST   /v1/mailbox/create            สร้างกล่องจดหมาย
GET    /v1/mailbox/:id/messages      ดึงรายการเมล
GET    /v1/message/:id               อ่านเมลฉบับเต็ม + ไฟล์แนบ
DELETE /v1/mailbox/:id               ลบกล่อง
GET    /v1/domains                   ดึงรายการโดเมน
```

ตัวอย่าง:

```js
const res = await fetch(`${API_URL}/v1/mailbox/create`, {
  method: 'POST',
  headers: { 'X-API-Key': API_KEY, 'Content-Type': 'application/json' },
  body: JSON.stringify({ tenantId: 'user_123', ttlHours: 24 })
});
const { id, address } = await res.json();
// { id: "mb_...", address: "ploy_narak42@tempmail.io" }
```

ถ้าไม่ส่ง `localPart` ระบบจะสุ่มชื่อให้อัตโนมัติ — ออกมาเป็นชื่อเหมือนคนจริง เช่น `sarah.miller92`, `toon_zaa`, `mint_narak`, `arm_bkk`

## เอกสาร

- **[API_INTEGRATION.md](API_INTEGRATION.md)** — สำหรับทีม backend เว็บหลักที่จะมาต่อ API มีตัวอย่างโค้ดครบ
- **[INSTALL_GUIDE.md](INSTALL_GUIDE.md)** — คู่มือติดตั้ง EN ครบ 3 เคส (ติดตั้ง / เพิ่ม node / ลด node)
- **[INSTALL_GUIDE_TH.html](INSTALL_GUIDE_TH.html)** — คู่มือภาษาไทย + ตัวช่วยสร้าง .env
- **[.env.example](.env.example)** — ตัวแปรทั้งหมดพร้อมคำอธิบาย

## Scripts

```bash
./deploy.sh           # ติดตั้งครั้งแรก (primary server)
./add-node.sh         # เพิ่ม mail-edge node (secondary server)
./remove-node.sh      # ถอด node ออก (zero downtime)
```

ทุกสคริปต์รองรับ:
- `--help` แสดงวิธีใช้
- `--version` แสดงเวอร์ชัน
- `remove-node.sh --force` ข้ามทุก confirmation prompt

ไฟล์ `lib.sh` เป็น shared library ที่ทุกสคริปต์ใช้ร่วมกัน (สี, logging, Docker install, OS detect)

## Multi-node

เมื่อ traffic เยอะ เพิ่ม node ใหม่ที่รันแค่ `mail-edge` ต่อกลับมาที่ API + DB ของเครื่องหลัก
DNS MX priority จัดการ failover ให้อัตโนมัติ ดูรายละเอียดใน [INSTALL_GUIDE.md](INSTALL_GUIDE.md)

## ตัวแปรสำคัญ

| ตัวแปร | ค่าเริ่มต้น | |
|--------|-----------|---|
| `EXTERNAL_API_KEY` | สุ่มตอน deploy | เว็บหลักใช้เรียก API |
| `ADMIN_API_KEY` | สุ่มตอน deploy | เข้า Admin Panel |
| `SPAM_REJECT_THRESHOLD` | 15 | คะแนนสแปมที่จะ reject ตรง SMTP |
| `MAX_ATTACHMENTS` | 10 | จำนวนไฟล์แนบสูงสุดต่อเมล |
| `MAX_ATTACHMENT_SIZE_MB` | 10 | ขนาดไฟล์แนบสูงสุด |
| `FRONTEND_URL` | *(ว่าง)* | ใส่เฉพาะถ้า browser เรียก API ตรง |

## License

Private
