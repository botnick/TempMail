package handlers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"tempmail/shared/apiutil"
	"tempmail/shared/db"
	"tempmail/shared/logger"
	"tempmail/shared/models"
)

// Reusable HTTP clients
var rspamdHealthClient = &http.Client{Timeout: 2 * time.Second}
var webhookClient = &http.Client{Timeout: 10 * time.Second}

// Domain name validation regex
var domainNameRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*\.[a-zA-Z]{2,}$`)

// Allowed settings keys with validation
var allowedSettings = map[string]struct{}{
	"spam_reject_threshold":     {},
	"default_mailbox_ttl_hours": {},
	"default_ttl_hours":         {}, // backward-compat alias for default_mailbox_ttl_hours
	"default_message_ttl_hours": {},
	"cleanup_interval_minutes":  {},
	"max_message_size_mb":       {},
	"max_mailboxes_free":        {},
	"max_attachments":           {},
	"max_attachment_size_mb":    {},
	"allow_anonymous":           {},
	"webhook_url":               {},
	"webhook_secret":            {},
	"smtp_port":                 {},
	"dmarc_policy":              {},
	"spf_qualifier":             {},
}

// escapeLike escapes SQL LIKE wildcard characters to prevent wildcard injection
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

// ---------------------------------------------------------------------------
// POST /admin/login — ล็อกอินด้วย username + password
// ---------------------------------------------------------------------------

type AdminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func HandleAdminLogin(c *fiber.Ctx) error {
	var req AdminLoginRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request"})
	}

	expectedUser := os.Getenv("ADMIN_USERNAME")
	if expectedUser == "" {
		expectedUser = "admin"
	}
	expectedPass := os.Getenv("ADMIN_API_KEY")

	if expectedPass == "" {
		logger.Log.Error("ADMIN_API_KEY not configured")
		return apiutil.SendError(c, fiber.StatusInternalServerError, "server_error", "Admin not configured")
	}

	if req.Username != expectedUser || req.Password != expectedPass {
		logger.Log.Warn("Failed admin login attempt",
			zap.String("username", req.Username),
			zap.String("ip", c.IP()),
		)
		return apiutil.SendError(c, fiber.StatusUnauthorized, "unauthorized", "Invalid credentials")
	}

	// Generate session token (valid 24 hours)
	token, err := GenerateSessionToken(req.Username, expectedPass)
	if err != nil {
		return apiutil.SendError(c, fiber.StatusInternalServerError, "server_error", "Token generation failed")
	}

	logger.Log.Info("Admin login successful",
		zap.String("username", req.Username),
		zap.String("ip", c.IP()),
	)

	return c.JSON(fiber.Map{
		"token":     token,
		"username":  req.Username,
		"expiresIn": 86400, // 24 hours in seconds
	})
}

// GenerateSessionToken creates an HMAC-SHA256 signed token
// Format: base64(username:expiry_unix).signature
func GenerateSessionToken(username string, secret string) (string, error) {
	expiry := time.Now().Add(24 * time.Hour).Unix()
	payload := fmt.Sprintf("%s:%d", username, expiry)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(payload))

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded))
	sig := fmt.Sprintf("%x", mac.Sum(nil))

	return encoded + "." + sig, nil
}

// ValidateSessionToken checks the HMAC signature and expiry
func ValidateSessionToken(token string, secret string) (string, bool) {
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return "", false
	}

	encoded, sig := parts[0], parts[1]

	// Verify HMAC
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(encoded))
	expectedSig := fmt.Sprintf("%x", mac.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(expectedSig)) {
		return "", false
	}

	// Decode payload
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", false
	}

	// Parse username:expiry
	payloadParts := strings.SplitN(string(payload), ":", 2)
	if len(payloadParts) != 2 {
		return "", false
	}

	username := payloadParts[0]
	var expiry int64
	fmt.Sscanf(payloadParts[1], "%d", &expiry)

	if time.Now().Unix() > expiry {
		return "", false // Token expired
	}

	return username, true
}

// ---------------------------------------------------------------------------
// GET /admin/dashboard — ภาพรวมระบบ
// ---------------------------------------------------------------------------

var serverStartTime = time.Now()

func HandleAdminDashboard(c *fiber.Ctx) error {
	var totalDomains, totalMailboxes, totalMessages, totalSpamBlocked int64

	db.DB.Model(&models.Domain{}).Where("status = ?", "ACTIVE").Count(&totalDomains)
	db.DB.Model(&models.Mailbox{}).Where("status = ?", "ACTIVE").Count(&totalMailboxes)
	db.DB.Model(&models.Message{}).Count(&totalMessages)
	db.DB.Model(&models.Message{}).Where("quarantine_action != ?", "ACCEPT").Count(&totalSpamBlocked)

	// Messages received today
	var todayMessages int64
	today := time.Now().Truncate(24 * time.Hour)
	db.DB.Model(&models.Message{}).Where("received_at >= ?", today).Count(&todayMessages)

	// Active redis mailboxes count
	redisCount, _ := db.Redis.SCard(context.Background(), "system:active_mailboxes").Result()

	// -----------------------------------------------------------------------
	// Detailed Service Status Checks
	// -----------------------------------------------------------------------
	type serviceInfo struct {
		Status  string `json:"status"`
		Latency string `json:"latency"`
		Detail  string `json:"detail"`
	}
	services := map[string]serviceInfo{
		"database":   {Status: "OFFLINE"},
		"redis":      {Status: "OFFLINE"},
		"rspamd":     {Status: "OFFLINE"},
		"worker":     {Status: "ONLINE", Detail: "background"},
		"mailserver": {Status: "ONLINE", Detail: "this instance"},
	}

	// 1. PostgreSQL — ping + pool stats
	if sqlDB, err := db.DB.DB(); err == nil {
		start := time.Now()
		if err := sqlDB.Ping(); err == nil {
			lat := time.Since(start)
			stats := sqlDB.Stats()
			services["database"] = serviceInfo{
				Status:  "ONLINE",
				Latency: lat.Round(time.Microsecond).String(),
				Detail:  fmt.Sprintf("open:%d idle:%d inuse:%d max:%d", stats.OpenConnections, stats.Idle, stats.InUse, stats.MaxOpenConnections),
			}
		}
	}

	// 2. Redis — ping + pool stats
	{
		start := time.Now()
		if err := db.Redis.Ping(context.Background()).Err(); err == nil {
			lat := time.Since(start)
			pool := db.Redis.PoolStats()
			services["redis"] = serviceInfo{
				Status:  "ONLINE",
				Latency: lat.Round(time.Microsecond).String(),
				Detail:  fmt.Sprintf("conns:%d idle:%d hits:%d misses:%d", pool.TotalConns, pool.IdleConns, pool.Hits, pool.Misses),
			}
		}
	}

	// 3. Rspamd ping
	{
		start := time.Now()
		rspamdURL := "http://rspamd:11334/ping"
		if resp, err := rspamdHealthClient.Get(rspamdURL); err == nil {
			defer resp.Body.Close()
			lat := time.Since(start)
			if resp.StatusCode == http.StatusOK {
				services["rspamd"] = serviceInfo{
					Status: "ONLINE", Latency: lat.Round(time.Microsecond).String(), Detail: "spam filter",
				}
			}
		}
	}

	// -----------------------------------------------------------------------
	// Go Runtime Info
	// -----------------------------------------------------------------------
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	uptime := time.Since(serverStartTime)

	runtimeInfo := fiber.Map{
		"goroutines": runtime.NumGoroutine(),
		"goVersion":  runtime.Version(),
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"cpus":       runtime.NumCPU(),
		"allocMB":    fmt.Sprintf("%.1f", float64(mem.Alloc)/1024/1024),
		"sysMB":      fmt.Sprintf("%.1f", float64(mem.Sys)/1024/1024),
		"gcCycles":   mem.NumGC,
		"uptimeStr":  formatDuration(uptime),
		"uptimeSec":  int(uptime.Seconds()),
	}

	return c.JSON(fiber.Map{
		"totalDomains":         totalDomains,
		"totalMailboxes":       totalMailboxes,
		"totalMessages":        totalMessages,
		"totalSpamBlocked":     totalSpamBlocked,
		"messagesToday":        todayMessages,
		"redisActiveMailboxes": redisCount,
		"services":             services,
		"runtime":              runtimeInfo,
		"serverTime":           time.Now().UTC(),
	})
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}

// ---------------------------------------------------------------------------
// GET /admin/domains — รายการ domain ทั้งหมด
// ---------------------------------------------------------------------------

func HandleAdminDomains(c *fiber.Ctx) error {
	search := c.Query("search", "")
	status := c.Query("status", "")
	limit := c.QueryInt("limit", 50)
	offset := c.QueryInt("offset", 0)

	query := db.DB.Model(&models.Domain{}).Preload("Node")

	if status != "" {
		query = query.Where("domains.status = ?", status)
	}

	if search != "" {
		like := "%" + escapeLike(search) + "%"
		// Search by domain_name OR node name OR node ip
		query = query.Joins("LEFT JOIN mail_nodes ON domains.node_id = mail_nodes.id").
			Where("domains.domain_name ILIKE ? OR mail_nodes.name ILIKE ? OR mail_nodes.ip_address ILIKE ?", like, like, like)
	}

	var total int64
	query.Count(&total)

	var domains []models.Domain
	query.Order("domains.created_at DESC").Limit(limit).Offset(offset).Find(&domains)

	return c.JSON(fiber.Map{"domains": domains, "count": total})
}

// ---------------------------------------------------------------------------
// POST /admin/domains — เพิ่ม domain ใหม่
// ---------------------------------------------------------------------------

type CreateDomainRequest struct {
	DomainName string  `json:"domainName"`
	TenantID   *string `json:"tenantId"`
	NodeID     *string `json:"nodeId"`
}

func HandleAdminCreateDomain(c *fiber.Ctx) error {
	var req CreateDomainRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request"})
	}

	if req.DomainName == "" {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "domainName is required")
	}

	// Validate domain name format (M-5)
	if !domainNameRegex.MatchString(req.DomainName) {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_domain", "Invalid domain name format")
	}

	// Check if domain already exists
	var existing models.Domain
	if err := db.DB.Where("domain_name = ?", req.DomainName).First(&existing).Error; err == nil {
		// If domain was DELETED, re-activate it instead of blocking
		if existing.Status == "DELETED" {
			existing.Status = "ACTIVE"
			existing.NodeID = req.NodeID
			existing.TenantID = req.TenantID
			db.DB.Save(&existing)
			logger.Log.Info("Re-activated deleted domain", zap.String("domain", req.DomainName))
			writeAuditLog("domain.reactivate", existing.ID, c)
			db.DB.Preload("Node").First(&existing, "id = ?", existing.ID)
			return c.Status(fiber.StatusCreated).JSON(fiber.Map{
				"domain":      existing,
				"reactivated": true,
			})
		}
		return apiutil.SendError(c, fiber.StatusConflict, "domain_exists", "Domain already exists")
	}

	// Validate node if specified
	var nodeIP string
	if req.NodeID != nil && *req.NodeID != "" {
		var node models.MailNode
		if err := db.DB.First(&node, "id = ?", *req.NodeID).Error; err != nil {
			return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_node", "Node not found")
		}
		nodeIP = node.IPAddress
	}

	domain := models.Domain{
		ID:         uuid.New().String(),
		DomainName: req.DomainName,
		TenantID:   req.TenantID,
		NodeID:     req.NodeID,
		Status:     "ACTIVE",
	}

	if err := db.DB.Create(&domain).Error; err != nil {
		logger.Log.Error("Failed to create domain", zap.Error(err))
		return apiutil.SendError(c, fiber.StatusInternalServerError, "database_error", "Database error")
	}

	logger.Log.Info("Domain created via admin", zap.String("domain", req.DomainName))

	// Build DNS instructions
	dnsInstructions := []fiber.Map{}
	if nodeIP != "" {
		dnsInstructions = append(dnsInstructions,
			fiber.Map{"type": "MX", "name": req.DomainName, "value": "mail." + req.DomainName, "priority": 10, "proxy": false, "note": "Mail exchange record — points to your mail server"},
			fiber.Map{"type": "A", "name": "mail." + req.DomainName, "value": nodeIP, "proxy": false, "note": "A record — must point to the node IP, proxy OFF (DNS only)"},
		)
	}

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"domain": domain,
		"dns":    dnsInstructions,
		"nodeIp": nodeIP,
	})
}

// ---------------------------------------------------------------------------
// DELETE /admin/domains/:id — ลบ domain
// ---------------------------------------------------------------------------

func HandleAdminDeleteDomain(c *fiber.Ctx) error {
	domainID := c.Params("id")

	var domain models.Domain
	if err := db.DB.First(&domain, "id = ?", domainID).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "domain_not_found", "Domain not found")
	}

	// Write audit BEFORE hard delete so we keep a record
	writeAuditLog("domain.delete", domainID+" ("+domain.DomainName+")", c)

	// Hard delete: cascade — remove mailboxes from Redis + DB, then domain
	var mailboxes []models.Mailbox
	db.DB.Preload("Domain").Where("domain_id = ?", domainID).Find(&mailboxes)
	for _, mb := range mailboxes {
		fullAddr := mb.LocalPart + "@" + mb.Domain.DomainName
		db.Redis.SRem(context.Background(), "system:active_mailboxes", fullAddr)
		// Delete messages + attachments for this mailbox
		db.DB.Where("mailbox_id = ?", mb.ID).Delete(&models.Attachment{})
		db.DB.Where("mailbox_id = ?", mb.ID).Delete(&models.Message{})
	}
	db.DB.Where("domain_id = ?", domainID).Delete(&models.Mailbox{})
	db.DB.Delete(&domain)

	logger.Log.Info("Domain hard-deleted via admin",
		zap.String("id", domainID),
		zap.String("domain", domain.DomainName),
		zap.Int("mailboxes_removed", len(mailboxes)),
	)

	return c.JSON(fiber.Map{"status": "deleted", "id": domainID, "domain": domain.DomainName})
}

// ---------------------------------------------------------------------------
// GET /admin/mailboxes — รายการ mailbox ทั้งหมด (pagination + search)
// ---------------------------------------------------------------------------

func HandleAdminMailboxes(c *fiber.Ctx) error {
	search := c.Query("search", "")
	status := c.Query("status", "ACTIVE")
	limit := c.QueryInt("limit", 50)
	offset := c.QueryInt("offset", 0)

	query := db.DB.Model(&models.Mailbox{}).Preload("Domain")

	if status != "" {
		query = query.Where("mailboxes.status = ?", status)
	}
	if search != "" {
		like := "%" + escapeLike(search) + "%"
		query = query.Joins("LEFT JOIN domains ON domains.id = mailboxes.domain_id").
			Where("mailboxes.local_part ILIKE ? OR domains.domain_name ILIKE ? OR CONCAT(mailboxes.local_part, '@', domains.domain_name) ILIKE ?", like, like, like)
	}

	var total int64
	query.Count(&total)

	var mailboxes []models.Mailbox
	query.Order("mailboxes.created_at DESC").Limit(limit).Offset(offset).Find(&mailboxes)

	return c.JSON(fiber.Map{
		"total":     total,
		"mailboxes": mailboxes,
	})
}

// ---------------------------------------------------------------------------
// DELETE /admin/mailboxes/:id — ลบ mailbox
// ---------------------------------------------------------------------------

func HandleAdminDeleteMailbox(c *fiber.Ctx) error {
	mailboxID := c.Params("id")

	var mailbox models.Mailbox
	if err := db.DB.Preload("Domain").First(&mailbox, "id = ?", mailboxID).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "mailbox_not_found", "Mailbox not found")
	}

	// Audit before hard delete
	fullAddress := mailbox.LocalPart + "@" + mailbox.Domain.DomainName
	writeAuditLog("mailbox.delete", mailboxID+" ("+fullAddress+")", c)

	// Remove from Redis
	db.Redis.SRem(context.Background(), "system:active_mailboxes", fullAddress)

	// Hard delete: cascade messages + attachments
	db.DB.Where("mailbox_id = ?", mailboxID).Delete(&models.Attachment{})
	db.DB.Where("mailbox_id = ?", mailboxID).Delete(&models.Message{})
	db.DB.Delete(&mailbox)

	logger.Log.Info("Mailbox hard-deleted via admin", zap.String("id", mailboxID), zap.String("address", fullAddress))

	return c.JSON(fiber.Map{"status": "deleted", "id": mailboxID, "address": fullAddress})
}

// ---------------------------------------------------------------------------
// GET /admin/messages — ค้นหา messages (pagination)
// ---------------------------------------------------------------------------

func HandleAdminMessages(c *fiber.Ctx) error {
	search := c.Query("search", "")
	mailboxID := c.Query("mailbox_id", "")
	mailboxStatus := c.Query("mailbox_status", "") // ACTIVE, EXPIRED, ORPHANED
	limit := c.QueryInt("limit", 50)
	offset := c.QueryInt("offset", 0)

	if limit > 200 {
		limit = 200
	}

	baseJoin := "LEFT JOIN mailboxes ON mailboxes.id = messages.mailbox_id"
	where := "1=1"
	args := []interface{}{}

	if mailboxID != "" {
		where += " AND messages.mailbox_id = ?"
		args = append(args, mailboxID)
	}
	if search != "" {
		like := "%" + escapeLike(search) + "%"
		where += " AND (messages.from_address ILIKE ? OR messages.to_address ILIKE ? OR messages.subject ILIKE ?)"
		args = append(args, like, like, like)
	}
	switch mailboxStatus {
	case "ORPHANED":
		where += " AND messages.mailbox_id NOT IN (SELECT id FROM mailboxes)"
	case "ACTIVE", "EXPIRED":
		where += " AND mailboxes.status = ?"
		args = append(args, mailboxStatus)
	}

	// --- Count ---
	var total int64
	db.DB.Table("messages").
		Joins(baseJoin).
		Where(where, args...).
		Count(&total)

	// --- Fetch ---
	type msgRow struct {
		ID               string    `json:"id"`
		MailboxID        string    `json:"mailboxId"`
		FromAddress      string    `json:"fromAddress"`
		ToAddress        string    `json:"toAddress"`
		Subject          string    `json:"subject"`
		SpamScore        float64   `json:"spamScore"`
		QuarantineAction string    `json:"quarantineAction"`
		ExpiresAt        time.Time `json:"expiresAt"`
		ReceivedAt       time.Time `json:"receivedAt"`
		S3KeyRaw         string    `json:"s3KeyRaw"`
		MailboxAddress   string    `json:"mailboxAddress"`
		MailboxStatus    string    `json:"mailboxStatus"`
	}

	rows := make([]msgRow, 0)
	db.DB.Table("messages").
		Select(`messages.id, messages.mailbox_id, messages.from_address, messages.to_address,
			messages.subject, messages.spam_score, messages.quarantine_action,
			messages.expires_at, messages.received_at, messages.s3_key_raw,
			COALESCE(mailboxes.local_part || '@' || domains.domain_name, '') AS mailbox_address,
			COALESCE(mailboxes.status, 'ORPHANED') AS mailbox_status`).
		Joins(baseJoin).
		Joins("LEFT JOIN domains ON domains.id = mailboxes.domain_id").
		Where(where, args...).
		Order("messages.received_at DESC").
		Limit(limit).Offset(offset).
		Scan(&rows)

	return c.JSON(fiber.Map{
		"total":    total,
		"messages": rows,
	})
}

// ---------------------------------------------------------------------------
// GET /admin/audit-log — ดู audit log
// ---------------------------------------------------------------------------

func HandleAdminAuditLog(c *fiber.Ctx) error {
	limit := c.QueryInt("limit", 100)
	offset := c.QueryInt("offset", 0)
	search := c.Query("search", "")
	action := c.Query("action", "")

	query := db.DB.Model(&models.AuditLog{})

	// Free text search across multiple fields
	if search != "" {
		like := "%" + escapeLike(search) + "%"
		query = query.Where("action LIKE ? OR target_id LIKE ? OR user_id LIKE ? OR ip_address LIKE ?", like, like, like, like)
	}
	// Filter by specific action type
	if action != "" {
		query = query.Where("action = ?", action)
	}

	var total int64
	query.Count(&total)

	var logs []models.AuditLog
	query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&logs)

	// Get unique action types for dynamic grouping (no hardcode)
	var actions []string
	db.DB.Model(&models.AuditLog{}).Distinct("action").Pluck("action", &actions)

	return c.JSON(fiber.Map{
		"logs":    logs,
		"total":   total,
		"actions": actions,
	})
}

// ---------------------------------------------------------------------------
// GET /admin/settings — ดูค่าตั้งต่าง ๆ ของระบบ (จาก Redis hash)
// ---------------------------------------------------------------------------

func HandleAdminGetSettings(c *fiber.Ctx) error {
	ctx := context.Background()
	settings, _ := db.Redis.HGetAll(ctx, "system:settings").Result()

	// Provide sensible defaults
	defaults := map[string]string{
		"spam_reject_threshold":     "15",
		"default_mailbox_ttl_hours": "24",
		"default_message_ttl_hours": "24",
		"cleanup_interval_minutes":  "5",
		"max_message_size_mb":       "25",
		"max_mailboxes_free":        "5",
		"max_attachments":           "10",
		"max_attachment_size_mb":    "10",
		"allow_anonymous":           "true",
		"webhook_url":               "",
		"webhook_secret":            "",
		"smtp_port":                 "25",
		"dmarc_policy":              "none",
		"spf_qualifier":             "~all",
	}
	for k, v := range defaults {
		if _, exists := settings[k]; !exists {
			settings[k] = v
		}
	}

	return c.JSON(fiber.Map{"settings": settings})
}

// ---------------------------------------------------------------------------
// POST /admin/settings — อัปเดตค่าตั้ง
// ---------------------------------------------------------------------------

func HandleAdminUpdateSettings(c *fiber.Ctx) error {
	var body map[string]string
	if err := c.BodyParser(&body); err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "Invalid request body")
	}

	// C-2: Validate against allowlist — reject unknown keys
	for k := range body {
		if _, ok := allowedSettings[k]; !ok {
			return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_setting",
				fmt.Sprintf("Unknown setting key: %s", k))
		}
	}

	// Validate numeric settings have sane values
	numericLimits := map[string][2]int{
		"spam_reject_threshold":     {1, 100},
		"default_mailbox_ttl_hours": {1, 8760},
		"default_ttl_hours":         {1, 8760}, // backward-compat alias
		"default_message_ttl_hours": {1, 8760},
		"cleanup_interval_minutes":  {1, 1440},
		"max_message_size_mb":       {1, 100},
		"max_mailboxes_free":        {1, 10000000},
		"max_attachments":           {1, 50},
		"max_attachment_size_mb":    {1, 100},
	}
	for k, v := range body {
		if limits, ok := numericLimits[k]; ok {
			n, err := strconv.Atoi(v)
			if err != nil || n < limits[0] || n > limits[1] {
				return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_value",
					fmt.Sprintf("%s must be a number between %d and %d", k, limits[0], limits[1]))
			}
		}
	}

	ctx := context.Background()
	for k, v := range body {
		db.Redis.HSet(ctx, "system:settings", k, v)
	}

	logger.Log.Info("System settings updated via admin", zap.Int("fields", len(body)))
	writeAuditLog("settings.update", fmt.Sprintf("keys=%d", len(body)), c)

	return c.JSON(fiber.Map{"status": "updated", "count": len(body)})
}

// ---------------------------------------------------------------------------
// GET /admin/domains/dns-check?domain=example.com — ตรวจสอบ DNS records
// ---------------------------------------------------------------------------

type dnsRecord struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Value  string `json:"value"`
	Status string `json:"status"` // OK, WARN, FAIL
}

func HandleDNSCheck(c *fiber.Ctx) error {
	domain := c.Query("domain")
	if domain == "" {
		return apiutil.SendError(c, fiber.StatusBadRequest, "missing_domain", "Domain parameter is required")
	}

	var records []dnsRecord
	allOk := true

	// 1. MX Records
	mxRecords, err := net.LookupMX(domain)
	if err != nil || len(mxRecords) == 0 {
		records = append(records, dnsRecord{
			Type: "MX", Name: domain, Value: "No MX records found", Status: "FAIL",
		})
		allOk = false
	} else {
		for _, mx := range mxRecords {
			records = append(records, dnsRecord{
				Type: "MX", Name: domain,
				Value:  fmt.Sprintf("%s (priority %d)", strings.TrimSuffix(mx.Host, "."), mx.Pref),
				Status: "OK",
			})
		}
	}

	// 2. A Records for the MX target (or the domain itself)
	lookupHost := domain
	if len(mxRecords) > 0 {
		lookupHost = strings.TrimSuffix(mxRecords[0].Host, ".")
	}
	addrs, err := net.LookupHost(lookupHost)
	if err != nil || len(addrs) == 0 {
		records = append(records, dnsRecord{
			Type: "A", Name: lookupHost, Value: "No A record found", Status: "FAIL",
		})
		allOk = false
	} else {
		for _, a := range addrs {
			records = append(records, dnsRecord{
				Type: "A", Name: lookupHost, Value: a, Status: "OK",
			})
		}
	}

	// 3. SPF (TXT) — optional but recommended
	txtRecords, _ := net.LookupTXT(domain)
	hasSPF := false
	for _, txt := range txtRecords {
		if strings.HasPrefix(txt, "v=spf1") {
			hasSPF = true
			records = append(records, dnsRecord{
				Type: "SPF", Name: domain, Value: txt, Status: "OK",
			})
		}
	}
	if !hasSPF {
		records = append(records, dnsRecord{
			Type: "SPF", Name: domain, Value: "No SPF record found", Status: "WARN",
		})
	}

	// 4. DMARC (TXT at _dmarc.domain)
	dmarcRecords, _ := net.LookupTXT("_dmarc." + domain)
	hasDMARC := false
	for _, txt := range dmarcRecords {
		if strings.HasPrefix(txt, "v=DMARC1") {
			hasDMARC = true
			records = append(records, dnsRecord{
				Type: "DMARC", Name: "_dmarc." + domain, Value: txt, Status: "OK",
			})
		}
	}
	if !hasDMARC {
		records = append(records, dnsRecord{
			Type: "DMARC", Name: "_dmarc." + domain, Value: "No DMARC record found", Status: "WARN",
		})
	}

	summary := "All critical DNS records are configured correctly."
	if !allOk {
		summary = "Some DNS records are missing or misconfigured. Mail delivery may not work correctly."
	} else if !hasSPF || !hasDMARC {
		summary = "Core records are OK but SPF/DMARC are recommended for deliverability."
	}

	return c.JSON(fiber.Map{
		"records": records,
		"allOk":   allOk,
		"summary": summary,
	})
}

// ---------------------------------------------------------------------------
// PUBLIC: GET /api/server-info — DNS setup info for Web App
// ---------------------------------------------------------------------------

// HandleServerInfo returns server node info and DNS setup instructions.
// Public endpoint — no auth required.
func HandleServerInfo(c *fiber.Ctx) error {
	// Load configurable settings from Redis
	ctx := context.Background()
	settings, _ := db.Redis.HGetAll(ctx, "system:settings").Result()

	smtpPort := 25
	if v, ok := settings["smtp_port"]; ok {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p <= 65535 {
			smtpPort = p
		}
	}

	dmarcPolicy := "none"
	if v, ok := settings["dmarc_policy"]; ok && v != "" {
		dmarcPolicy = v
	}

	spfQualifier := "~all"
	if v, ok := settings["spf_qualifier"]; ok && v != "" {
		spfQualifier = v
	}

	// Fetch active nodes
	var nodes []models.MailNode
	if err := db.DB.Where("status = ?", "ACTIVE").Order("created_at ASC").Find(&nodes).Error; err != nil {
		logger.Log.Error("Failed to fetch nodes for server-info", zap.Error(err))
		return apiutil.SendError(c, fiber.StatusInternalServerError, "database_error", "Failed to load server info")
	}

	if len(nodes) == 0 {
		return c.JSON(fiber.Map{
			"hostname":       "",
			"ip":             "",
			"smtp_port":      smtpPort,
			"setup_complete": false,
			"dns_records":    fiber.Map{},
			"nodes":          []fiber.Map{},
		})
	}

	// Primary node = first active node
	primary := nodes[0]
	hostname := primary.Hostname
	if hostname == "" {
		hostname = primary.IPAddress // fallback for display, but MX won't use IP
	}

	// Check if all nodes have hostnames configured
	allHostnamesSet := true
	for _, n := range nodes {
		if n.Hostname == "" {
			allHostnamesSet = false
			break
		}
	}

	// Build nodes array
	nodeList := make([]fiber.Map, 0, len(nodes))
	for _, n := range nodes {
		h := n.Hostname
		if h == "" {
			h = n.IPAddress
		}
		nodeList = append(nodeList, fiber.Map{
			"id":                 n.ID,
			"name":               n.Name,
			"hostname":           h,
			"ip":                 n.IPAddress,
			"region":             n.Region,
			"active":             n.Status == "ACTIVE",
			"hostname_configured": n.Hostname != "",
		})
	}

	// Build MX records — only for nodes that have hostname set (RFC: MX must point to domain, not IP)
	mxRecords := make([]fiber.Map, 0, len(nodes))
	for i, n := range nodes {
		if n.Hostname == "" {
			continue // Skip nodes without hostname — MX cannot point to IP
		}
		mxRecords = append(mxRecords, fiber.Map{
			"type":     "MX",
			"name":     "@",
			"value":    n.Hostname,
			"priority": (i + 1) * 10,
		})
	}

	// Build A records — hostname → IP mapping (needed for MX to resolve)
	aRecords := make([]fiber.Map, 0, len(nodes))
	for _, n := range nodes {
		if n.Hostname == "" {
			continue // No A record needed if hostname not set
		}
		aRecords = append(aRecords, fiber.Map{
			"type":  "A",
			"name":  n.Hostname,
			"value": n.IPAddress,
		})
	}

	// Build SPF — include all node IPs
	var spfParts []string
	spfParts = append(spfParts, "v=spf1")
	for _, n := range nodes {
		spfParts = append(spfParts, "ip4:"+n.IPAddress)
	}
	spfParts = append(spfParts, spfQualifier)
	spfValue := strings.Join(spfParts, " ")

	dnsRecords := fiber.Map{
		"mx": mxRecords,
		"a":  aRecords,
		"spf": fiber.Map{
			"type":  "TXT",
			"name":  "@",
			"value": spfValue,
		},
		"dmarc": fiber.Map{
			"type":  "TXT",
			"name":  "_dmarc",
			"value": fmt.Sprintf("v=DMARC1; p=%s;", dmarcPolicy),
		},
	}

	return c.JSON(fiber.Map{
		"hostname":       hostname,
		"ip":             primary.IPAddress,
		"smtp_port":      smtpPort,
		"setup_complete": allHostnamesSet,
		"dns_records":    dnsRecords,
		"nodes":          nodeList,
	})
}

// HandleDetectHostname does reverse DNS (PTR) lookup on a node's IP.
// Used by the Admin Panel "Scan" button to auto-detect hostname.
// POST /admin/nodes/:id/detect-hostname
func HandleDetectHostname(c *fiber.Ctx) error {
	id := c.Params("id")

	var node models.MailNode
	if err := db.DB.Where("id = ?", id).First(&node).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "node_not_found", "Node not found")
	}

	if node.IPAddress == "" {
		return apiutil.SendError(c, fiber.StatusBadRequest, "no_ip", "Node has no IP address")
	}

	// Reverse DNS lookup (PTR record)
	hosts, err := net.LookupAddr(node.IPAddress)
	detectedHostname := ""
	if err == nil && len(hosts) > 0 {
		// PTR records end with a dot — trim it
		h := strings.TrimSuffix(hosts[0], ".")
		// Skip generic cloud hostnames
		if !strings.Contains(h, "compute") && !strings.Contains(h, "bc.googleusercontent") {
			detectedHostname = h
		}
	}

	// If apply=true in body, save to DB
	type detectReq struct {
		Apply bool `json:"apply"`
	}
	var req detectReq
	_ = c.BodyParser(&req)

	if req.Apply && detectedHostname != "" {
		node.Hostname = detectedHostname
		if err := db.DB.Save(&node).Error; err != nil {
			return apiutil.SendError(c, fiber.StatusInternalServerError, "database_error", "Failed to update node")
		}
		writeAuditLog("node.hostname_detected", node.ID+" ("+detectedHostname+")", c)
	}

	return c.JSON(fiber.Map{
		"detected":  detectedHostname != "",
		"hostname":  detectedHostname,
		"ip":        node.IPAddress,
		"applied":   req.Apply && detectedHostname != "",
		"all_hosts": hosts, // raw PTR results for debugging
	})
}

// ---------------------------------------------------------------------------
// NODE MANAGEMENT
// ---------------------------------------------------------------------------

// GET /admin/nodes — รายการ node ทั้งหมด
func HandleAdminNodes(c *fiber.Ctx) error {
	search := c.Query("search", "")
	status := c.Query("status", "")
	limit := c.QueryInt("limit", 50)
	offset := c.QueryInt("offset", 0)

	query := db.DB.Model(&models.MailNode{}).Preload("Domains")

	if status != "" {
		query = query.Where("status = ?", status)
	}
	if search != "" {
		like := "%" + escapeLike(search) + "%"
		query = query.Where("name ILIKE ? OR ip_address ILIKE ? OR region ILIKE ?", like, like, like)
	}

	var total int64
	query.Count(&total)

	var nodes []models.MailNode
	query.Order("created_at ASC").Limit(limit).Offset(offset).Find(&nodes)

	return c.JSON(fiber.Map{"nodes": nodes, "count": total})
}

// POST /admin/nodes — เพิ่ม node ใหม่
type CreateNodeRequest struct {
	Name      string `json:"name"`
	Hostname  string `json:"hostname"`
	IPAddress string `json:"ipAddress"`
	Region    string `json:"region"`
}

func HandleAdminCreateNode(c *fiber.Ctx) error {
	var req CreateNodeRequest
	if err := c.BodyParser(&req); err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "Invalid request body")
	}
	if req.Name == "" || req.IPAddress == "" {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "name and ipAddress are required")
	}

	node := models.MailNode{
		ID:        uuid.New().String(),
		Name:      req.Name,
		Hostname:  req.Hostname,
		IPAddress: req.IPAddress,
		Region:    req.Region,
		Status:    "ACTIVE",
	}

	if err := db.DB.Create(&node).Error; err != nil {
		logger.Log.Error("Failed to create node", zap.Error(err))
		return apiutil.SendError(c, fiber.StatusInternalServerError, "database_error", "Database error")
	}

	logger.Log.Info("Node created", zap.String("name", req.Name), zap.String("ip", req.IPAddress))
	return c.Status(fiber.StatusCreated).JSON(node)
}

// DELETE /admin/nodes/:id — ลบ node
func HandleAdminDeleteNode(c *fiber.Ctx) error {
	nodeID := c.Params("id")

	var node models.MailNode
	if err := db.DB.First(&node, "id = ?", nodeID).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "node_not_found", "Node not found")
	}

	// Check if any domains are assigned to this node
	var domainCount int64
	db.DB.Model(&models.Domain{}).Where("node_id = ?", nodeID).Count(&domainCount)
	if domainCount > 0 {
		return apiutil.SendError(c, fiber.StatusConflict, "node_in_use",
			fmt.Sprintf("Cannot delete node: %d domain(s) still assigned", domainCount))
	}

	writeAuditLog("node.delete", nodeID+" ("+node.Name+")", c)
	db.DB.Delete(&node)
	logger.Log.Info("Node deleted", zap.String("name", node.Name))
	return c.JSON(fiber.Map{"status": "deleted"})
}

// ===========================================================================
// DOMAIN FILTER (Blocklist / Whitelist)
// ===========================================================================

// GET /admin/filters — list all domain filters
func HandleAdminFilters(c *fiber.Ctx) error {
	search := c.Query("search", "")
	filterType := c.Query("type", "")
	limit := c.QueryInt("limit", 50)
	offset := c.QueryInt("offset", 0)

	query := db.DB.Model(&models.DomainFilter{})

	if filterType != "" {
		query = query.Where("filter_type = ?", filterType)
	}
	if search != "" {
		like := "%" + escapeLike(search) + "%"
		query = query.Where("pattern ILIKE ? OR reason ILIKE ?", like, like)
	}

	var total int64
	query.Count(&total)

	var filters []models.DomainFilter
	query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&filters)

	return c.JSON(fiber.Map{"filters": filters, "count": total})
}

// POST /admin/filters — add a filter
func HandleAdminCreateFilter(c *fiber.Ctx) error {
	var req struct {
		Pattern    string `json:"pattern"`
		FilterType string `json:"filterType"` // BLOCK or ALLOW
		Reason     string `json:"reason"`
	}
	if err := c.BodyParser(&req); err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "Invalid request body")
	}
	if req.Pattern == "" || (req.FilterType != "BLOCK" && req.FilterType != "ALLOW") {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "pattern and filterType (BLOCK/ALLOW) are required")
	}

	filter := models.DomainFilter{
		ID:         uuid.New().String(),
		Pattern:    strings.ToLower(req.Pattern),
		FilterType: req.FilterType,
		Reason:     req.Reason,
	}
	if err := db.DB.Create(&filter).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusConflict, "filter_exists", "Filter pattern already exists")
	}

	// Sync to Redis for fast lookup by mail-edge
	syncFiltersToRedis()

	logger.Log.Info("Domain filter created", zap.String("pattern", req.Pattern), zap.String("type", req.FilterType))
	writeAuditLog("filter.create", filter.ID, c)
	return c.Status(fiber.StatusCreated).JSON(filter)
}

// DELETE /admin/filters/:id
func HandleAdminDeleteFilter(c *fiber.Ctx) error {
	id := c.Params("id")
	if err := db.DB.Delete(&models.DomainFilter{}, "id = ?", id).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "filter_not_found", "Filter not found")
	}
	syncFiltersToRedis()
	writeAuditLog("filter.delete", id, c)
	return c.JSON(fiber.Map{"status": "deleted"})
}

// syncFiltersToRedis pushes all filters to Redis sets for O(1) lookup during mail ingest
func syncFiltersToRedis() {
	ctx := context.Background()
	var filters []models.DomainFilter
	db.DB.Find(&filters)

	// H-3: Use pipeline to avoid race condition between Del and SAdd
	pipe := db.Redis.Pipeline()
	pipe.Del(ctx, "system:domain_blocklist", "system:domain_allowlist")
	for _, f := range filters {
		if f.FilterType == "BLOCK" {
			pipe.SAdd(ctx, "system:domain_blocklist", f.Pattern)
		} else {
			pipe.SAdd(ctx, "system:domain_allowlist", f.Pattern)
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		logger.Log.Error("Failed to sync filters to Redis", zap.Error(err))
	}
}

// ===========================================================================
// EMAIL PREVIEW
// ===========================================================================

// GET /admin/messages/:id — view full message content
func HandleAdminMessageDetail(c *fiber.Ctx) error {
	msgID := c.Params("id")
	var msg models.Message
	if err := db.DB.Preload("Attachments").First(&msg, "id = ?", msgID).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "message_not_found", "Message not found")
	}
	return c.JSON(msg)
}

// ===========================================================================
// EXPORT / IMPORT CONFIG
// ===========================================================================

// GET /admin/export — export full system configuration as JSON
func HandleAdminExport(c *fiber.Ctx) error {
	var domains []models.Domain
	db.DB.Preload("Node").Find(&domains)

	var nodes []models.MailNode
	db.DB.Find(&nodes)

	var filters []models.DomainFilter
	db.DB.Find(&filters)

	// Settings from Redis
	ctx := context.Background()
	settings, _ := db.Redis.HGetAll(ctx, "system:settings").Result()

	export := fiber.Map{
		"exportedAt": time.Now().UTC(),
		"version":    "1.0",
		"domains":    domains,
		"nodes":      nodes,
		"filters":    filters,
		"settings":   settings,
	}

	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=tempmail-config-%s.json", time.Now().Format("2006-01-02")))
	return c.JSON(export)
}

// POST /admin/import — import system configuration from JSON
func HandleAdminImport(c *fiber.Ctx) error {
	var data struct {
		Settings map[string]string     `json:"settings"`
		Filters  []models.DomainFilter `json:"filters"`
	}
	if err := json.Unmarshal(c.Body(), &data); err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_json", "Invalid JSON format")
	}

	// H-5: Cap imported items
	if len(data.Filters) > 1000 {
		return apiutil.SendError(c, fiber.StatusBadRequest, "too_many_filters", "Maximum 1000 filters per import")
	}
	if len(data.Settings) > 50 {
		return apiutil.SendError(c, fiber.StatusBadRequest, "too_many_settings", "Maximum 50 settings per import")
	}

	imported := 0
	ctx := context.Background()

	// Import settings (only allowed keys)
	if data.Settings != nil {
		for k, v := range data.Settings {
			if _, ok := allowedSettings[k]; ok {
				db.Redis.HSet(ctx, "system:settings", k, v)
				imported++
			}
		}
	}

	// Import filters (skip existing)
	for _, f := range data.Filters {
		f.ID = uuid.New().String()
		if err := db.DB.Create(&f).Error; err == nil {
			imported++
		}
	}
	syncFiltersToRedis()

	logger.Log.Info("Config imported", zap.Int("items", imported))
	writeAuditLog("config.import", fmt.Sprintf("items=%d", imported), c)
	return c.JSON(fiber.Map{"status": "imported", "count": imported})
}

// ===========================================================================
// SYSTEM METRICS
// ===========================================================================

// GET /admin/metrics — system throughput and resource usage
func HandleAdminMetrics(c *fiber.Ctx) error {
	ctx := context.Background()

	// Messages received in last 1h, 24h
	var msgLastHour, msgLast24h int64
	oneHourAgo := time.Now().Add(-1 * time.Hour)
	oneDayAgo := time.Now().Add(-24 * time.Hour)
	db.DB.Model(&models.Message{}).Where("received_at > ?", oneHourAgo).Count(&msgLastHour)
	db.DB.Model(&models.Message{}).Where("received_at > ?", oneDayAgo).Count(&msgLast24h)

	// Total storage estimate (messages + attachments count)
	var totalMessages, totalAttachments int64
	db.DB.Model(&models.Message{}).Count(&totalMessages)
	db.DB.Model(&models.Attachment{}).Count(&totalAttachments)

	// Active mailboxes
	var activeMailboxes int64
	db.DB.Model(&models.Mailbox{}).Where("status = ?", "ACTIVE").Count(&activeMailboxes)

	// Expired mailboxes pending cleanup
	var expiredMailboxes int64
	db.DB.Model(&models.Mailbox{}).Where("status = ? AND expires_at < ?", "ACTIVE", time.Now()).Count(&expiredMailboxes)

	// Redis info
	// L-4: Parse Redis info instead of exposing raw INFO string
	redisDBSize, _ := db.Redis.DBSize(ctx).Result()
	redisMemInfo, _ := db.Redis.Info(ctx, "memory").Result()
	usedMemory := "unknown"
	for _, line := range strings.Split(redisMemInfo, "\n") {
		if strings.HasPrefix(line, "used_memory_human:") {
			usedMemory = strings.TrimSpace(strings.TrimPrefix(line, "used_memory_human:"))
			break
		}
	}

	// Blocked messages (spam score > threshold)
	var blockedCount int64
	db.DB.Model(&models.Message{}).Where("quarantine_action != ?", "ACCEPT").Count(&blockedCount)

	// Domain filter counts
	var blocklistCount, allowlistCount int64
	db.DB.Model(&models.DomainFilter{}).Where("filter_type = ?", "BLOCK").Count(&blocklistCount)
	db.DB.Model(&models.DomainFilter{}).Where("filter_type = ?", "ALLOW").Count(&allowlistCount)

	return c.JSON(fiber.Map{
		"throughput": fiber.Map{
			"lastHour": msgLastHour,
			"last24h":  msgLast24h,
		},
		"storage": fiber.Map{
			"totalMessages":    totalMessages,
			"totalAttachments": totalAttachments,
		},
		"mailboxes": fiber.Map{
			"active":         activeMailboxes,
			"expiredPending": expiredMailboxes,
		},
		"spam": fiber.Map{
			"blockedMessages": blockedCount,
			"blocklistRules":  blocklistCount,
			"allowlistRules":  allowlistCount,
		},
		"redis": fiber.Map{
			"dbSize":     redisDBSize,
			"usedMemory": usedMemory,
		},
	})
}

// ===========================================================================
// API KEY MANAGEMENT
// ===========================================================================

// GET /admin/api-keys — list all API keys
func HandleAdminAPIKeys(c *fiber.Ctx) error {
	search := c.Query("search", "")
	status := c.Query("status", "")
	limit := c.QueryInt("limit", 50)
	offset := c.QueryInt("offset", 0)

	query := db.DB.Model(&models.APIKey{})

	if status != "" {
		query = query.Where("status = ?", status)
	}
	if search != "" {
		like := "%" + escapeLike(search) + "%"
		query = query.Where("name ILIKE ? OR key_prefix ILIKE ?", like, like)
	}

	var total int64
	query.Count(&total)

	var keys []models.APIKey
	query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&keys)

	return c.JSON(fiber.Map{"keys": keys, "count": total})
}

// POST /admin/api-keys — create a new API key
func HandleAdminCreateAPIKey(c *fiber.Ctx) error {
	var req struct {
		Name        string `json:"name"`
		Permissions string `json:"permissions"`
		RateLimit   int    `json:"rateLimit"`
		IsInternal  bool   `json:"isInternal"`
	}
	if err := c.BodyParser(&req); err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "Invalid request body")
	}
	if req.Name == "" {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "name is required")
	}
	if req.Permissions == "" {
		req.Permissions = "read,write"
	}
	if req.RateLimit <= 0 {
		req.RateLimit = 100
	}

	// Generate a secure random key
	rawKey := uuid.New().String() + "-" + uuid.New().String()
	hash := sha256.Sum256([]byte(rawKey))
	keyHash := fmt.Sprintf("%x", hash[:])

	apiKey := models.APIKey{
		ID:          uuid.New().String(),
		Name:        req.Name,
		KeyHash:     keyHash,
		KeyPrefix:   rawKey[:8],
		Permissions: req.Permissions,
		RateLimit:   req.RateLimit,
		IsInternal:  req.IsInternal,
		Status:      "ACTIVE",
	}

	if err := db.DB.Create(&apiKey).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusInternalServerError, "database_error", "Failed to create API key")
	}

	// Sync all active key hashes to Redis for fast middleware validation
	SyncAPIKeysToRedis()

	// Store the raw key in Redis for mail-edge to use as Bearer token
	db.Redis.Set(context.Background(), "system:api_token", rawKey, 0)

	logger.Log.Info("API key created", zap.String("name", req.Name), zap.String("prefix", rawKey[:8]), zap.Bool("isInternal", req.IsInternal))
	writeAuditLog("apikey.create", apiKey.ID, c)

	// Return the raw key ONLY on creation — it cannot be retrieved again
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"key":    apiKey,
		"rawKey": rawKey,
		"notice": "Save this key now — it cannot be shown again!",
	})
}

// DELETE /admin/api-keys/:id — delete an API key
func HandleAdminDeleteAPIKey(c *fiber.Ctx) error {
	id := c.Params("id")
	var key models.APIKey
	if err := db.DB.First(&key, "id = ?", id).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "key_not_found", "API key not found")
	}
	db.DB.Delete(&key)
	SyncAPIKeysToRedis()
	logger.Log.Info("API key deleted", zap.String("name", key.Name))
	writeAuditLog("apikey.delete", id, c)
	return c.JSON(fiber.Map{"status": "deleted"})
}

// POST /admin/api-keys/:id/roll — roll an API key
func HandleAdminRollAPIKey(c *fiber.Ctx) error {
	id := c.Params("id")
	var key models.APIKey
	if err := db.DB.First(&key, "id = ?", id).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "key_not_found", "API key not found")
	}

	rawKey := uuid.New().String() + "-" + uuid.New().String()
	hash := sha256.Sum256([]byte(rawKey))
	keyHash := fmt.Sprintf("%x", hash[:])

	key.KeyHash = keyHash
	key.KeyPrefix = rawKey[:8]
	if err := db.DB.Save(&key).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusInternalServerError, "database_error", "Failed to roll API key")
	}

	SyncAPIKeysToRedis()

	if key.IsInternal {
		db.Redis.Set(context.Background(), "system:api_token", rawKey, 0)
	}

	logger.Log.Info("API key rolled", zap.String("name", key.Name), zap.String("id", key.ID))
	writeAuditLog("apikey.roll", id, c)

	return c.JSON(fiber.Map{
		"status": "rolled",
		"key":    key,
		"rawKey": rawKey,
	})
}

// ===========================================================================
// HELPERS
// ===========================================================================

// writeAuditLog records an admin action to the audit_logs table
func writeAuditLog(action string, targetID string, c *fiber.Ctx) {
	log := models.AuditLog{
		ID:        uuid.New().String(),
		Action:    action,
		TargetID:  targetID,
		IPAddress: c.IP(),
	}
	db.DB.Create(&log)
}

// SyncAPIKeysToRedis pushes all ACTIVE API key hashes to a Redis set for O(1) validation.
// It also stores per-key metadata (isInternal flag) in a hash for middleware lookup.
func SyncAPIKeysToRedis() {
	ctx := context.Background()
	var keys []models.APIKey
	db.DB.Where("status = ?", "ACTIVE").Find(&keys)

	// H-4: Use pipeline to avoid race condition between Del and SAdd
	pipe := db.Redis.Pipeline()
	pipe.Del(ctx, "system:api_key_hashes", "system:api_key_meta")
	for _, k := range keys {
		pipe.SAdd(ctx, "system:api_key_hashes", k.KeyHash)
		if k.IsInternal {
			pipe.HSet(ctx, "system:api_key_meta", k.KeyHash, "internal")
		}
	}
	if _, err := pipe.Exec(ctx); err != nil {
		logger.Log.Error("Failed to sync API keys to Redis", zap.Error(err))
	}
	logger.Log.Debug("API key hashes synced to Redis", zap.Int("count", len(keys)))
}

// ===========================================================================
// PUT /admin/domains/:id — edit domain (change node, status)
// ===========================================================================

func HandleAdminUpdateDomain(c *fiber.Ctx) error {
	domainID := c.Params("id")

	var domain models.Domain
	if err := db.DB.First(&domain, "id = ?", domainID).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "domain_not_found", "Domain not found")
	}

	var req struct {
		NodeID *string `json:"nodeId"`
		Status string  `json:"status"`
	}
	if err := c.BodyParser(&req); err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "Invalid request body")
	}

	if req.NodeID != nil {
		if *req.NodeID == "" {
			domain.NodeID = nil
		} else {
			var node models.MailNode
			if err := db.DB.First(&node, "id = ?", *req.NodeID).Error; err != nil {
				return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_node", "Node not found")
			}
			domain.NodeID = req.NodeID
		}
	}

	if req.Status == "ACTIVE" || req.Status == "PENDING" || req.Status == "DISABLED" {
		domain.Status = req.Status
	}

	db.DB.Save(&domain)
	writeAuditLog("domain.update", domainID, c)
	logger.Log.Info("Domain updated", zap.String("id", domainID))

	db.DB.Preload("Node").First(&domain, "id = ?", domainID)
	return c.JSON(domain)
}

// ===========================================================================
// PUT /admin/nodes/:id — edit node (name, IP, region)
// ===========================================================================

func HandleAdminUpdateNode(c *fiber.Ctx) error {
	nodeID := c.Params("id")

	var node models.MailNode
	if err := db.DB.First(&node, "id = ?", nodeID).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "node_not_found", "Node not found")
	}

	var req struct {
		Name      string  `json:"name"`
		Hostname  *string `json:"hostname"` // pointer: null=omit, ""=clear, "x"=set
		IPAddress string  `json:"ipAddress"`
		Region    string  `json:"region"`
		Status    string  `json:"status"`
	}
	if err := c.BodyParser(&req); err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "Invalid request body")
	}

	if req.Name != "" {
		node.Name = req.Name
	}
	if req.Hostname != nil {
		node.Hostname = *req.Hostname
	}
	if req.IPAddress != "" {
		node.IPAddress = req.IPAddress
	}
	if req.Region != "" {
		node.Region = req.Region
	}
	if req.Status == "ACTIVE" || req.Status == "DISABLED" {
		node.Status = req.Status
	}

	db.DB.Save(&node)
	writeAuditLog("node.update", nodeID, c)
	logger.Log.Info("Node updated", zap.String("id", nodeID), zap.String("name", node.Name))
	return c.JSON(node)
}

// ===========================================================================
// PUT /admin/filters/:id — edit filter (pattern, type, reason)
// ===========================================================================

func HandleAdminUpdateFilter(c *fiber.Ctx) error {
	filterID := c.Params("id")

	var filter models.DomainFilter
	if err := db.DB.First(&filter, "id = ?", filterID).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "filter_not_found", "Filter not found")
	}

	var req struct {
		Pattern    string `json:"pattern"`
		FilterType string `json:"filterType"`
		Reason     string `json:"reason"`
	}
	if err := c.BodyParser(&req); err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "Invalid request body")
	}

	if req.Pattern != "" {
		filter.Pattern = strings.ToLower(req.Pattern)
	}
	if req.FilterType == "BLOCK" || req.FilterType == "ALLOW" {
		filter.FilterType = req.FilterType
	}
	if req.Reason != "" {
		filter.Reason = req.Reason
	}

	db.DB.Save(&filter)
	syncFiltersToRedis()
	writeAuditLog("filter.update", filterID, c)
	logger.Log.Info("Filter updated", zap.String("id", filterID))
	return c.JSON(filter)
}

// ===========================================================================
// PUT /admin/api-keys/:id — edit API key (name, permissions, rate limit)
// ===========================================================================

func HandleAdminUpdateAPIKey(c *fiber.Ctx) error {
	keyID := c.Params("id")

	var key models.APIKey
	if err := db.DB.First(&key, "id = ?", keyID).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "key_not_found", "API key not found")
	}

	var req struct {
		Name        string `json:"name"`
		Permissions string `json:"permissions"`
		RateLimit   int    `json:"rateLimit"`
		Status      string `json:"status"`
		IsInternal  *bool  `json:"isInternal"` // pointer to distinguish false from omitted
	}
	if err := c.BodyParser(&req); err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "Invalid request body")
	}

	if req.Name != "" {
		key.Name = req.Name
	}
	if req.Permissions != "" {
		key.Permissions = req.Permissions
	}
	if req.RateLimit > 0 {
		key.RateLimit = req.RateLimit
	}
	if req.Status == "ACTIVE" || req.Status == "REVOKED" || req.Status == "DISABLED" {
		key.Status = req.Status
	}
	if req.IsInternal != nil {
		key.IsInternal = *req.IsInternal
	}

	db.DB.Save(&key)
	SyncAPIKeysToRedis() // always sync after any key change
	writeAuditLog("apikey.update", keyID, c)
	logger.Log.Info("API key updated", zap.String("id", keyID), zap.Bool("isInternal", key.IsInternal))
	return c.JSON(key)
}

// ===========================================================================
// DELETE /admin/messages/:id — hard-delete a message
// ===========================================================================

func HandleAdminDeleteMessage(c *fiber.Ctx) error {
	msgID := c.Params("id")

	var msg models.Message
	if err := db.DB.First(&msg, "id = ?", msgID).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "message_not_found", "Message not found")
	}

	// Delete attachments
	db.DB.Where("message_id = ?", msgID).Delete(&models.Attachment{})
	// Delete message
	db.DB.Delete(&msg)

	writeAuditLog("message.delete", msgID, c)
	logger.Log.Info("Message deleted via admin", zap.String("id", msgID))
	return c.JSON(fiber.Map{"status": "deleted", "id": msgID})
}

// ===========================================================================
// POST /admin/messages/bulk-delete — bulk-delete messages
// ===========================================================================

func HandleAdminBulkDeleteMessages(c *fiber.Ctx) error {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.BodyParser(&req); err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "Invalid request body")
	}
	if len(req.IDs) == 0 {
		return apiutil.SendError(c, fiber.StatusBadRequest, "empty_ids", "No message IDs provided")
	}
	if len(req.IDs) > 100 {
		return apiutil.SendError(c, fiber.StatusBadRequest, "too_many_ids", "Maximum 100 IDs per request")
	}

	// M-6: Use transaction for atomic bulk delete
	var count int64
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("message_id IN ?", req.IDs).Delete(&models.Attachment{}).Error; err != nil {
			return err
		}
		result := tx.Where("id IN ?", req.IDs).Delete(&models.Message{})
		if result.Error != nil {
			return result.Error
		}
		count = result.RowsAffected
		return nil
	})
	if err != nil {
		logger.Log.Error("Bulk delete failed", zap.Error(err))
		return apiutil.SendError(c, fiber.StatusInternalServerError, "database_error", "Bulk delete failed")
	}

	writeAuditLog("messages.bulk_delete", fmt.Sprintf("count=%d", count), c)
	logger.Log.Info("Messages bulk-deleted via admin", zap.Int64("count", count))

	return c.JSON(fiber.Map{
		"status":  "deleted",
		"deleted": count,
	})
}

// ===========================================================================
// POST /admin/mailboxes/quick-create — quick-create mailbox for testing
// ===========================================================================

func HandleAdminQuickCreateMailbox(c *fiber.Ctx) error {
	var req struct {
		DomainID string `json:"domainId"`
		TTLHours int    `json:"ttlHours"`
	}
	if err := c.BodyParser(&req); err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "Invalid request body")
	}

	// Find domain
	var domain models.Domain
	if req.DomainID != "" {
		if err := db.DB.First(&domain, "id = ? AND status = ?", req.DomainID, "ACTIVE").Error; err != nil {
			return apiutil.SendError(c, fiber.StatusBadRequest, "domain_not_found", "Domain not found or inactive")
		}
	} else {
		// Use first active domain
		if err := db.DB.Where("status = ?", "ACTIVE").First(&domain).Error; err != nil {
			return apiutil.SendError(c, fiber.StatusBadRequest, "no_domain", "No active domain available")
		}
	}

	ttl := 1 // default 1 hour for testing
	if req.TTLHours > 0 {
		ttl = req.TTLHours
	}

	now := time.Now()
	expiresAt := now.Add(time.Duration(ttl) * time.Hour)
	localPart := "test-" + uuid.New().String()[:8]

	mailbox := models.Mailbox{
		ID:        uuid.New().String(),
		LocalPart: localPart,
		DomainID:  domain.ID,
		TenantID:  "admin-test",
		Status:    "ACTIVE",
		ExpiresAt: &expiresAt,
		CreatedAt: now,
	}

	if err := db.DB.Create(&mailbox).Error; err != nil {
		logger.Log.Error("Failed to create test mailbox", zap.Error(err))
		return apiutil.SendError(c, fiber.StatusInternalServerError, "database_error", "Database error")
	}

	fullAddress := localPart + "@" + domain.DomainName
	db.Redis.SAdd(context.Background(), "system:active_mailboxes", fullAddress)

	writeAuditLog("mailbox.quick_create", mailbox.ID, c)
	logger.Log.Info("Quick mailbox created", zap.String("address", fullAddress))

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":        mailbox.ID,
		"address":   fullAddress,
		"localPart": localPart,
		"domain":    domain.DomainName,
		"expiresAt": expiresAt,
		"ttlHours":  ttl,
	})
}

// ===========================================================================
// POST /admin/webhook-test — send a test webhook payload
// ===========================================================================

func HandleAdminWebhookTest(c *fiber.Ctx) error {
	ctx := context.Background()
	webhookURL, _ := db.Redis.HGet(ctx, "system:settings", "webhook_url").Result()
	webhookSecret, _ := db.Redis.HGet(ctx, "system:settings", "webhook_secret").Result()

	if webhookURL == "" {
		return apiutil.SendError(c, fiber.StatusBadRequest, "no_webhook", "Webhook URL not configured in Settings")
	}

	result, err := fireWebhook(webhookURL, webhookSecret, "test", map[string]interface{}{
		"event":     "test",
		"mailboxId": "test-" + uuid.New().String(),
		"messageId": "test-" + uuid.New().String(),
		"to":        "test@example.com",
		"from":      "tempmail-test@system.local",
		"subject":   "TempMail Webhook Test",
		"message":   "This is a test webhook from TempMail admin panel",
		"timestamp": time.Now().UTC(),
	})
	if err != nil {
		return c.JSON(fiber.Map{"status": "error", "error": err.Error(), "url": webhookURL})
	}
	return c.JSON(fiber.Map{"status": "ok", "response": result, "url": webhookURL})
}

// ===========================================================================
// WEBHOOK FIRE HELPER — called from ingest
// ===========================================================================

// FireMessageWebhook sends a webhook notification when a new message is received
func FireMessageWebhook(mailboxID, messageID, toAddress, fromAddr, subject string) {
	ctx := context.Background()
	webhookURL, _ := db.Redis.HGet(ctx, "system:settings", "webhook_url").Result()
	webhookSecret, _ := db.Redis.HGet(ctx, "system:settings", "webhook_secret").Result()

	if webhookURL == "" {
		return
	}

	go func() {
		_, err := fireWebhook(webhookURL, webhookSecret, "message.received", map[string]interface{}{
			"event":     "message.received",
			"mailboxId": mailboxID,
			"messageId": messageID,
			"to":        toAddress,
			"from":      fromAddr,
			"subject":   subject,
			"timestamp": time.Now().UTC(),
		})
		if err != nil {
			logger.Log.Warn("Webhook delivery failed",
				zap.String("url", webhookURL),
				zap.Error(err),
			)
		}
	}()
}

// fireWebhook sends a JSON POST to the webhook URL with optional HMAC signature
func fireWebhook(url, secret, event string, payload map[string]interface{}) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Event", event)

	// Sign with HMAC-SHA256 if secret is configured
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		sig := fmt.Sprintf("sha256=%x", mac.Sum(nil))
		req.Header.Set("X-Webhook-Signature", sig)
	}

	// M-4: Reuse package-level webhookClient
	client := webhookClient
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return string(respBody), fmt.Errorf("webhook returned %d: %s", resp.StatusCode, string(respBody))
	}

	return string(respBody), nil
}
