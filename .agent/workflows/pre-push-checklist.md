---
description: Pre-push documentation checklist — MUST run before every git commit/push
---

# Pre-Push Documentation Checklist

**ทุกครั้งก่อน git commit + push ต้องอัพเดทเอกสารทั้งหมดให้ตรงกับ code ล่าสุด:**

## 1. Version Bump
// turbo
- `lib.sh` → `TEMPMAIL_VERSION="x.y.z"`
- `.env.example` → header comment
- All `.html` file footers

## 2. README.md (ละเอียด ครบถ้วน ไม่งง)
- Architecture table
- Configuration tables (env vars, Redis settings)
- API routes table (SDK + Admin) — ต้องตรงกับ `main.go` routes
- Admin Panel features table
- Models table — ต้องตรงกับ `models.go`
- File structure tree
- Security section

## 3. HTML Guides
- `API_INTEGRATION_TH.html` — endpoints table, detail sections, code examples, cURL, footer version
- `INSTALL_GUIDE_TH.html` — repo URL, deploy steps, config changes
- `API_TESTER.html` — new endpoints must have interactive forms + Send buttons

## 4. Markdown Guides
- `INSTALL_GUIDE.md` — repo URL, deploy steps, config changes
- `API_INTEGRATION.md` — if exists, sync with TH version

## 5. Config Files
- `.env.example` — add new env vars, remove deprecated ones, update comments

## 6. Verify
// turbo
- `cd api && go build ./...`
- `cd worker && go build ./...`
- `cd mail-edge && go build ./...`

## 7. Commit
- Commit message must describe ALL changes including doc updates
