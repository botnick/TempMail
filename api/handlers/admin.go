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
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"tempmail/shared/apiutil"
	"tempmail/shared/db"
	"tempmail/shared/logger"
	"tempmail/shared/models"
)

// Reusable HTTP client for Rspamd health checks (admin dashboard)
var rspamdHealthClient = &http.Client{Timeout: 2 * time.Second}

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
		"token":    token,
		"username": req.Username,
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

func HandleAdminDashboard(c *fiber.Ctx) error {
	var totalDomains, totalMailboxes, totalMessages, totalSpamBlocked int64

	db.DB.Model(&models.Domain{}).Count(&totalDomains)
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
	// System Service Status Checks
	// -----------------------------------------------------------------------
	services := map[string]string{
		"database":   "OFFLINE",
		"redis":      "OFFLINE",
		"rspamd":     "OFFLINE",
		"worker":     "ONLINE", // Assumed online if it's logging, or we can check last heartbeat
		"mailserver": "ONLINE", // Assumed online if API is reachable
	}

	// 1. Check PostgreSQL
	if sqlDB, err := db.DB.DB(); err == nil {
		if err := sqlDB.Ping(); err == nil {
			services["database"] = "ONLINE"
		}
	}

	// 2. Check Redis
	if err := db.Redis.Ping(context.Background()).Err(); err == nil {
		services["redis"] = "ONLINE"
	}

	// 3. Check Rspamd (HTTP ping to rspamd:11334)
	// Using a short timeout since it's an internal network
	rspamdURL := "http://rspamd:11334/ping"
	if resp, err := rspamdHealthClient.Get(rspamdURL); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			services["rspamd"] = "ONLINE"
		}
	}

	return c.JSON(fiber.Map{
		"totalDomains":         totalDomains,
		"totalMailboxes":       totalMailboxes,
		"totalMessages":        totalMessages,
		"totalSpamBlocked":     totalSpamBlocked,
		"messagesToday":        todayMessages,
		"redisActiveMailboxes": redisCount,
		"services":             services,
		"serverTime":           time.Now().UTC(),
	})
}

// ---------------------------------------------------------------------------
// GET /admin/domains — รายการ domain ทั้งหมด
// ---------------------------------------------------------------------------

func HandleAdminDomains(c *fiber.Ctx) error {
	var domains []models.Domain
	db.DB.Preload("Node").Order("created_at DESC").Find(&domains)
	return c.JSON(fiber.Map{"domains": domains, "count": len(domains)})
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

	// Check if domain already exists
	var existing models.Domain
	if err := db.DB.Where("domain_name = ?", req.DomainName).First(&existing).Error; err == nil {
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
		"domain":       domain,
		"dns":          dnsInstructions,
		"nodeIp":       nodeIP,
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

	domain.Status = "DELETED"
	db.DB.Save(&domain)

	// Deactivate all mailboxes on this domain
	db.DB.Model(&models.Mailbox{}).Where("domain_id = ? AND status = ?", domainID, "ACTIVE").
		Update("status", "DELETED")

	logger.Log.Info("Domain deleted via admin", zap.String("id", domainID), zap.String("domain", domain.DomainName))
	writeAuditLog("domain.delete", domainID, c)

	return c.JSON(fiber.Map{"status": "deleted", "id": domainID})
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
		query = query.Where("local_part ILIKE ?", "%"+search+"%")
	}

	var total int64
	query.Count(&total)

	var mailboxes []models.Mailbox
	query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&mailboxes)

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

	mailbox.Status = "DELETED"
	db.DB.Save(&mailbox)

	// Remove from Redis
	fullAddress := mailbox.LocalPart + "@" + mailbox.Domain.DomainName
	db.Redis.SRem(context.Background(), "system:active_mailboxes", fullAddress)

	logger.Log.Info("Mailbox deleted via admin", zap.String("id", mailboxID), zap.String("address", fullAddress))

	return c.JSON(fiber.Map{"status": "deleted", "id": mailboxID})
}

// ---------------------------------------------------------------------------
// GET /admin/messages — ค้นหา messages (pagination)
// ---------------------------------------------------------------------------

func HandleAdminMessages(c *fiber.Ctx) error {
	search := c.Query("search", "")
	mailboxID := c.Query("mailbox_id", "")
	limit := c.QueryInt("limit", 50)
	offset := c.QueryInt("offset", 0)

	query := db.DB.Model(&models.Message{})

	if mailboxID != "" {
		query = query.Where("mailbox_id = ?", mailboxID)
	}
	if search != "" {
		query = query.Where("from_address ILIKE ? OR subject ILIKE ?", "%"+search+"%", "%"+search+"%")
	}

	var total int64
	query.Count(&total)

	var messages []models.Message
	query.Order("received_at DESC").Limit(limit).Offset(offset).Find(&messages)

	return c.JSON(fiber.Map{
		"total":    total,
		"messages": messages,
	})
}

// ---------------------------------------------------------------------------
// GET /admin/audit-log — ดู audit log
// ---------------------------------------------------------------------------

func HandleAdminAuditLog(c *fiber.Ctx) error {
	limit := c.QueryInt("limit", 100)
	offset := c.QueryInt("offset", 0)

	var logs []models.AuditLog
	db.DB.Order("created_at DESC").Limit(limit).Offset(offset).Find(&logs)

	return c.JSON(fiber.Map{"logs": logs, "count": len(logs)})
}

// ---------------------------------------------------------------------------
// GET /admin/settings — ดูค่าตั้งต่าง ๆ ของระบบ (จาก Redis hash)
// ---------------------------------------------------------------------------

func HandleAdminGetSettings(c *fiber.Ctx) error {
	ctx := context.Background()
	settings, _ := db.Redis.HGetAll(ctx, "system:settings").Result()

	// Provide sensible defaults
	defaults := map[string]string{
		"spam_reject_threshold":       "15",
		"default_ttl_hours":           "24",
		"default_message_ttl_hours":   "24",
		"max_message_size_mb":         "25",
		"max_mailboxes_free":          "5",
		"max_attachments":             "10",
		"max_attachment_size_mb":      "10",
		"allow_anonymous":             "true",
		"webhook_url":                 "",
		"webhook_secret":              "",
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

	ctx := context.Background()
	for k, v := range body {
		db.Redis.HSet(ctx, "system:settings", k, v)
	}

	logger.Log.Info("System settings updated via admin", zap.Int("fields", len(body)))

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
// NODE MANAGEMENT
// ---------------------------------------------------------------------------

// GET /admin/nodes — รายการ node ทั้งหมด
func HandleAdminNodes(c *fiber.Ctx) error {
	var nodes []models.MailNode
	db.DB.Preload("Domains").Order("created_at ASC").Find(&nodes)
	return c.JSON(fiber.Map{"nodes": nodes, "count": len(nodes)})
}

// POST /admin/nodes — เพิ่ม node ใหม่
type CreateNodeRequest struct {
	Name      string `json:"name"`
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

	db.DB.Delete(&node)
	logger.Log.Info("Node deleted", zap.String("name", node.Name))
	return c.JSON(fiber.Map{"status": "deleted"})
}

// ===========================================================================
// DOMAIN FILTER (Blocklist / Whitelist)
// ===========================================================================

// GET /admin/filters — list all domain filters
func HandleAdminFilters(c *fiber.Ctx) error {
	var filters []models.DomainFilter
	db.DB.Order("created_at DESC").Find(&filters)
	return c.JSON(fiber.Map{"filters": filters, "count": len(filters)})
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

	// Clear and rebuild
	db.Redis.Del(ctx, "system:domain_blocklist", "system:domain_allowlist")
	for _, f := range filters {
		if f.FilterType == "BLOCK" {
			db.Redis.SAdd(ctx, "system:domain_blocklist", f.Pattern)
		} else {
			db.Redis.SAdd(ctx, "system:domain_allowlist", f.Pattern)
		}
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
		Settings map[string]string  `json:"settings"`
		Filters  []models.DomainFilter `json:"filters"`
	}
	if err := json.Unmarshal(c.Body(), &data); err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_json", "Invalid JSON format")
	}

	imported := 0
	ctx := context.Background()

	// Import settings
	if data.Settings != nil {
		for k, v := range data.Settings {
			db.Redis.HSet(ctx, "system:settings", k, v)
		}
		imported += len(data.Settings)
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
	redisInfo, _ := db.Redis.Info(ctx, "memory", "clients").Result()
	redisDBSize, _ := db.Redis.DBSize(ctx).Result()

	// Blocked messages (spam score > threshold)
	var blockedCount int64
	db.DB.Model(&models.Message{}).Where("quarantine_action != ?", "ACCEPT").Count(&blockedCount)

	// Domain filter counts
	var blocklistCount, allowlistCount int64
	db.DB.Model(&models.DomainFilter{}).Where("filter_type = ?", "BLOCK").Count(&blocklistCount)
	db.DB.Model(&models.DomainFilter{}).Where("filter_type = ?", "ALLOW").Count(&allowlistCount)

	return c.JSON(fiber.Map{
		"throughput": fiber.Map{
			"lastHour":  msgLastHour,
			"last24h":   msgLast24h,
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
			"dbSize": redisDBSize,
			"info":   redisInfo,
		},
	})
}

// ===========================================================================
// API KEY MANAGEMENT
// ===========================================================================

// GET /admin/api-keys — list all API keys
func HandleAdminAPIKeys(c *fiber.Ctx) error {
	var keys []models.APIKey
	db.DB.Order("created_at DESC").Find(&keys)
	return c.JSON(fiber.Map{"keys": keys, "count": len(keys)})
}

// POST /admin/api-keys — create a new API key
func HandleAdminCreateAPIKey(c *fiber.Ctx) error {
	var req struct {
		Name        string `json:"name"`
		Permissions string `json:"permissions"`
		RateLimit   int    `json:"rateLimit"`
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
		Status:      "ACTIVE",
	}

	if err := db.DB.Create(&apiKey).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusInternalServerError, "database_error", "Failed to create API key")
	}

	// Sync all active key hashes to Redis for fast middleware validation
	SyncAPIKeysToRedis()

	// Store the raw key in Redis for mail-edge to use as Bearer token
	db.Redis.Set(context.Background(), "system:api_token", rawKey, 0)

	logger.Log.Info("API key created", zap.String("name", req.Name), zap.String("prefix", rawKey[:8]))
	writeAuditLog("apikey.create", apiKey.ID, c)

	// Return the raw key ONLY on creation — it cannot be retrieved again
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"key":    apiKey,
		"rawKey": rawKey,
		"notice": "Save this key now — it cannot be shown again!",
	})
}

// DELETE /admin/api-keys/:id — revoke an API key
func HandleAdminDeleteAPIKey(c *fiber.Ctx) error {
	id := c.Params("id")
	var key models.APIKey
	if err := db.DB.First(&key, "id = ?", id).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "key_not_found", "API key not found")
	}
	key.Status = "REVOKED"
	db.DB.Save(&key)
	SyncAPIKeysToRedis()
	logger.Log.Info("API key revoked", zap.String("name", key.Name))
	writeAuditLog("apikey.revoke", id, c)
	return c.JSON(fiber.Map{"status": "revoked"})
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

// SyncAPIKeysToRedis pushes all ACTIVE API key hashes to a Redis set for O(1) validation
func SyncAPIKeysToRedis() {
	ctx := context.Background()
	var keys []models.APIKey
	db.DB.Where("status = ?", "ACTIVE").Find(&keys)

	// Clear and rebuild
	db.Redis.Del(ctx, "system:api_key_hashes")
	for _, k := range keys {
		db.Redis.SAdd(ctx, "system:api_key_hashes", k.KeyHash)
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
		Name      string `json:"name"`
		IPAddress string `json:"ipAddress"`
		Region    string `json:"region"`
		Status    string `json:"status"`
	}
	if err := c.BodyParser(&req); err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_request", "Invalid request body")
	}

	if req.Name != "" {
		node.Name = req.Name
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
	if req.Status == "ACTIVE" || req.Status == "REVOKED" {
		key.Status = req.Status
		SyncAPIKeysToRedis()
	}

	db.DB.Save(&key)
	writeAuditLog("apikey.update", keyID, c)
	logger.Log.Info("API key updated", zap.String("id", keyID))
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

	client := &http.Client{Timeout: 10 * time.Second}
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

