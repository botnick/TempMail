package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"net/http"
	"tempmail/shared/apiutil"
	"tempmail/shared/db"
	"tempmail/shared/logger"
	"tempmail/shared/models"
)

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
	client := &http.Client{Timeout: 2 * time.Second}
	rspamdURL := "http://rspamd:11334/ping"
	if resp, err := client.Get(rspamdURL); err == nil {
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
		"spam_reject_threshold": "15",
		"default_ttl_hours":    "24",
		"max_message_size_mb":  "25",
		"max_mailboxes_free":   "5",
		"allow_anonymous":      "true",
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
