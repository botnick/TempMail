# TempMail API — Integration Guide for Main Web App

> **Version**: 1.0  
> **Base URL**: `https://api.your-tempmail-server.com`  
> **Auth**: Every request must include header `X-API-Key: YOUR_EXTERNAL_API_KEY`

---

## Overview

TempMail is a **backend email service** that receives real emails from the internet, filters spam, and stores them temporarily. Your web app creates disposable mailboxes via API, users receive real emails, and mailboxes auto-expire.

```
Your Web App (Server-Side)          TempMail Service
┌────────────────────┐              ┌─────────────────────┐
│                    │  API calls   │  mail-edge (SMTP)    │
│  Node.js / Go /    │◄───────────►│  api (REST)          │
│  PHP / Python      │  X-API-Key  │  Rspamd (spam)       │
│                    │              │  Redis + Postgres    │
└────────────────────┘              │  Worker (auto-expire)│
                                    └─────────────────────┘
```

**Security model:**
- All API calls require `X-API-Key` — without it, every request returns `401`
- Mailbox IDs are UUIDs — unguessable, not sequential
- Your app stores the mailbox ID after creation — only your app can access it
- No user can discover or access another user's mailbox without knowing the UUID
- HTML content is sanitized server-side (XSS-safe) before storage

---

## Authentication

Include this header on **every** request:

```
X-API-Key: YOUR_EXTERNAL_API_KEY
```

Example (Node.js):
```js
const API_URL = 'https://api.your-tempmail-server.com';
const API_KEY = process.env.TEMPMAIL_API_KEY;

const headers = {
  'X-API-Key': API_KEY,
  'Content-Type': 'application/json'
};
```

---

## Endpoints

### 1. List Available Domains

```
GET /v1/domains
```

Returns all active domains that can receive email.

**Response:**
```json
{
  "count": 2,
  "domains": [
    { "id": "d_abc123", "domainName": "tempmail.io", "isPublic": true },
    { "id": "d_def456", "domainName": "quickmail.dev", "isPublic": true }
  ]
}
```

**Use case:** Show domain dropdown to user when creating a mailbox.

---

### 2. Create Mailbox

```
POST /v1/mailbox/create
Content-Type: application/json
```

**Request body:**
```json
{
  "localPart": "john",
  "domainId": "d_abc123",
  "tenantId": "user_12345",
  "ttlHours": 48
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `localPart` | No | Email name (a-z, 0-9, `.` `-` `_`, 1-64 chars). Empty = random word-based name (e.g. `cool.fox42`) |
| `domainId` | No | Specific domain ID from `/v1/domains`. Empty = first public domain |
| `tenantId` | **Yes** | Your user's ID. Used for audit and isolation. Empty = "anonymous" |
| `ttlHours` | No | Mailbox lifetime in hours. Default = 24 |

**Response (201):**
```json
{
  "id": "mb_7f3a2b1c-...",
  "address": "john@tempmail.io",
  "localPart": "john",
  "domain": "tempmail.io",
  "domainId": "d_abc123",
  "expiresAt": "2026-03-14T06:00:00Z",
  "status": "ACTIVE"
}
```

> **⚠ IMPORTANT:** Store `id` in your database. This is the only way to access the mailbox later.  
> The mailbox ID is a UUID — it cannot be guessed or enumerated.

**Error responses:**

| Status | Meaning |
|--------|---------|
| 400 | Invalid name, bad characters, or no domain available |
| 409 | Name already taken on that domain |
| 401 | Missing or invalid API key |

---

### 3. Get Messages (Inbox List)

```
GET /v1/mailbox/{mailbox_id}/messages
```

Returns up to 100 most recent messages, newest first.

**Response:**
```json
{
  "mailboxId": "mb_7f3a2b1c-...",
  "count": 3,
  "messages": [
    {
      "id": "msg_a1b2c3d4-...",
      "from": "noreply@github.com",
      "subject": "Verify your email",
      "spamScore": 0.5,
      "isSpam": false,
      "quarantineAction": "ACCEPT",
      "hasHtml": true,
      "receivedAt": "2026-03-12T06:30:00Z",
      "expiresAt": "2026-03-14T06:00:00Z"
    }
  ]
}
```

| Field | Description |
|-------|-------------|
| `isSpam` | `true` if email was flagged by spam filter — show warning badge |
| `quarantineAction` | `"ACCEPT"` = clean, `"QUARANTINE"` = suspicious, show but warn |
| `spamScore` | Rspamd score. Lower = cleaner. Above 15 = auto-rejected at SMTP level |
| `hasHtml` | If message has rich HTML body |

> **Note:** This endpoint returns summaries only (no body content). Use endpoint #4 to read full message.

---

### 4. Read Full Message

```
GET /v1/message/{message_id}
```

Returns the complete message including body and attachments.

**Response:**
```json
{
  "id": "msg_a1b2c3d4-...",
  "mailboxId": "mb_7f3a2b1c-...",
  "from": "noreply@github.com",
  "to": "john@tempmail.io",
  "subject": "Verify your email",
  "textBody": "Click here to verify...",
  "htmlBody": "<div>Click <a href='...'>here</a> to verify</div>",
  "spamScore": 0.5,
  "quarantineAction": "ACCEPT",
  "attachments": [
    {
      "id": "att_x1y2z3-...",
      "filename": "invoice.pdf",
      "contentType": "application/pdf",
      "sizeBytes": 204800
    }
  ],
  "receivedAt": "2026-03-12T06:30:00Z",
  "expiresAt": "2026-03-14T06:00:00Z"
}
```

> **Security:** `htmlBody` is already sanitized server-side using bluemonday. It is safe to render via `dangerouslySetInnerHTML` (React) or `v-html` (Vue), but you may add additional sanitization on your end if desired.

---

### 5. Delete Mailbox

```
DELETE /v1/mailbox/{mailbox_id}
```

Soft-deletes the mailbox. SMTP will immediately stop accepting new email for this address.

**Response:**
```json
{
  "status": "deleted",
  "id": "mb_7f3a2b1c-..."
}
```

---

## Recommended Architecture — Event-Driven (NOT Polling)

> **⛔ Do NOT use polling.** Polling every N seconds wastes resources on both sides.  
> Email arrival is unpredictable — polling will either be too slow (user waits) or too frequent (wastes API calls).

### ✅ Recommended: User-Triggered Fetch

The simplest and most efficient pattern:

```
1. User opens inbox page → Your app calls GET /v1/mailbox/{id}/messages
2. User clicks "Refresh" → Your app calls GET /v1/mailbox/{id}/messages again
3. User clicks a message → Your app calls GET /v1/message/{msg_id}
```

This is how most temp mail services work. The user triggers the fetch, not a timer.

### ✅ Alternative: Short-Lived Polling with Backoff (if needed)

If your UX requires auto-refresh (e.g., waiting for a verification email), use progressive backoff:

```js
// Only poll while user is actively waiting on the inbox page
async function waitForEmail(mailboxId, maxWaitMs = 120000) {
  const start = Date.now();
  let interval = 3000; // Start at 3s

  while (Date.now() - start < maxWaitMs) {
    const { messages } = await fetchMessages(mailboxId);
    if (messages.length > 0) return messages;

    await sleep(interval);
    interval = Math.min(interval * 1.5, 10000); // Max 10s between checks
  }

  return []; // Timed out
}

// STOP polling when user leaves the page
```

**Rules for polling:**
- Poll **only** while user is on the inbox page
- Stop immediately when user navigates away
- Use exponential backoff (3s → 4.5s → 6.75s → 10s cap)
- Set a hard timeout (2 minutes max)
- Never poll in the background

---

## Complete Integration Example (Node.js)

```js
// tempmail-client.js — Server-side only, never expose API_KEY to browser

const API_URL = process.env.TEMPMAIL_API_URL;
const API_KEY = process.env.TEMPMAIL_API_KEY;

async function tmFetch(path, method = 'GET', body = null) {
  const opts = {
    method,
    headers: { 'X-API-Key': API_KEY, 'Content-Type': 'application/json' },
  };
  if (body) opts.body = JSON.stringify(body);

  const res = await fetch(`${API_URL}${path}`, opts);
  if (!res.ok) {
    const err = await res.json().catch(() => ({}));
    throw new Error(err.error || `TempMail API error: ${res.status}`);
  }
  return res.json();
}

// ─── Exported Functions ──────────────────────────

export async function getDomains() {
  return tmFetch('/v1/domains');
}

export async function createMailbox(userId, localPart = '', domainId = '', ttlHours = 24) {
  return tmFetch('/v1/mailbox/create', 'POST', {
    localPart, domainId, tenantId: userId, ttlHours
  });
}

export async function getMessages(mailboxId) {
  return tmFetch(`/v1/mailbox/${mailboxId}/messages`);
}

export async function readMessage(messageId) {
  return tmFetch(`/v1/message/${messageId}`);
}

export async function deleteMailbox(mailboxId) {
  return tmFetch(`/v1/mailbox/${mailboxId}`, 'DELETE');
}
```

### Usage in your route handler:

```js
import { createMailbox, getMessages, readMessage } from './tempmail-client.js';

// POST /api/inbox/create
app.post('/api/inbox/create', async (req, res) => {
  const userId = req.user.id; // from your auth
  const { name, domain } = req.body;

  const mailbox = await createMailbox(userId, name, domain, 48);

  // Store mailbox.id in YOUR database, linked to userId
  await db.userMailboxes.create({
    userId,
    mailboxId: mailbox.id,
    address: mailbox.address,
    expiresAt: mailbox.expiresAt,
  });

  res.json({ address: mailbox.address, expiresAt: mailbox.expiresAt });
});

// GET /api/inbox/:mailboxId/messages
app.get('/api/inbox/:mailboxId/messages', async (req, res) => {
  // ⚠ CRITICAL: Verify this mailbox belongs to the logged-in user
  const ownership = await db.userMailboxes.findOne({
    where: { mailboxId: req.params.mailboxId, userId: req.user.id }
  });
  if (!ownership) return res.status(403).json({ error: 'Not your mailbox' });

  const data = await getMessages(req.params.mailboxId);
  res.json(data);
});
```

---

## Security Checklist

| ✅ | Item |
|---|------|
| ✅ | Call API from **server-side only** — never expose `API_KEY` to browser JavaScript |
| ✅ | Store `mailboxId` in your DB linked to `userId` — verify ownership on every request |
| ✅ | Never let users pass arbitrary `mailboxId` without ownership check |
| ✅ | Use `tenantId` (your userId) when creating mailbox — for audit trail |
| ✅ | Show spam warning (`isSpam: true`) to users, but don't hide the message |
| ✅ | Set appropriate `ttlHours` — shorter = less data exposure |
| ✅ | Don't poll — use user-triggered fetch or backoff pattern |

---

## Error Handling

All errors return JSON with consistent format:

```json
{ "error": "Human-readable error message" }
```

| HTTP Status | Meaning | Action |
|-------------|---------|--------|
| 400 | Bad request (invalid params) | Fix request body/params |
| 401 | Missing or invalid `X-API-Key` | Check your API key |
| 404 | Mailbox or message not found | Mailbox may have expired |
| 409 | Conflict (name taken) | Try different local part |
| 429 | Rate limited | Back off and retry |
| 500 | Server error | Retry after delay, check TempMail logs |

---

## Configuration Limits

These limits are configured on the TempMail server:

| Limit | Default | Description |
|-------|---------|-------------|
| Max message size | 25 MB | Emails larger than this are rejected at SMTP level |
| Max attachments per email | 10 | Extra attachments are dropped |
| Max attachment size | 10 MB | Per-file limit |
| Spam reject threshold | 15 | Rspamd score; above = rejected at SMTP, below = accepted with `spamScore` |
| Messages per mailbox | 100 | Latest 100 returned by GET messages |
| Mailbox name length | 1-64 chars | Allowed: `a-z 0-9 . - _` |

---

## Environment Variables (Your Web App)

Add these to your `.env`:

```env
TEMPMAIL_API_URL=https://api.your-tempmail-server.com
TEMPMAIL_API_KEY=your_external_api_key_here
```

> **Never** put `TEMPMAIL_API_KEY` in frontend code, client-side JavaScript, or public repositories.
