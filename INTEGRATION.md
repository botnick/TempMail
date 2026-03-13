# TempMail — API Integration Guide

> **Version**: 1.0  
> **Base URL**: `http://<API_HOST>:<API_PORT>` (default `0.0.0.0:3000`)  
> **Content-Type**: `application/json` (ทุก request/response)  
> **Source of truth**: เอกสารนี้อิง 100% จาก source code ใน `api/`, `shared/`

---

## Table of Contents

1. [Authentication](#1-authentication)
2. [Error Response Format](#2-error-response-format)
3. [Public Endpoints](#3-public-endpoints)
4. [Internal Endpoints](#4-internal-endpoints)
5. [SDK Endpoints (`/v1/`)](#5-sdk-endpoints-v1)
6. [Admin Endpoints (`/admin/`)](#6-admin-endpoints-admin)
7. [Server-Sent Events (SSE)](#7-server-sent-events-sse)
8. [Webhook Integration](#8-webhook-integration)
9. [Rate Limiting](#9-rate-limiting)
10. [Security Headers](#10-security-headers)

---

## 1. Authentication

> Source: `api/main.go` → `apiTokenMiddleware()`

### SDK & Internal Endpoints

ทุก endpoint ใน `/v1/*` และ `/internal/*` ต้องส่ง API Key ผ่าน header:

```
Authorization: Bearer <API_KEY>
```

**Validation flow:**
1. Hash API key ด้วย SHA-256
2. ตรวจสอบ hash กับ Redis set `system:api_key_hashes`
3. ถ้า key มี flag `isInternal` → อนุญาตเข้า `/internal/*` routes

**Error ถ้าไม่มี/ผิด:**

```json
// 401 Unauthorized
{ "error": "Missing or invalid API key" }
```

### Admin Endpoints

> Source: `api/main.go` → `adminSessionMiddleware()`

ทุก endpoint ใน `/admin/*` (ยกเว้น `/admin/login`) ต้องส่ง session token:

```
Cookie: admin_session=<SESSION_TOKEN>
```

**Validation flow:**
1. อ่าน cookie `admin_session`
2. Verify HMAC-SHA256 signature ด้วย `ADMIN_SESSION_SECRET`
3. ตรวจสอบ expiry จาก signed payload

**Error ถ้า session ไม่ valid:**

```json
// 401 Unauthorized
{ "error": "Unauthorized" }
```

---

## 2. Error Response Format

> Source: `shared/apiutil/errors.go`

### Structured Error (ใช้ใน SDK & Admin handlers)

```json
{
  "error": {
    "code": "error_code_string",
    "message": "Human-readable description"
  }
}
```

**ตัวอย่าง:**

```json
// 400 Bad Request
{
  "error": {
    "code": "invalid_request",
    "message": "name is required"
  }
}

// 404 Not Found
{
  "error": {
    "code": "mailbox_not_found",
    "message": "Mailbox not found"
  }
}

// 500 Internal Server Error
{
  "error": {
    "code": "database_error",
    "message": "Database error"
  }
}
```

### Simple Error (ใช้ใน middleware & บาง handler)

```json
{ "error": "Missing or invalid API key" }
```

---

## 3. Public Endpoints

### 3.1 `GET /health`

> Source: `api/main.go` line 149

Health check — ไม่ต้อง authentication

**Response `200 OK`:**
```json
{ "status": "ok" }
```

---

## 4. Internal Endpoints

### 4.1 `POST /internal/mail/ingest`

> Source: `api/handlers/ingest.go`

รับ email ที่ส่งมาจาก `mail-edge` (SMTP relay) แล้ว parse, sanitize, เก็บลง DB + R2

**Auth:** `Authorization: Bearer <INTERNAL_API_KEY>` (key ที่มี `isInternal: true`)

**Request Body:**
```json
{
  "from": "sender@example.com",
  "to": ["recipient@yourdomain.com"],
  "rawEmail": "<base64-encoded RFC822 email>"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `from` | string | ✅ | Sender address (envelope FROM) |
| `to` | []string | ✅ | Recipient addresses (envelope RCPT TO) |
| `rawEmail` | string | ✅ | Base64-encoded RFC822 raw email |

**Response `200 OK`:**
```json
{
  "status": "accepted",
  "mailbox": "test-abc12345@example.com",
  "messageId": "uuid-of-created-message"
}
```

**Error Responses:**

| Code | Error Code | Message | เมื่อ |
|------|-----------|---------|------|
| 400 | `invalid_request` | Invalid request body | Body parse ไม่ได้ |
| 400 | `no_recipients` | No recipients | `to` ว่าง |
| 400 | `invalid_email` | Failed to decode raw email | Base64 decode ล้มเหลว |
| 404 | `mailbox_not_found` | No active mailbox found | ไม่มี mailbox ที่ match |
| 422 | `spam_rejected` | Message rejected by spam filter | Rspamd score เกิน threshold |
| 500 | `storage_error` | Failed to upload to storage | R2 upload ล้มเหลว |
| 500 | `database_error` | Database error | DB insert ล้มเหลว |

**Side Effects:**
- Upload raw email → R2 key: `raw/{mailboxID}/{messageID}.eml`
- Upload attachments → R2 key: `attachments/{mailboxID}/{messageID}/{filename}`
- Sanitize HTML body (ลบ script, iframe, event handlers)
- ส่ง SSE event `mailbox:{mailboxID}` → `{"event":"new_message","messageId":"..."}`
- Fire webhook (ถ้า `webhook_url` ตั้งค่าไว้)

---

## 5. SDK Endpoints (`/v1/`)

> Source: `api/handlers/sdk.go`  
> Auth: ทุก endpoint ต้องมี `Authorization: Bearer <API_KEY>`

### 5.1 `POST /v1/mailbox/create`

> Source: `sdk.go` → `HandleCreateMailbox()`

สร้าง temporary mailbox ใหม่

**Request Body:**
```json
{
  "domain": "example.com",
  "ttl": 3600,
  "metadata": "any string"
}
```

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `domain` | string | ❌ | random active domain | Domain name ที่ต้องการ |
| `ttl` | int | ❌ | จาก setting `default_mailbox_ttl` (3600) | TTL เป็นวินาที |
| `metadata` | string | ❌ | `""` | Metadata ที่ต้องการเก็บ |

**Response `201 Created`:**
```json
{
  "id": "uuid",
  "address": "random-local@example.com",
  "localPart": "random-local",
  "domain": "example.com",
  "status": "ACTIVE",
  "expiresAt": "2025-01-01T01:00:00Z",
  "createdAt": "2025-01-01T00:00:00Z",
  "metadata": ""
}
```

**Error Responses:**

| Code | Error Code | Message | เมื่อ |
|------|-----------|---------|------|
| 400 | `invalid_request` | Invalid request body | Body parse ไม่ได้ |
| 400 | `invalid_domain` | Domain not found or not active | Domain ไม่พบหรือไม่ ACTIVE |
| 400 | `ttl_too_short` | TTL must be at least 60 seconds | TTL < 60 |
| 400 | `ttl_too_long` | TTL exceeds maximum allowed | TTL เกิน `max_mailbox_ttl` |
| 500 | `database_error` | Database error | DB insert ล้มเหลว |

---

### 5.2 `GET /v1/mailbox/count`

> Source: `sdk.go` → `HandleMailboxCount()`

นับจำนวน mailbox ทั้งหมดแบ่งตาม status

**Query Parameters:** ไม่มี

**Response `200 OK`:**
```json
{
  "total": 150,
  "active": 100,
  "expired": 50
}
```

---

### 5.3 `GET /v1/mailbox/:id`

> Source: `sdk.go` → `HandleGetMailbox()`

ดึงข้อมูล mailbox ตาม ID

**Path Parameters:**

| Param | Type | Description |
|-------|------|-------------|
| `id` | string (UUID) | Mailbox ID |

**Response `200 OK`:**
```json
{
  "id": "uuid",
  "localPart": "random-local",
  "domainId": "uuid",
  "tenantId": "tenant-1",
  "status": "ACTIVE",
  "expiresAt": "2025-01-01T01:00:00Z",
  "createdAt": "2025-01-01T00:00:00Z",
  "metadata": "",
  "messageCount": 5,
  "domain": {
    "id": "uuid",
    "domainName": "example.com"
  }
}
```

**Error Responses:**

| Code | Error Code | Message |
|------|-----------|---------|
| 404 | `mailbox_not_found` | Mailbox not found |

---

### 5.4 `GET /v1/mailbox/:id/messages`

> Source: `sdk.go` → `HandleMailboxMessages()`

ดึงรายการ messages ใน mailbox (paginated)

**Path Parameters:**

| Param | Type | Description |
|-------|------|-------------|
| `id` | string (UUID) | Mailbox ID |

**Query Parameters:**

| Param | Type | Default | Description |
|-------|------|---------|-------------|
| `limit` | int | 50 | จำนวน max per page |
| `offset` | int | 0 | Offset for pagination |

**Response `200 OK`:**
```json
{
  "messages": [
    {
      "id": "uuid",
      "mailboxId": "uuid",
      "fromAddress": "sender@example.com",
      "toAddress": "recipient@yourdomain.com",
      "subject": "Hello",
      "bodyText": "Plain text body...",
      "bodyHtml": "<p>HTML body...</p>",
      "receivedAt": "2025-01-01T00:05:00Z",
      "status": "UNREAD",
      "spamScore": 1.5,
      "attachments": []
    }
  ],
  "count": 1
}
```

**Error Responses:**

| Code | Error Code | Message |
|------|-----------|---------|
| 404 | `mailbox_not_found` | Mailbox not found |

---

### 5.5 `PATCH /v1/mailbox/:id`

> Source: `sdk.go` → `HandleUpdateMailbox()`

อัปเดต mailbox (ต่ออายุ, เปลี่ยน metadata)

**Path Parameters:**

| Param | Type | Description |
|-------|------|-------------|
| `id` | string (UUID) | Mailbox ID |

**Request Body:**
```json
{
  "ttl": 7200,
  "metadata": "updated info"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `ttl` | int | ❌ | TTL ใหม่ (วินาที) จะต่อจาก now |
| `metadata` | string | ❌ | Metadata ใหม่ |

**Response `200 OK`:**
```json
{
  "id": "uuid",
  "localPart": "random-local",
  "status": "ACTIVE",
  "expiresAt": "2025-01-01T02:00:00Z",
  "metadata": "updated info"
}
```

**Error Responses:**

| Code | Error Code | Message |
|------|-----------|---------|
| 400 | `invalid_request` | Invalid request body |
| 404 | `mailbox_not_found` | Mailbox not found |

---

### 5.6 `DELETE /v1/mailbox/:id`

> Source: `sdk.go` → `HandleDeleteMailbox()`

ลบ mailbox (hard delete + ลบ messages, attachments)

**Response `200 OK`:**
```json
{
  "status": "deleted",
  "id": "uuid"
}
```

**Error Responses:**

| Code | Error Code | Message |
|------|-----------|---------|
| 404 | `mailbox_not_found` | Mailbox not found |
| 500 | `database_error` | Database error |

---

### 5.7 `GET /v1/message/:id`

> Source: `sdk.go` → `HandleGetMessage()`

ดึงข้อมูล message พร้อม attachments

**Response `200 OK`:** (GORM model `Message` + preloaded `Attachments`)
```json
{
  "id": "uuid",
  "mailboxId": "uuid",
  "fromAddress": "sender@example.com",
  "toAddress": "recipient@yourdomain.com",
  "subject": "Hello",
  "bodyText": "...",
  "bodyHtml": "<p>...</p>",
  "s3KeyRaw": "raw/mailboxId/messageId.eml",
  "receivedAt": "2025-01-01T00:05:00Z",
  "status": "UNREAD",
  "spamScore": 1.5,
  "quarantineAction": "ACCEPT",
  "attachments": [
    {
      "id": "uuid",
      "messageId": "uuid",
      "filename": "doc.pdf",
      "contentType": "application/pdf",
      "size": 102400,
      "s3Key": "attachments/mailboxId/messageId/doc.pdf"
    }
  ]
}
```

**Side Effect:** ถ้า status เป็น `UNREAD` จะอัปเดตเป็น `READ`

**Error Responses:**

| Code | Error Code | Message |
|------|-----------|---------|
| 404 | `message_not_found` | Message not found |

---

### 5.8 `DELETE /v1/message/:id`

> Source: `sdk.go` → `HandleDeleteMessage()`

ลบ message (hard delete + ลบ attachments)

**Response `200 OK`:**
```json
{
  "status": "deleted",
  "id": "uuid"
}
```

**Error Responses:**

| Code | Error Code | Message |
|------|-----------|---------|
| 404 | `message_not_found` | Message not found |

---

### 5.9 `GET /v1/domains`

> Source: `sdk.go` → `HandleListDomains()`

ดึงรายการ domain ที่ ACTIVE

**Response `200 OK`:**
```json
{
  "domains": [
    {
      "id": "uuid",
      "domainName": "example.com",
      "status": "ACTIVE",
      "createdAt": "2025-01-01T00:00:00Z"
    }
  ]
}
```

---

### 5.10 `GET /v1/attachment/:id`

> Source: `sdk.go` → `HandleDownloadAttachment()`

ดาวน์โหลดไฟล์ attachment จาก R2

**Response `200 OK`:**
- `Content-Type`: ตาม `contentType` ของ attachment
- `Content-Disposition`: `attachment; filename="<filename>"`
- Body: binary file data

**Error Responses:**

| Code | Error Code | Message |
|------|-----------|---------|
| 404 | `attachment_not_found` | Attachment not found |
| 500 | `storage_error` | Failed to download from storage |

---

## 6. Admin Endpoints (`/admin/`)

> Source: `api/handlers/admin.go`  
> Auth: ทุก endpoint ต้องมี `Cookie: admin_session=<TOKEN>` (ยกเว้น `/admin/login`)

---

### 6.1 `POST /admin/login`

> Source: `admin.go` → `HandleAdminLogin()`

Login ด้วย username + password → รับ session cookie

**Request Body:**
```json
{
  "username": "admin",
  "password": "secret"
}
```

**Response `200 OK`:**
```json
{ "status": "ok" }
```
+ Set-Cookie: `admin_session=<HMAC_SIGNED_TOKEN>; HttpOnly; SameSite=Strict; Max-Age=86400`

**Error Responses:**

| Code | Body |
|------|------|
| 400 | `{"error":"Missing credentials"}` |
| 401 | `{"error":"Invalid credentials"}` |

---

### 6.2 `GET /admin/dashboard`

> Source: `admin.go` → `HandleAdminDashboard()`

ข้อมูลรวมสำหรับ dashboard

**Response `200 OK`:**
```json
{
  "domains": { "total": 5, "active": 3, "pending": 2 },
  "mailboxes": { "total": 150, "active": 100 },
  "messages": { "total": 5000, "unread": 200 },
  "nodes": { "total": 2, "active": 2 },
  "recentMessages": [ /* 10 latest messages */ ]
}
```

---

### 6.3 Domain Management

#### `GET /admin/domains`
List domains (paginated, filterable)

| Query Param | Type | Default | Description |
|-------------|------|---------|-------------|
| `search` | string | `""` | ค้นหาชื่อ domain |
| `status` | string | `""` | Filter: `ACTIVE`, `PENDING`, `DISABLED`, `DELETED` |
| `limit` | int | 50 | Max per page |
| `offset` | int | 0 | Pagination offset |

**Response `200 OK`:**
```json
{ "domains": [/* Domain objects with Node preloaded */], "count": 5 }
```

#### `POST /admin/domains`
สร้าง domain ใหม่ (หรือ reactivate domain ที่ DELETED)

**Request Body:**
```json
{ "domainName": "example.com", "nodeId": "uuid-or-empty" }
```

**Response `201 Created`:**
```json
{
  "domain": { /* Domain object */ },
  "dns": {
    "mx": { "host": "example.com", "value": "mail.example.com", "priority": 10 },
    "spf": { "host": "example.com", "value": "v=spf1 ip4:1.2.3.4 -all" }
  }
}
```

| Error Code | Status | Message |
|-----------|--------|---------|
| 400 `invalid_request` | 400 | Domain name is required |
| 409 `domain_exists` | 409 | Domain already exists |
| 500 `database_error` | 500 | Database error |

**Audit:** `domain.create` หรือ `domain.reactivate` (สำหรับ DELETED domain)

#### `PUT /admin/domains/:id`
อัปเดต domain (เปลี่ยน node, status)

**Request Body:**
```json
{ "nodeId": "uuid-or-null", "status": "ACTIVE" }
```

Valid statuses: `ACTIVE`, `PENDING`, `DISABLED`

**Response `200 OK`:** Domain object (with Node preloaded)

| Error Code | Status | Message |
|-----------|--------|---------|
| 404 `domain_not_found` | 404 | Domain not found |
| 400 `invalid_node` | 400 | Node not found |

**Audit:** `domain.update`

#### `DELETE /admin/domains/:id`
Hard-delete domain + cascade ลบ mailboxes, messages, attachments

**Response `200 OK`:**
```json
{ "status": "deleted", "domain": "example.com" }
```

| Error Code | Status | Message |
|-----------|--------|---------|
| 404 `domain_not_found` | 404 | Domain not found |

**Audit:** `domain.delete`

---

### 6.4 Node Management

#### `GET /admin/nodes`

| Query Param | Type | Default | Description |
|-------------|------|---------|-------------|
| `search` | string | `""` | ค้นหา name, IP, region |
| `status` | string | `""` | Filter: `ACTIVE`, `DISABLED` |
| `limit` | int | 50 | Max per page |
| `offset` | int | 0 | Offset |

**Response `200 OK`:**
```json
{ "nodes": [/* MailNode objects with Domains preloaded */], "count": 2 }
```

#### `POST /admin/nodes`

**Request Body:**
```json
{ "name": "node-sg-1", "ipAddress": "1.2.3.4", "region": "SGP" }
```

| Field | Required |
|-------|----------|
| `name` | ✅ |
| `ipAddress` | ✅ |
| `region` | ❌ |

**Response `201 Created`:** MailNode object (status defaults `ACTIVE`)

| Error Code | Status | Message |
|-----------|--------|---------|
| 400 `invalid_request` | 400 | name and ipAddress are required |
| 500 `database_error` | 500 | Database error |

#### `PUT /admin/nodes/:id`

**Request Body:**
```json
{ "name": "new-name", "ipAddress": "5.6.7.8", "region": "US", "status": "DISABLED" }
```

Valid statuses: `ACTIVE`, `DISABLED` — เฉพาะ field ที่ส่งมาจะถูกอัปเดต

**Response `200 OK`:** Updated MailNode, **Audit:** `node.update`

#### `DELETE /admin/nodes/:id`

**Response `200 OK`:**
```json
{ "status": "deleted" }
```

| Error Code | Status | Message |
|-----------|--------|---------|
| 404 `node_not_found` | 404 | Node not found |
| 409 `node_in_use` | 409 | Cannot delete node: N domain(s) still assigned |

**Audit:** `node.delete`

---

### 6.5 Mailbox Management

#### `GET /admin/mailboxes`

| Query Param | Type | Default | Description |
|-------------|------|---------|-------------|
| `search` | string | `""` | ค้นหา local_part, tenant_id, metadata |
| `status` | string | `""` | Filter: `ACTIVE`, `DELETED` |
| `limit` | int | 50 | Max per page |
| `offset` | int | 0 | Offset |

**Response `200 OK`:**
```json
{
  "mailboxes": [/* Mailbox objects with Domain preloaded */],
  "count": 100
}
```

#### `DELETE /admin/mailboxes/:id`
Hard-delete mailbox + cascade ลบ messages, attachments + ลบจาก Redis set

**Response `200 OK`:**
```json
{ "status": "deleted" }
```

**Audit:** `mailbox.delete`

#### `POST /admin/mailboxes/quick-create`
Quick-create mailbox สำหรับ testing

**Request Body:**
```json
{ "domainId": "uuid-or-empty", "ttlHours": 1 }
```

**Response `201 Created`:**
```json
{
  "id": "uuid",
  "address": "test-abc12345@example.com",
  "localPart": "test-abc12345",
  "domain": "example.com",
  "expiresAt": "2025-01-01T01:00:00Z",
  "ttlHours": 1
}
```

| Error Code | Status | Message |
|-----------|--------|---------|
| 400 `domain_not_found` | 400 | Domain not found or inactive |
| 400 `no_domain` | 400 | No active domain available |
| 500 `database_error` | 500 | Database error |

**Audit:** `mailbox.quick_create`

---

### 6.6 Message Management

#### `GET /admin/messages`

| Query Param | Type | Default | Description |
|-------------|------|---------|-------------|
| `search` | string | `""` | ค้นหา subject, from, to |
| `mailbox` | string | `""` | Filter by mailbox ID |
| `status` | string | `""` | Filter: `UNREAD`, `READ` |
| `limit` | int | 50 | Max per page |
| `offset` | int | 0 | Offset |

**Response `200 OK`:**
```json
{ "messages": [/* Message objects */], "count": 500 }
```

#### `GET /admin/messages/:id`  
ดูรายละเอียด message (preload Attachments)

**Response `200 OK`:** Full Message object with Attachments array

| Error Code | Status |
|-----------|--------|
| 404 `message_not_found` | 404 |

#### `DELETE /admin/messages/:id`  
Hard-delete message + attachments

**Response `200 OK`:**
```json
{ "status": "deleted", "id": "uuid" }
```
**Audit:** `message.delete`

#### `POST /admin/messages/bulk-delete`  
ลบ messages แบบ bulk (ใน transaction)

**Request Body:**
```json
{ "ids": ["uuid-1", "uuid-2", "uuid-3"] }
```

**Limits:** max 100 IDs per request

**Response `200 OK`:**
```json
{ "status": "deleted", "deleted": 3 }
```

| Error Code | Status | Message |
|-----------|--------|---------|
| 400 `invalid_request` | 400 | Invalid request body |
| 400 `empty_ids` | 400 | No message IDs provided |
| 400 `too_many_ids` | 400 | Maximum 100 IDs per request |
| 500 `database_error` | 500 | Bulk delete failed |

**Audit:** `messages.bulk_delete`

---

### 6.7 Domain Filter Management

#### `GET /admin/filters`

| Query Param | Type | Default | Description |
|-------------|------|---------|-------------|
| `search` | string | `""` | ค้นหา pattern, reason |
| `type` | string | `""` | Filter: `BLOCK`, `ALLOW` |
| `limit` | int | 50 | Max per page |
| `offset` | int | 0 | Offset |

**Response `200 OK`:**
```json
{ "filters": [/* DomainFilter objects */], "count": 10 }
```

#### `POST /admin/filters`

**Request Body:**
```json
{ "pattern": "spam.com", "filterType": "BLOCK", "reason": "Known spam domain" }
```

Valid `filterType`: `BLOCK` | `ALLOW`

**Response `201 Created`:** DomainFilter object (pattern จะ lowercase อัตโนมัติ)

| Error Code | Status | Message |
|-----------|--------|---------|
| 400 `invalid_request` | 400 | pattern and filterType (BLOCK/ALLOW) are required |
| 409 `filter_exists` | 409 | Filter pattern already exists |

**Side Effect:** Sync filters to Redis sets (`system:domain_blocklist`, `system:domain_allowlist`)  
**Audit:** `filter.create`

#### `PUT /admin/filters/:id`

**Request Body:**
```json
{ "pattern": "new-pattern.com", "filterType": "ALLOW", "reason": "Updated" }
```

**Response `200 OK`:** Updated DomainFilter, **Audit:** `filter.update`

#### `DELETE /admin/filters/:id`

**Response `200 OK`:**
```json
{ "status": "deleted" }
```
**Audit:** `filter.delete`

---

### 6.8 API Key Management

#### `GET /admin/api-keys`

| Query Param | Type | Default | Description |
|-------------|------|---------|-------------|
| `search` | string | `""` | ค้นหา name, key_prefix |
| `status` | string | `""` | Filter: `ACTIVE`, `REVOKED`, `DISABLED` |
| `limit` | int | 50 | Max per page |
| `offset` | int | 0 | Offset |

**Response `200 OK`:**
```json
{ "keys": [/* APIKey objects (KeyHash ซ่อนอยู่) */], "count": 3 }
```

#### `POST /admin/api-keys`

**Request Body:**
```json
{
  "name": "SDK Key",
  "permissions": "read,write",
  "rateLimit": 100,
  "isInternal": false
}
```

| Field | Required | Default |
|-------|----------|---------|
| `name` | ✅ | — |
| `permissions` | ❌ | `"read,write"` |
| `rateLimit` | ❌ | `100` |
| `isInternal` | ❌ | `false` |

**Response `201 Created`:**
```json
{
  "key": { /* APIKey object */ },
  "rawKey": "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx-xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
  "notice": "Save this key now — it cannot be shown again!"
}
```

> ⚠️ `rawKey` แสดงครั้งเดียวตอน create — ไม่สามารถ retrieve ได้อีก

**Side Effects:** Sync key hashes → Redis, ถ้า internal → set `system:api_token`  
**Audit:** `apikey.create`

#### `PUT /admin/api-keys/:id`

**Request Body:**
```json
{
  "name": "Updated Name",
  "permissions": "read",
  "rateLimit": 200,
  "status": "REVOKED",
  "isInternal": true
}
```

Valid statuses: `ACTIVE`, `REVOKED`, `DISABLED`

**Response `200 OK`:** Updated APIKey object, **Audit:** `apikey.update`

#### `DELETE /admin/api-keys/:id`

**Response `200 OK`:**
```json
{ "status": "deleted" }
```

| Error Code | Status |
|-----------|--------|
| 404 `key_not_found` | 404 |

**Audit:** `apikey.delete`

#### `POST /admin/api-keys/:id/roll`
Generate new key ทดแทน key เดิม

**Response `200 OK`:**
```json
{
  "status": "rolled",
  "key": { /* Updated APIKey */ },
  "rawKey": "new-xxxxxxxx-..."
}
```

**Audit:** `apikey.roll`

---

### 6.9 Settings

#### `GET /admin/settings`
ดึง settings ทั้งหมดจาก Redis hash `system:settings`

**Response `200 OK`:**
```json
{
  "settings": {
    "default_mailbox_ttl": "3600",
    "max_mailbox_ttl": "86400",
    "max_message_size": "26214400",
    "retention_days": "30",
    "spam_threshold": "5.0",
    "webhook_url": "",
    "webhook_secret": "",
    "quarantine_action": "TAG",
    "max_attachments": "10",
    "allowed_attachment_types": "*"
  }
}
```

#### `PUT /admin/settings`
อัปเดต settings (validate ต่อ `allowedSettings` map)

**Request Body:**
```json
{
  "default_mailbox_ttl": "7200",
  "webhook_url": "https://example.com/hook"
}
```

**Allowed keys** (source: `admin.go` `allowedSettings` map):

| Key | Type | Min | Max |
|-----|------|-----|-----|
| `default_mailbox_ttl` | numeric | 60 | 604800 |
| `max_mailbox_ttl` | numeric | 300 | 2592000 |
| `max_message_size` | numeric | 1048576 | 52428800 |
| `retention_days` | numeric | 1 | 365 |
| `spam_threshold` | numeric | 0 | 100 |
| `webhook_url` | string | — | — |
| `webhook_secret` | string | — | — |
| `quarantine_action` | string | — | — |
| `max_attachments` | numeric | 0 | 50 |
| `allowed_attachment_types` | string | — | — |

**Response `200 OK`:**
```json
{ "status": "updated", "updated": ["default_mailbox_ttl", "webhook_url"] }
```

| Error Code | Status | Message |
|-----------|--------|---------|
| 400 `invalid_request` | 400 | Invalid request body |
| 400 `invalid_setting` | 400 | Unknown setting key: xxx |
| 400 `invalid_value` | 400 | Value for xxx must be between min and max |

**Audit:** `settings.update`

---

### 6.10 DNS Check

#### `GET /admin/dns-check/:domain`

ตรวจสอบ DNS records (MX, A, SPF, DMARC) ของ domain

**Response `200 OK`:**
```json
{
  "records": [
    { "type": "MX", "expected": "mail.example.com", "actual": "mail.example.com", "status": "OK" },
    { "type": "A", "expected": "1.2.3.4", "actual": "1.2.3.4", "status": "OK" },
    { "type": "SPF", "expected": "v=spf1 ...", "actual": "...", "status": "WARN" },
    { "type": "DMARC", "expected": "v=DMARC1 ...", "actual": "", "status": "FAIL" }
  ],
  "allOk": false,
  "summary": "Some DNS records are missing or misconfigured."
}
```

**Record statuses:** `OK`, `WARN`, `FAIL`

---

### 6.11 System Metrics

#### `GET /admin/metrics`

**Response `200 OK`:**
```json
{
  "throughput": { "lastHour": 250, "last24h": 5000 },
  "storage": { "totalMessages": 50000, "totalAttachments": 12000 },
  "mailboxes": { "active": 100, "expiredPending": 15 },
  "spam": { "blockedMessages": 200, "blocklistRules": 50, "allowlistRules": 10 },
  "redis": { "dbSize": 1500, "usedMemory": "25.50M" }
}
```

---

### 6.12 Export / Import

#### `GET /admin/export`
Export configuration เป็น JSON file

**Response `200 OK`:**
- `Content-Disposition: attachment; filename=tempmail-config-YYYY-MM-DD.json`
```json
{
  "exportedAt": "2025-01-01T00:00:00Z",
  "version": "1.0",
  "domains": [/* Domain objects */],
  "nodes": [/* MailNode objects */],
  "filters": [/* DomainFilter objects */],
  "settings": { /* key-value map */ }
}
```

#### `POST /admin/import`
Import configuration จาก JSON

**Request Body:**
```json
{
  "settings": { "key": "value" },
  "filters": [{ "pattern": "spam.com", "filterType": "BLOCK", "reason": "..." }]
}
```

**Limits:** max 1000 filters, max 50 settings per import

**Response `200 OK`:**
```json
{ "status": "imported", "count": 5 }
```

| Error Code | Status | Message |
|-----------|--------|---------|
| 400 `invalid_json` | 400 | Invalid JSON format |
| 400 `too_many_filters` | 400 | Maximum 1000 filters per import |
| 400 `too_many_settings` | 400 | Maximum 50 settings per import |

**Audit:** `config.import`

---

### 6.13 Webhook Test

#### `POST /admin/webhook-test`

ส่ง test payload ไปยัง webhook URL ที่ตั้งค่าไว้

**Response `200 OK`:**
```json
{ "status": "ok", "response": "...", "url": "https://example.com/hook" }
```

ถ้า webhook URL ยังไม่ตั้ง:
```json
// 400
{ "error": { "code": "no_webhook", "message": "Webhook URL not configured in Settings" } }
```

ถ้า webhook ตอบ error:
```json
{ "status": "error", "error": "webhook returned 500: ...", "url": "..." }
```

---

### 6.14 Audit Log

#### `GET /admin/audit-log`

| Query Param | Type | Default | Description |
|-------------|------|---------|-------------|
| `search` | string | `""` | ค้นหา action, target_id, ip_address |
| `action` | string | `""` | Filter by specific action |
| `limit` | int | 50 | Max per page |
| `offset` | int | 0 | Offset |

**Response `200 OK`:**
```json
{
  "logs": [
    {
      "id": "uuid",
      "action": "domain.create",
      "targetId": "uuid",
      "ipAddress": "127.0.0.1",
      "createdAt": "2025-01-01T00:00:00Z"
    }
  ],
  "count": 100,
  "actions": ["domain.create", "domain.delete", "apikey.create", ...]
}
```

**All audit actions (from source):**

| Action | Trigger |
|--------|---------|
| `domain.create` | สร้าง domain ใหม่ |
| `domain.reactivate` | Reactivate deleted domain |
| `domain.update` | แก้ไข domain |
| `domain.delete` | ลบ domain |
| `node.update` | แก้ไข node |
| `node.delete` | ลบ node |
| `mailbox.delete` | ลบ mailbox |
| `mailbox.quick_create` | Quick-create mailbox |
| `message.delete` | ลบ message |
| `messages.bulk_delete` | Bulk-delete messages |
| `filter.create` | สร้าง filter |
| `filter.update` | แก้ไข filter |
| `filter.delete` | ลบ filter |
| `apikey.create` | สร้าง API key |
| `apikey.update` | แก้ไข API key |
| `apikey.delete` | ลบ API key |
| `apikey.roll` | Roll API key |
| `settings.update` | อัปเดต settings |
| `config.import` | Import configuration |
| `retention_cleanup` | Worker: ลบข้อมูลเก่า |
| `mailbox_expired` | Worker: ลบ mailbox หมดอายุ |

---

## 7. Server-Sent Events (SSE)

> Source: `api/main.go` → `handleSSE()`

### `GET /events`

**Auth:** `Authorization: Bearer <API_KEY>` (query param `token` ก็ได้)

**Query Parameters:**

| Param | Type | Description |
|-------|------|-------------|
| `channel` | string | Channel ที่ต้องการ subscribe (เช่น `mailbox:{id}`) |
| `token` | string | API key (alternative to Authorization header) |

**Event Format:**
```
event: message
data: {"event":"new_message","messageId":"uuid","mailboxId":"uuid"}
```

**Heartbeat:** ส่ง `:heartbeat\n\n` ทุก 30 วินาที

---

## 8. Webhook Integration

> Source: `api/handlers/admin.go` → `fireWebhook()`, `FireMessageWebhook()`

### Webhook Payload

เมื่อมี message ใหม่เข้ามา (ถ้า `webhook_url` ตั้งค่าไว้):

```json
{
  "event": "message.received",
  "mailboxId": "uuid",
  "messageId": "uuid",
  "to": "recipient@example.com",
  "from": "sender@example.com",
  "subject": "Hello",
  "timestamp": "2025-01-01T00:05:00Z"
}
```

### Webhook Headers

| Header | Value |
|--------|-------|
| `Content-Type` | `application/json` |
| `X-Webhook-Event` | `message.received` หรือ `test` |
| `X-Webhook-Signature` | `sha256=<HMAC-SHA256 hex>` (ถ้า `webhook_secret` ตั้งค่าไว้) |

### Signature Verification

```python
# ตัวอย่าง Python
import hmac, hashlib
expected = hmac.new(secret.encode(), body, hashlib.sha256).hexdigest()
assert signature == f"sha256={expected}"
```

---

## 9. Rate Limiting

> Source: `api/main.go` → Fiber `limiter` middleware

| Scope | Max Requests | Window | Response |
|-------|-------------|--------|----------|
| Global API | 100 | 1 minute | `429 Too Many Requests` |

```json
// 429 response body
{
  "error": "Too many requests, please try again later"
}
```

---

## 10. Security Headers

> Source: `api/main.go` → security headers middleware

ทุก response จะมี headers เหล่านี้:

| Header | Value |
|--------|-------|
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `X-XSS-Protection` | `1; mode=block` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |
| `Permissions-Policy` | `camera=(), microphone=(), geolocation=()` |

---

> 📌 **หมายเหตุ**: เอกสารนี้อิง 100% จาก source code — ไม่มีส่วนใดที่ assume หรือคิดขึ้นมาเอง  
> 📁 **Source files**: `api/main.go`, `api/handlers/sdk.go`, `api/handlers/ingest.go`, `api/handlers/admin.go`, `shared/apiutil/errors.go`, `shared/models/models.go`
