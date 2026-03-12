# TempMail — SDK Integration Guide

> For developers integrating TempMail into their frontend or backend application.

---

## Base URL

```
https://your-server-ip:4000
```

## Authentication

All `/v1/*` endpoints require an API key. Get one from **Admin Panel → API Keys**.

```http
X-API-Key: tm_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

or as Bearer token:

```http
Authorization: Bearer tm_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

> ⚠️ The raw key is shown **only once** at creation. Store it in your environment variables.

---

## Mailboxes

### Create a Mailbox

```http
POST /v1/mailbox/create
Content-Type: application/json
X-API-Key: <key>

{
  "localPart": "john",           // optional — random if omitted
  "domainName": "example.com",   // must be a registered domain
  "ttlHours": 24                 // optional — default from settings
}
```

**Response `201`**

```json
{
  "id": "3fa85f64-5717-4562-b3fc-2c963f66afa6",
  "localPart": "john",
  "domainId": "...",
  "tenantId": "...",
  "status": "ACTIVE",
  "expiresAt": "2026-03-14T00:00:00Z",
  "createdAt": "2026-03-13T00:00:00Z",
  "domain": {
    "domainName": "example.com"
  }
}
```

---

### Get Mailbox

```http
GET /v1/mailbox/:id
X-API-Key: <key>
```

---

### Get Mailbox Count

```http
GET /v1/mailbox/count
X-API-Key: <key>
```

**Response `200`**
```json
{ "count": 142 }
```

---

### Update Mailbox (extend TTL, change status)

```http
PATCH /v1/mailbox/:id
Content-Type: application/json
X-API-Key: <key>

{
  "ttlHours": 48,
  "status": "ACTIVE"
}
```

---

### Delete Mailbox

```http
DELETE /v1/mailbox/:id
X-API-Key: <key>
```

---

## Messages

### List Messages for a Mailbox

```http
GET /v1/mailbox/:id/messages
X-API-Key: <key>
```

**Query params** (all optional)

| Param | Default | Description |
|-------|---------|-------------|
| `page` | `1` | Page number |
| `limit` | `50` | Results per page (max 100) |
| `sort` | `desc` | `asc` or `desc` by receivedAt |

**Response `200`**
```json
{
  "messages": [
    {
      "id": "...",
      "mailboxId": "...",
      "fromAddress": "sender@gmail.com",
      "toAddress": "john@example.com",
      "subject": "Hello",
      "textBody": "...",
      "htmlBody": "<p>...</p>",
      "spamScore": 0.2,
      "quarantineAction": "ACCEPT",
      "expiresAt": "2026-03-14T00:00:00Z",
      "receivedAt": "2026-03-13T10:00:00Z",
      "attachments": [
        {
          "id": "...",
          "messageId": "...",
          "filename": "invoice.pdf",
          "contentType": "application/pdf",
          "sizeBytes": 102400,
          "s3Key": "attachments/..."
        }
      ]
    }
  ],
  "total": 5,
  "page": 1,
  "limit": 50
}
```

---

### Get Single Message

```http
GET /v1/message/:id
X-API-Key: <key>
```

Returns the same structure as a single message object above.

---

### Delete Message

```http
DELETE /v1/message/:id
X-API-Key: <key>
```

---

## Attachments

### Download / View Attachment

```http
GET /v1/attachment/:id
X-API-Key: <key>
```

Proxies the file from Cloudflare R2 and responds with the original `Content-Type`.  
Use this URL directly in `<img src="...">`, `<a href="...">`, or fetch.

> The admin panel uses `/admin/attachment/:id` (session token) instead of the `/v1/` path.

---

## Domains

### List Active Domains

```http
GET /v1/domains
X-API-Key: <key>
```

**Response `200`**
```json
[
  {
    "id": "...",
    "domainName": "example.com",
    "status": "ACTIVE"
  }
]
```

Use this to let users pick which domain to create their mailbox on.

---

## Health Check

```http
GET /health
```

No auth required.

**Response `200`**
```json
{ "status": "ok" }
```

---

## Error Responses

All errors return JSON:

```json
{
  "error": "human-readable message"
}
```

| HTTP Status | Meaning |
|-------------|---------|
| `400` | Bad request / missing fields |
| `401` | Missing or invalid API key |
| `404` | Resource not found |
| `409` | Conflict (e.g. duplicate mailbox) |
| `429` | Rate limit exceeded |
| `500` | Internal server error |

---

## Webhook (Push Notifications)

Configure in Admin Panel → **Settings** → Webhook URL.

When a new email arrives, TempMail POSTs to your URL:

```http
POST https://yourapp.com/webhook
Content-Type: application/json
X-TempMail-Signature: sha256=<hmac>

{
  "event": "mail.received",
  "mailboxId": "...",
  "messageId": "...",
  "receivedAt": "2026-03-13T10:00:00Z"
}
```

### Verify Signature

```javascript
const crypto = require('crypto')

function verify(secret, body, signature) {
  const expected = 'sha256=' + crypto
    .createHmac('sha256', secret)
    .update(body)
    .digest('hex')
  return crypto.timingSafeEqual(
    Buffer.from(expected),
    Buffer.from(signature)
  )
}
```

---

## JavaScript Example

```javascript
const BASE = 'https://your-server:4000'
const KEY  = process.env.TEMPMAIL_API_KEY

async function createMailbox(domain) {
  const res = await fetch(`${BASE}/v1/mailbox/create`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-API-Key': KEY },
    body: JSON.stringify({ domainName: domain, ttlHours: 24 })
  })
  return res.json()
}

async function waitForMail(mailboxId, pollMs = 3000) {
  while (true) {
    const res = await fetch(`${BASE}/v1/mailbox/${mailboxId}/messages`, {
      headers: { 'X-API-Key': KEY }
    })
    const data = await res.json()
    if (data.messages.length > 0) return data.messages[0]
    await new Promise(r => setTimeout(r, pollMs))
  }
}

// Usage
const box = await createMailbox('example.com')
console.log('Email address:', box.localPart + '@example.com')
const msg = await waitForMail(box.id)
console.log('Subject:', msg.subject)
```

---

## Python Example

```python
import os, time, requests

BASE = 'https://your-server:4000'
HEADERS = {'X-API-Key': os.environ['TEMPMAIL_API_KEY']}

def create_mailbox(domain, ttl_hours=24):
    r = requests.post(f'{BASE}/v1/mailbox/create',
        json={'domainName': domain, 'ttlHours': ttl_hours},
        headers=HEADERS)
    r.raise_for_status()
    return r.json()

def wait_for_mail(mailbox_id, timeout=60, poll=3):
    deadline = time.time() + timeout
    while time.time() < deadline:
        r = requests.get(f'{BASE}/v1/mailbox/{mailbox_id}/messages',
            headers=HEADERS)
        msgs = r.json().get('messages', [])
        if msgs:
            return msgs[0]
        time.sleep(poll)
    raise TimeoutError('No mail received')

box = create_mailbox('example.com')
print('Address:', f"{box['localPart']}@example.com")
msg = wait_for_mail(box['id'])
print('Subject:', msg['subject'])
```

---

## Rate Limits

| Route group | Limit | Scope |
|-------------|-------|-------|
| Public (`/health`) | 60 req/min | Per IP |
| SDK (`/v1/*`) | Per API key setting (default 100 req/min) | Per key |
| Admin login | 10 req/min | Per IP |

Rate limit exceeded returns `429` with:
```json
{ "error": "Rate limit exceeded. Try again later." }
```
