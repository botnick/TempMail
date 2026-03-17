# TempMail API Integration Documentation

> **Generated from source code** — `api/main.go`, `api/handlers/sdk.go`, `api/handlers/ingest.go`, `api/handlers/admin.go`
>
> **Base URL:** `http://your-server:3000`

---

## Table of Contents

- [Authentication](#authentication)
- [Error Format](#error-format)
- [Rate Limiting](#rate-limiting)
- [Public Endpoints](#1-public-endpoints)
  - [GET /health](#get-health)
  - [GET /api/server-info](#get-apiserverinfo)
- [SDK Endpoints (v1)](#2-sdk-endpoints-v1)
  - [POST /v1/mailbox/create](#post-v1mailboxcreate)
  - [GET /v1/mailbox/:id](#get-v1mailboxid)
  - [GET /v1/mailbox/:id/messages](#get-v1mailboxidmessages)
  - [PATCH /v1/mailbox/:id](#patch-v1mailboxid)
  - [DELETE /v1/mailbox/:id](#delete-v1mailboxid)
  - [GET /v1/mailbox/count](#get-v1mailboxcount)
  - [GET /v1/message/:id](#get-v1messageid)
  - [DELETE /v1/message/:id](#delete-v1messageid)
  - [GET /v1/attachment/:id](#get-v1attachmentid)
  - [GET /v1/domains](#get-v1domains)
- [Internal Endpoints](#3-internal-endpoints)
  - [POST /internal/mail/ingest](#post-internalmailingest)
- [Admin Endpoints](#4-admin-endpoints)
  - [POST /admin/login](#post-adminlogin)
  - [GET /admin/dashboard](#get-admindashboard)
  - [GET /admin/metrics](#get-adminmetrics)
  - [Domain Management](#domain-management)
  - [Node Management](#node-management)
  - [Filter Management](#filter-management)
  - [Mailbox Management (Admin)](#mailbox-management-admin)
  - [Message Management (Admin)](#message-management-admin)
  - [Settings](#settings)
  - [API Key Management](#api-key-management)
  - [Export / Import](#export--import)
  - [Audit Log](#audit-log)
  - [Webhook Test](#webhook-test)
  - [SSE Events](#sse-events)

---

## Authentication

### SDK / Internal Endpoints (`/v1/*`, `/internal/*`)

Send API key via **one** of these methods:

| Method | Header | Example |
|--------|--------|---------|
| Header | `X-API-Key` | `X-API-Key: your-api-key` |
| Bearer | `Authorization` | `Authorization: Bearer your-api-key` |

### Admin Endpoints (`/admin/*`)

1. **Login** (`POST /admin/login`) — no auth required
2. **All other admin routes** — Bearer session token from login:

```
Authorization: Bearer <session-token>
```

Session tokens expire after **24 hours**.

---

## Error Format

The API uses two error response formats:

### Standard format (most SDK routes)

```json
{
  "error": "Human readable error message"
}
```

### Structured format (admin routes + ingest)

```json
{
  "error": {
    "code": "error_code",
    "message": "Human readable error message"
  }
}
```

---

## Rate Limiting

| Scope | Default | Window |
|-------|---------|--------|
| Public routes (`/health`) | `PUBLIC_RATE_LIMIT_PER_MIN` (default 60) | 1 minute per IP |
| Login (`/admin/login`) | `LOGIN_RATE_LIMIT_PER_MIN` (default 5) | 1 minute per IP |

**Rate limit exceeded response:**

```
HTTP/1.1 429 Too Many Requests
```
```json
{
  "error": "Rate limit exceeded. Try again later."
}
```

---

## 1. Public Endpoints

### GET /health

Health check — no authentication required.

**Request:**
```
GET /health
```

**Response — `200 OK`:**
```json
{
  "status": "ok"
}
```

| Status | Meaning |
|--------|---------|
| `200 OK` | Server is healthy |
| `429 Too Many Requests` | Rate limit exceeded |

---

### GET /api/server-info

Get server node information and DNS setup instructions for domain configuration. No authentication required.

Use this endpoint from your Web App to display dynamic DNS instructions to users when they add a new domain.

**Request:**
```
GET /api/server-info
```

**Response — `200 OK`:**
```json
{
  "hostname": "mx1.tempmail.dev",
  "ip": "68.183.184.209",
  "smtp_port": 25,
  "dns_records": {
    "mx": [
      {
        "type": "MX",
        "name": "@",
        "value": "mx1.tempmail.dev",
        "priority": 10
      },
      {
        "type": "MX",
        "name": "@",
        "value": "mx2.tempmail.dev",
        "priority": 20
      }
    ],
    "spf": {
      "type": "TXT",
      "name": "@",
      "value": "v=spf1 ip4:68.183.184.209 ip4:134.209.100.50 ~all"
    },
    "dmarc": {
      "type": "TXT",
      "name": "_dmarc",
      "value": "v=DMARC1; p=none;"
    }
  },
  "nodes": [
    {
      "id": "uuid",
      "name": "primary",
      "hostname": "mx1.tempmail.dev",
      "ip": "68.183.184.209",
      "region": "sgp1",
      "active": true
    }
  ]
}
```

**Response Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `hostname` | string | Primary node hostname (falls back to IP if not set) |
| `ip` | string | Primary node IP address |
| `smtp_port` | integer | SMTP port (configurable via `smtp_port` setting, default: `25`) |
| `dns_records.mx` | array | MX records — one per active node with increasing priority (10, 20, ...) |
| `dns_records.spf` | object | SPF TXT record — includes all node IPs, qualifier configurable via `spf_qualifier` setting |
| `dns_records.dmarc` | object | DMARC TXT record — policy configurable via `dmarc_policy` setting |
| `nodes` | array | All active server nodes with id, name, hostname, ip, region, active status |

**Response — `200 OK` (no nodes configured):**
```json
{
  "hostname": "",
  "ip": "",
  "smtp_port": 25,
  "dns_records": {},
  "nodes": []
}
```

**Configurable Settings** (via Admin Panel → Settings):

| Setting Key | Default | Description |
|-------------|---------|-------------|
| `smtp_port` | `25` | SMTP port shown in DNS instructions |
| `dmarc_policy` | `none` | DMARC policy: `none`, `quarantine`, `reject` |
| `spf_qualifier` | `~all` | SPF qualifier: `~all`, `-all`, `+all`, `?all` |

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Server info returned |
| `429 Too Many Requests` | — | Rate limit exceeded |
| `500 Internal Server Error` | `database_error` | `Failed to load server info` |

**cURL Example:**
```bash
curl http://your-server:4000/api/server-info
```

**JavaScript Example:**
```javascript
const response = await fetch('http://your-server:4000/api/server-info');
const serverInfo = await response.json();

// Display DNS instructions to user
console.log(`MX Record: Point to ${serverInfo.hostname}`);
console.log(`Server IP: ${serverInfo.ip}`);
console.log(`SPF: ${serverInfo.dns_records.spf.value}`);
console.log(`DMARC: ${serverInfo.dns_records.dmarc.value}`);
```

---

## 2. SDK Endpoints (v1)

> **Authentication required:** `X-API-Key` or `Authorization: Bearer <key>`

### POST /v1/mailbox/create

Create a new temporary mailbox.

**Request Body:**
```json
{
  "localPart": "john.doe",
  "domainId": "uuid-of-domain",
  "tenantId": "user-123",
  "ttlHours": 24
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `localPart` | string | No | Desired mailbox name (a-z, 0-9, `.`, `-`, `_`). Auto-generated if omitted. |
| `domainId` | string | No | UUID of domain to use. Falls back to first public domain. |
| `tenantId` | string | No | Your user ID. Defaults to `"anonymous"`. |
| `ttlHours` | integer | No | Mailbox lifetime in hours. Default: `24`. |

**Response — `201 Created`:**
```json
{
  "id": "uuid",
  "address": "john.doe@example.com",
  "localPart": "john.doe",
  "domain": "example.com",
  "domainId": "uuid",
  "expiresAt": "2026-03-15T01:53:51Z",
  "status": "ACTIVE"
}
```

| Status | Code | Message |
|--------|------|---------|
| `201 Created` | — | Mailbox created successfully |
| `400 Bad Request` | — | `Invalid request body` |
| `400 Bad Request` | — | `Domain not found or inactive` |
| `400 Bad Request` | — | `No public domain available` |
| `400 Bad Request` | — | `Invalid mailbox name. Use only a-z, 0-9, dots, dashes, underscores.` |
| `400 Bad Request` | — | `Mailbox name must be 1-64 characters` |
| `401 Unauthorized` | — | `API key required` / `Invalid API key` |
| `409 Conflict` | — | `Mailbox name already taken` |
| `500 Internal Server Error` | — | `Failed to create mailbox` |

---

### GET /v1/mailbox/:id

Get mailbox details including message count.

**Request:**
```
GET /v1/mailbox/uuid-of-mailbox
```

**Response — `200 OK`:**
```json
{
  "id": "uuid",
  "address": "john.doe@example.com",
  "localPart": "john.doe",
  "domain": "example.com",
  "domainId": "uuid",
  "tenantId": "user-123",
  "status": "ACTIVE",
  "expiresAt": "2026-03-15T01:53:51Z",
  "createdAt": "2026-03-14T01:53:51Z",
  "messageCount": 5
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Mailbox found |
| `401 Unauthorized` | `API key required` / `Invalid API key` |
| `404 Not Found` | `Mailbox not found` |

---

### GET /v1/mailbox/:id/messages

List all messages in a mailbox (summary view, max 100).

**Request:**
```
GET /v1/mailbox/uuid-of-mailbox/messages
```

**Response — `200 OK`:**
```json
{
  "mailboxId": "uuid",
  "count": 2,
  "messages": [
    {
      "id": "uuid",
      "from": "sender@gmail.com",
      "subject": "Hello World",
      "spamScore": 0.5,
      "isSpam": false,
      "quarantineAction": "ACCEPT",
      "hasHtml": true,
      "receivedAt": "2026-03-14T02:00:00Z",
      "expiresAt": "2026-03-15T02:00:00Z"
    }
  ]
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Messages listed (empty array if none) |
| `401 Unauthorized` | `API key required` / `Invalid API key` |
| `404 Not Found` | `Mailbox not found` |

---

### PATCH /v1/mailbox/:id

Extend TTL or change mailbox status.

**Request Body:**
```json
{
  "ttlHours": 48,
  "status": "PAUSED"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `ttlHours` | integer | No | New TTL from now. Re-activates expired mailbox. |
| `status` | string | No | `ACTIVE` or `PAUSED` |

**Response — `200 OK`:**
```json
{
  "id": "uuid",
  "address": "john.doe@example.com",
  "status": "PAUSED",
  "expiresAt": "2026-03-16T01:53:51Z"
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Mailbox updated |
| `400 Bad Request` | `Invalid request body` |
| `401 Unauthorized` | `API key required` / `Invalid API key` |
| `404 Not Found` | `Mailbox not found` |
| `410 Gone` | `Mailbox has been deleted` |

---

### DELETE /v1/mailbox/:id

Soft-delete a mailbox (sets status to `DELETED`).

**Request:**
```
DELETE /v1/mailbox/uuid-of-mailbox
```

**Response — `200 OK`:**
```json
{
  "status": "deleted",
  "id": "uuid"
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Mailbox deleted |
| `401 Unauthorized` | `API key required` / `Invalid API key` |
| `404 Not Found` | `Mailbox not found` |

---

### GET /v1/mailbox/count

Count mailboxes, optionally filtered by tenant.

**Query Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `tenantId` | string | No | Filter by tenant. All tenants if omitted. |

**Request:**
```
GET /v1/mailbox/count?tenantId=user-123
```

**Response — `200 OK`:**
```json
{
  "total": 10,
  "active": 8,
  "expired": 2
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Counts returned |
| `401 Unauthorized` | `API key required` / `Invalid API key` |

---

### GET /v1/message/:id

Get full message content including HTML body and attachments.

**Request:**
```
GET /v1/message/uuid-of-message
```

**Response — `200 OK`:**
```json
{
  "id": "uuid",
  "mailboxId": "uuid",
  "from": "sender@gmail.com",
  "to": "john.doe@example.com",
  "subject": "Hello World",
  "textBody": "Plain text content...",
  "htmlBody": "<p>Sanitized HTML content</p>",
  "spamScore": 0.5,
  "quarantineAction": "ACCEPT",
  "attachments": [
    {
      "id": "uuid",
      "filename": "document.pdf",
      "contentType": "application/pdf",
      "sizeBytes": 102400
    }
  ],
  "receivedAt": "2026-03-14T02:00:00Z",
  "expiresAt": "2026-03-15T02:00:00Z"
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Full message returned |
| `401 Unauthorized` | `API key required` / `Invalid API key` |
| `404 Not Found` | `Message not found` |

---

### DELETE /v1/message/:id

Delete a single message and its attachments.

**Request:**
```
DELETE /v1/message/uuid-of-message
```

**Response — `200 OK`:**
```json
{
  "status": "deleted",
  "id": "uuid"
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Message deleted |
| `401 Unauthorized` | `API key required` / `Invalid API key` |
| `404 Not Found` | `Message not found` |

---

### GET /v1/attachment/:id

Get a presigned download URL for an attachment (valid 15 minutes).

**Request:**
```
GET /v1/attachment/uuid-of-attachment
```

**Response — `200 OK`:**
```json
{
  "id": "uuid",
  "filename": "document.pdf",
  "contentType": "application/pdf",
  "sizeBytes": 102400,
  "downloadUrl": "https://r2.cloudflarestorage.com/...",
  "expiresIn": 900
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Download URL generated |
| `401 Unauthorized` | `API key required` / `Invalid API key` |
| `404 Not Found` | `Attachment not found` / `Attachment file not available` |
| `500 Internal Server Error` | `Failed to generate download link` |
| `503 Service Unavailable` | `Object storage not configured` |

---

### GET /v1/domains

List all active domains available for mailbox creation.

**Request:**
```
GET /v1/domains
```

**Response — `200 OK`:**
```json
{
  "count": 2,
  "domains": [
    {
      "id": "uuid",
      "domainName": "example.com",
      "isPublic": true
    },
    {
      "id": "uuid",
      "domainName": "private.org",
      "isPublic": false
    }
  ]
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Domains listed |
| `401 Unauthorized` | `API key required` / `Invalid API key` |

---

## 3. Internal Endpoints

> **Authentication:** `Authorization: Bearer <API_TOKEN>` (internal key)

### POST /internal/mail/ingest

Process incoming email from mail-edge server. Sent as `multipart/form-data`.

**Form Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `from` | string | Yes | Sender email address |
| `to` | string | Yes | Recipient address (`local@domain`) |
| `quarantineAction` | string | No | `ACCEPT`, `QUARANTINE`, `REJECT`. Default: `ACCEPT` |
| `spamScore` | float | No | Rspamd spam score |
| `rawEmail` | file | Yes | Raw RFC822 email file (.eml) |

**Response — `201 Created`:**
```json
{
  "id": "uuid",
  "status": "ingested",
  "r2_key": "mail/example.com/john/uuid.eml",
  "attachments": 2
}
```

| Status | Code | Message |
|--------|------|---------|
| `201 Created` | — | Email ingested successfully |
| `400 Bad Request` | `missing_raw_email` | `Missing rawEmail file` |
| `400 Bad Request` | `read_error` | `Cannot read rawEmail` / `Failed to read email data` |
| `400 Bad Request` | `invalid_recipient` | `Invalid recipient` |
| `401 Unauthorized` | — | `API key required` / `Invalid API key` |
| `404 Not Found` | `domain_not_found` | `Domain not found` |
| `404 Not Found` | `mailbox_not_found` | `Mailbox not found` |
| `500 Internal Server Error` | `database_error` | `Database error` |

---

## 4. Admin Endpoints

> **Authentication:** `Authorization: Bearer <session-token>` (from login)
>
> Except `POST /admin/login` which is **public** (with login rate limit).

### POST /admin/login

Authenticate with admin credentials to get a session token.

**Request Body:**
```json
{
  "username": "admin",
  "password": "your-admin-api-key"
}
```

**Response — `200 OK`:**
```json
{
  "token": "base64payload.hmac-signature",
  "username": "admin",
  "expiresIn": 86400
}
```

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Login successful |
| `400 Bad Request` | — | `Invalid request` |
| `401 Unauthorized` | `unauthorized` | `Invalid credentials` |
| `429 Too Many Requests` | — | `Too many login attempts. Try again later.` |
| `500 Internal Server Error` | `server_error` | `Admin not configured` / `Token generation failed` |

---

### GET /admin/dashboard

System overview with service health checks and runtime info.

**Response — `200 OK`:**
```json
{
  "totalDomains": 3,
  "totalMailboxes": 150,
  "totalMessages": 1200,
  "totalSpamBlocked": 45,
  "messagesToday": 28,
  "redisActiveMailboxes": 148,
  "services": {
    "database": { "status": "ONLINE", "latency": "1.2ms", "detail": "open:5 idle:3 inuse:2 max:25" },
    "redis": { "status": "ONLINE", "latency": "0.5ms", "detail": "conns:10 idle:8 hits:5000 misses:2" },
    "rspamd": { "status": "ONLINE", "latency": "3ms", "detail": "spam filter" },
    "worker": { "status": "ONLINE", "detail": "background" },
    "mailserver": { "status": "ONLINE", "detail": "this instance" }
  },
  "runtime": {
    "goroutines": 12,
    "goVersion": "go1.23.0",
    "os": "linux",
    "arch": "amd64",
    "cpus": 4,
    "allocMB": "15.2",
    "sysMB": "28.5",
    "gcCycles": 42,
    "uptimeStr": "2d 5h 30m",
    "uptimeSec": 190200
  },
  "serverTime": "2026-03-14T01:53:51Z"
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Dashboard data returned |
| `401 Unauthorized` | `Invalid or expired session` |
| `500 Internal Server Error` | `Admin panel not configured` |

---

### GET /admin/metrics

System throughput, storage, mailbox, spam, and Redis metrics.

**Response — `200 OK`:**
```json
{
  "throughput": { "lastHour": 15, "last24h": 320 },
  "storage": { "totalMessages": 1200, "totalAttachments": 450 },
  "mailboxes": { "active": 148, "expiredPending": 5 },
  "spam": { "blockedMessages": 45, "blocklistRules": 12, "allowlistRules": 3 },
  "redis": { "dbSize": 250, "usedMemory": "5.2M" }
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Metrics returned |
| `401 Unauthorized` | `Invalid or expired session` |

---

### Domain Management

#### GET /admin/domains

List all domains with pagination and search.

**Query Parameters:**

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `search` | string | — | Search by domain name, node name, or node IP |
| `status` | string | — | Filter by status (`ACTIVE`, `PENDING`, `DISABLED`, `DELETED`) |
| `limit` | integer | `50` | Max results |
| `offset` | integer | `0` | Pagination offset |

**Response — `200 OK`:**
```json
{
  "domains": [
    {
      "id": "uuid",
      "domainName": "example.com",
      "status": "ACTIVE",
      "nodeId": "uuid",
      "node": { "id": "uuid", "name": "primary", "ipAddress": "1.2.3.4" },
      "createdAt": "2026-03-14T00:00:00Z"
    }
  ],
  "count": 3
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Domains listed |
| `401 Unauthorized` | `Invalid or expired session` |

---

#### POST /admin/domains

Create a new domain. If a previously deleted domain with the same name exists, it will be reactivated.

**Request Body:**
```json
{
  "domainName": "example.com",
  "tenantId": "optional-tenant-uuid",
  "nodeId": "optional-node-uuid"
}
```

**Response — `201 Created`:**
```json
{
  "domain": { "id": "uuid", "domainName": "example.com", "status": "ACTIVE" },
  "dns": [
    { "type": "MX", "name": "example.com", "value": "mail.example.com", "priority": 10 },
    { "type": "A", "name": "mail.example.com", "value": "1.2.3.4" }
  ],
  "nodeIp": "1.2.3.4"
}
```

**Reactivated domain — `201 Created`:**
```json
{
  "domain": { "id": "uuid", "domainName": "example.com", "status": "ACTIVE" },
  "reactivated": true
}
```

| Status | Code | Message |
|--------|------|---------|
| `201 Created` | — | Domain created / reactivated |
| `400 Bad Request` | `invalid_request` | `domainName is required` |
| `400 Bad Request` | `invalid_domain` | `Invalid domain name format` |
| `400 Bad Request` | `invalid_node` | `Node not found` |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `409 Conflict` | `domain_exists` | `Domain already exists` |
| `500 Internal Server Error` | `database_error` | `Database error` |

---

#### PUT /admin/domains/:id

Update domain node assignment or status.

**Request Body:**
```json
{
  "nodeId": "uuid-or-empty-string",
  "status": "ACTIVE"
}
```

| Field | Values |
|-------|--------|
| `status` | `ACTIVE`, `PENDING`, `DISABLED` |
| `nodeId` | UUID to assign, or `""` to unassign |

**Response — `200 OK`:** Full domain object

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Domain updated |
| `400 Bad Request` | `invalid_request` | `Invalid request body` |
| `400 Bad Request` | `invalid_node` | `Node not found` |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `404 Not Found` | `domain_not_found` | `Domain not found` |

---

#### DELETE /admin/domains/:id

Hard-delete a domain and cascade delete all mailboxes, messages, and attachments.

**Response — `200 OK`:**
```json
{
  "status": "deleted",
  "id": "uuid",
  "domain": "example.com"
}
```

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Domain deleted |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `404 Not Found` | `domain_not_found` | `Domain not found` |

---

#### GET /admin/domains/dns-check

Check DNS records for a domain (MX, A, SPF, DMARC).

**Query Parameters:**

| Param | Type | Required | Description |
|-------|------|----------|-------------|
| `domain` | string | Yes | Domain name to check |

**Response — `200 OK`:**
```json
{
  "records": [
    { "type": "MX", "name": "example.com", "value": "mail.example.com (priority 10)", "status": "OK" },
    { "type": "A", "name": "mail.example.com", "value": "1.2.3.4", "status": "OK" },
    { "type": "SPF", "name": "example.com", "value": "v=spf1 ...", "status": "OK" },
    { "type": "DMARC", "name": "_dmarc.example.com", "value": "v=DMARC1 ...", "status": "OK" }
  ],
  "allOk": true,
  "summary": "All critical DNS records are configured correctly."
}
```

| Record Status | Meaning |
|---------------|---------|
| `OK` | Record found and valid |
| `WARN` | Optional record missing (SPF/DMARC) |
| `FAIL` | Critical record missing (MX/A) |

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | DNS check results |
| `400 Bad Request` | `missing_domain` | `Domain parameter is required` |
| `401 Unauthorized` | — | `Invalid or expired session` |

---

### Node Management

#### GET /admin/nodes

List all mail nodes with pagination.

**Query Parameters:** `search`, `status`, `limit` (50), `offset` (0)

**Response — `200 OK`:**
```json
{
  "nodes": [
    {
      "id": "uuid",
      "name": "primary",
      "ipAddress": "1.2.3.4",
      "region": "auto-detected",
      "status": "ACTIVE",
      "domains": []
    }
  ],
  "count": 1
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Nodes listed |
| `401 Unauthorized` | `Invalid or expired session` |

---

#### POST /admin/nodes

Create a new mail node.

**Request Body:**
```json
{
  "name": "us-east-1",
  "ipAddress": "5.6.7.8",
  "region": "US East"
}
```

| Status | Code | Message |
|--------|------|---------|
| `201 Created` | — | Node created |
| `400 Bad Request` | `invalid_request` | `name and ipAddress are required` / `Invalid request body` |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `500 Internal Server Error` | `database_error` | `Database error` |

---

#### PUT /admin/nodes/:id

Update node name, IP, region, or status.

**Request Body:**
```json
{
  "name": "new-name",
  "ipAddress": "9.10.11.12",
  "region": "EU West",
  "status": "DISABLED"
}
```

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Node updated |
| `400 Bad Request` | `invalid_request` | `Invalid request body` |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `404 Not Found` | `node_not_found` | `Node not found` |

---

#### DELETE /admin/nodes/:id

Delete a node. Fails if domains are still assigned.

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | `{"status": "deleted"}` |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `404 Not Found` | `node_not_found` | `Node not found` |
| `409 Conflict` | `node_in_use` | `Cannot delete node: N domain(s) still assigned` |

---

### Filter Management

#### GET /admin/filters

List domain filters (blocklist/allowlist).

**Query Parameters:** `search`, `type` (`BLOCK`/`ALLOW`), `limit` (50), `offset` (0)

**Response — `200 OK`:**
```json
{
  "filters": [
    {
      "id": "uuid",
      "pattern": "spam.com",
      "filterType": "BLOCK",
      "reason": "Known spam domain",
      "createdAt": "2026-03-14T00:00:00Z"
    }
  ],
  "count": 1
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Filters listed |
| `401 Unauthorized` | `Invalid or expired session` |

---

#### POST /admin/filters

Create a new domain filter.

**Request Body:**
```json
{
  "pattern": "spam.com",
  "filterType": "BLOCK",
  "reason": "Known spam domain"
}
```

| Status | Code | Message |
|--------|------|---------|
| `201 Created` | — | Filter created |
| `400 Bad Request` | `invalid_request` | `pattern and filterType (BLOCK/ALLOW) are required` |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `409 Conflict` | `filter_exists` | `Filter pattern already exists` |

---

#### PUT /admin/filters/:id

Update filter pattern, type, or reason.

**Request Body:**
```json
{
  "pattern": "updated-pattern.com",
  "filterType": "ALLOW",
  "reason": "Now allowed"
}
```

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Filter updated |
| `400 Bad Request` | `invalid_request` | `Invalid request body` |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `404 Not Found` | `filter_not_found` | `Filter not found` |

---

#### DELETE /admin/filters/:id

Delete a domain filter.

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | `{"status": "deleted"}` |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `404 Not Found` | `filter_not_found` | `Filter not found` |

---

### Mailbox Management (Admin)

#### GET /admin/mailboxes

List mailboxes with pagination and search.

**Query Parameters:**

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `search` | string | — | Search by local part, domain name, or full address |
| `status` | string | `ACTIVE` | Filter by status |
| `limit` | integer | `50` | Max results |
| `offset` | integer | `0` | Pagination offset |

**Response — `200 OK`:**
```json
{
  "total": 150,
  "mailboxes": [
    {
      "id": "uuid",
      "localPart": "john.doe",
      "domain": { "id": "uuid", "domainName": "example.com" },
      "status": "ACTIVE",
      "expiresAt": "2026-03-15T01:53:51Z"
    }
  ]
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Mailboxes listed |
| `401 Unauthorized` | `Invalid or expired session` |

---

#### POST /admin/mailboxes/quick-create

Quick-create a test mailbox with auto-generated name.

**Request Body:**
```json
{
  "domainId": "optional-uuid",
  "ttlHours": 1
}
```

**Response — `201 Created`:**
```json
{
  "id": "uuid",
  "address": "test-a1b2c3d4@example.com",
  "localPart": "test-a1b2c3d4",
  "domain": "example.com",
  "expiresAt": "2026-03-14T02:53:51Z",
  "ttlHours": 1
}
```

| Status | Code | Message |
|--------|------|---------|
| `201 Created` | — | Mailbox created |
| `400 Bad Request` | `invalid_request` | `Invalid request body` |
| `400 Bad Request` | `domain_not_found` | `Domain not found or inactive` |
| `400 Bad Request` | `no_domain` | `No active domain available` |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `500 Internal Server Error` | `database_error` | `Database error` |

---

#### DELETE /admin/mailboxes/:id

Hard-delete a mailbox with all messages and attachments.

**Response — `200 OK`:**
```json
{
  "status": "deleted",
  "id": "uuid",
  "address": "john.doe@example.com"
}
```

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Mailbox deleted |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `404 Not Found` | `mailbox_not_found` | `Mailbox not found` |

---

### Message Management (Admin)

#### GET /admin/messages

List messages with advanced search and filtering.

**Query Parameters:**

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `search` | string | — | Search in from, to, subject |
| `mailbox_id` | string | — | Filter by mailbox UUID |
| `mailbox_status` | string | — | `ACTIVE`, `EXPIRED`, or `ORPHANED` |
| `limit` | integer | `50` | Max results (hard cap: 200) |
| `offset` | integer | `0` | Pagination offset |

**Response — `200 OK`:**
```json
{
  "total": 1200,
  "messages": [
    {
      "id": "uuid",
      "mailboxId": "uuid",
      "fromAddress": "sender@gmail.com",
      "toAddress": "john@example.com",
      "subject": "Hello",
      "spamScore": 0.5,
      "quarantineAction": "ACCEPT",
      "expiresAt": "2026-03-15T00:00:00Z",
      "receivedAt": "2026-03-14T00:00:00Z",
      "s3KeyRaw": "mail/example.com/john/uuid.eml",
      "mailboxAddress": "john@example.com",
      "mailboxStatus": "ACTIVE"
    }
  ]
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Messages listed |
| `401 Unauthorized` | `Invalid or expired session` |

---

#### GET /admin/messages/:id

Get full message content including body and attachments.

**Response — `200 OK`:** Full message model with attachments array.

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Message returned |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `404 Not Found` | `message_not_found` | `Message not found` |

---

#### DELETE /admin/messages/:id

Hard-delete a single message with attachments.

**Response — `200 OK`:**
```json
{
  "status": "deleted",
  "id": "uuid"
}
```

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Message deleted |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `404 Not Found` | `message_not_found` | `Message not found` |

---

#### POST /admin/messages/bulk-delete

Bulk delete multiple messages (max 100 per request).

**Request Body:**
```json
{
  "ids": ["uuid-1", "uuid-2", "uuid-3"]
}
```

**Response — `200 OK`:**
```json
{
  "status": "deleted",
  "deleted": 3
}
```

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Messages deleted |
| `400 Bad Request` | `invalid_request` | `Invalid request body` |
| `400 Bad Request` | `empty_ids` | `No message IDs provided` |
| `400 Bad Request` | `too_many_ids` | `Maximum 100 IDs per request` |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `500 Internal Server Error` | `database_error` | `Bulk delete failed` |

---

### Settings

#### GET /admin/settings

Get all system settings.

**Response — `200 OK`:**
```json
{
  "settings": {
    "spam_reject_threshold": "15",
    "default_ttl_hours": "24",
    "default_message_ttl_hours": "24",
    "max_message_size_mb": "25",
    "max_mailboxes_free": "5",
    "max_attachments": "10",
    "max_attachment_size_mb": "10",
    "allow_anonymous": "true",
    "webhook_url": "",
    "webhook_secret": ""
  }
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Settings returned |
| `401 Unauthorized` | `Invalid or expired session` |

---

#### POST /admin/settings

Update system settings. Only allowed keys are accepted.

**Allowed Keys:**

| Key | Range | Description |
|-----|-------|-------------|
| `spam_reject_threshold` | 1–100 | Spam score to reject |
| `default_ttl_hours` | 1–8760 | Default mailbox TTL |
| `default_message_ttl_hours` | 1–8760 | Default message TTL |
| `max_message_size_mb` | 1–100 | Max email size |
| `max_mailboxes_free` | 1–10000 | Max free mailboxes |
| `max_attachments` | 1–50 | Max attachments per message |
| `max_attachment_size_mb` | 1–100 | Max single attachment size |
| `allow_anonymous` | — | Allow anonymous mailbox creation |
| `webhook_url` | — | Webhook notification URL |
| `webhook_secret` | — | HMAC secret for webhook |

**Request Body:**
```json
{
  "spam_reject_threshold": "20",
  "webhook_url": "https://your-app.com/webhook"
}
```

**Response — `200 OK`:**
```json
{
  "status": "updated",
  "count": 2
}
```

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Settings updated |
| `400 Bad Request` | `invalid_request` | `Invalid request body` |
| `400 Bad Request` | `invalid_setting` | `Unknown setting key: xxx` |
| `400 Bad Request` | `invalid_value` | `xxx must be a number between Y and Z` |
| `401 Unauthorized` | — | `Invalid or expired session` |

---

### API Key Management

#### GET /admin/api-keys

List all API keys.

**Query Parameters:** `search`, `status`, `limit` (50), `offset` (0)

**Response — `200 OK`:**
```json
{
  "keys": [
    {
      "id": "uuid",
      "name": "default",
      "keyPrefix": "a1b2c3d4",
      "permissions": "read,write",
      "rateLimit": 100,
      "isInternal": false,
      "status": "ACTIVE",
      "createdAt": "2026-03-14T00:00:00Z"
    }
  ],
  "count": 1
}
```

| Status | Message |
|--------|---------|
| `200 OK` | API keys listed |
| `401 Unauthorized` | `Invalid or expired session` |

---

#### POST /admin/api-keys

Create a new API key. The raw key is shown **only once**.

**Request Body:**
```json
{
  "name": "frontend-app",
  "permissions": "read,write",
  "rateLimit": 200,
  "isInternal": false
}
```

**Response — `201 Created`:**
```json
{
  "key": {
    "id": "uuid",
    "name": "frontend-app",
    "keyPrefix": "a1b2c3d4",
    "permissions": "read,write",
    "rateLimit": 200,
    "status": "ACTIVE"
  },
  "rawKey": "full-raw-api-key-uuid-uuid",
  "notice": "Save this key now — it cannot be shown again!"
}
```

| Status | Code | Message |
|--------|------|---------|
| `201 Created` | — | Key created |
| `400 Bad Request` | `invalid_request` | `name is required` / `Invalid request body` |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `500 Internal Server Error` | `database_error` | `Failed to create API key` |

---

#### PUT /admin/api-keys/:id

Update API key metadata.

**Request Body:**
```json
{
  "name": "renamed-key",
  "permissions": "read",
  "rateLimit": 50,
  "status": "REVOKED",
  "isInternal": true
}
```

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Key updated |
| `400 Bad Request` | `invalid_request` | `Invalid request body` |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `404 Not Found` | `key_not_found` | `API key not found` |

---

#### DELETE /admin/api-keys/:id

Delete an API key.

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | `{"status": "deleted"}` |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `404 Not Found` | `key_not_found` | `API key not found` |

---

#### POST /admin/api-keys/:id/roll

Rotate an API key (generates new key, old key stops working immediately).

**Response — `200 OK`:**
```json
{
  "status": "rolled",
  "key": { "id": "uuid", "name": "default", "keyPrefix": "new-pref" },
  "rawKey": "new-full-raw-api-key-uuid-uuid"
}
```

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Key rotated |
| `401 Unauthorized` | — | `Invalid or expired session` |
| `404 Not Found` | `key_not_found` | `API key not found` |
| `500 Internal Server Error` | `database_error` | `Failed to roll API key` |

---

### Export / Import

#### GET /admin/export

Export full system configuration as JSON file download.

**Response — `200 OK`:** (file download)
```json
{
  "exportedAt": "2026-03-14T01:53:51Z",
  "version": "1.0",
  "domains": [],
  "nodes": [],
  "filters": [],
  "settings": {}
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Config exported |
| `401 Unauthorized` | `Invalid or expired session` |

---

#### POST /admin/import

Import system configuration from JSON.

**Request Body:** Same structure as export, but only `settings` and `filters` are imported.

**Limits:** Max 1000 filters, max 50 settings per import.

**Response — `200 OK`:**
```json
{
  "status": "imported",
  "count": 15
}
```

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Config imported |
| `400 Bad Request` | `invalid_json` | `Invalid JSON format` |
| `400 Bad Request` | `too_many_filters` | `Maximum 1000 filters per import` |
| `400 Bad Request` | `too_many_settings` | `Maximum 50 settings per import` |
| `401 Unauthorized` | — | `Invalid or expired session` |

---

### Audit Log

#### GET /admin/audit-log

View audit trail with search and filtering.

**Query Parameters:**

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `search` | string | — | Search across action, target, user, IP |
| `action` | string | — | Filter by action type (e.g., `domain.create`, `mailbox.delete`) |
| `limit` | integer | `100` | Max results |
| `offset` | integer | `0` | Pagination offset |

**Response — `200 OK`:**
```json
{
  "logs": [
    {
      "id": "uuid",
      "action": "domain.create",
      "targetId": "uuid",
      "userId": "",
      "ipAddress": "127.0.0.1",
      "createdAt": "2026-03-14T00:00:00Z"
    }
  ],
  "total": 50,
  "actions": ["domain.create", "domain.delete", "mailbox.delete", "settings.update"]
}
```

| Status | Message |
|--------|---------|
| `200 OK` | Audit log returned |
| `401 Unauthorized` | `Invalid or expired session` |

---

### Webhook Test

#### POST /admin/webhook-test

Send a test payload to the configured webhook URL.

**Response — `200 OK` (success):**
```json
{
  "status": "ok",
  "response": "...",
  "url": "https://your-app.com/webhook"
}
```

**Response — `200 OK` (webhook error):**
```json
{
  "status": "error",
  "error": "webhook returned 500: ...",
  "url": "https://your-app.com/webhook"
}
```

| Status | Code | Message |
|--------|------|---------|
| `200 OK` | — | Test sent (check `status` field) |
| `400 Bad Request` | `no_webhook` | `Webhook URL not configured in Settings` |
| `401 Unauthorized` | — | `Invalid or expired session` |

---

### SSE Events

#### GET /admin/events

Server-Sent Events stream for real-time admin panel updates.

**Authentication:** Pass session token as query parameter (EventSource doesn't support headers).

```
GET /admin/events?token=<session-token>
```

**Response — `200 OK`** (text/event-stream):
```
: connected

event: new_message
data: {}

: keepalive
```

Events are published via Redis pub/sub channel `mail:events`. Keepalive sent every 25 seconds.

| Status | Message |
|--------|---------|
| `200 OK` | SSE stream established |
| `401 Unauthorized` | `Unauthorized` / `Invalid token` |

---

## Webhook Payloads

When `webhook_url` is configured in settings, the system sends a POST request on new messages:

**Headers:**
```
Content-Type: application/json
X-Webhook-Event: message.received
X-Webhook-Signature: sha256=<hmac-hex>  (if webhook_secret is set)
```

**Payload:**
```json
{
  "event": "message.received",
  "mailboxId": "uuid",
  "messageId": "uuid",
  "to": "john@example.com",
  "from": "sender@gmail.com",
  "subject": "Hello World",
  "timestamp": "2026-03-14T01:53:51Z"
}
```

---

## Global HTTP Headers

All responses include these security headers:

| Header | Value |
|--------|-------|
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `X-XSS-Protection` | `1; mode=block` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |
| `Strict-Transport-Security` | `max-age=63072000; includeSubDomains` |
| `X-Request-Id` | Auto-generated UUID per request |

---

## Common Authentication Errors

These errors apply to all authenticated endpoints:

| Status | Condition | Response |
|--------|-----------|----------|
| `401 Unauthorized` | Missing API key (SDK routes) | `{"error": "API key required"}` |
| `401 Unauthorized` | Invalid API key (SDK routes) | `{"error": "Invalid API key"}` |
| `401 Unauthorized` | Missing/invalid session (Admin routes) | `{"error": "Invalid or expired session"}` |
| `500 Internal Server Error` | ADMIN_API_KEY not configured | `{"error": "Admin panel not configured"}` |
| `429 Too Many Requests` | Rate limit exceeded | `{"error": "Rate limit exceeded. Try again later."}` |
