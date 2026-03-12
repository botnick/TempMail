package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"tempmail/shared/db"
	"tempmail/shared/logger"
	"tempmail/shared/models"
	"tempmail/shared/namegen"
)

// ---------------------------------------------------------------------------
// POST /v1/mailbox/create — สร้างกล่องจดหมายทันที
// ---------------------------------------------------------------------------

type CreateMailboxRequest struct {
	LocalPart string  `json:"localPart"` // ชื่อ mailbox ที่ต้องการ (ถ้าไม่ส่ง → random)
	DomainID  string  `json:"domainId"`  // domain ที่ต้องการ
	TenantID  string  `json:"tenantId"`  // user ID จากเว็บหลัก
	TTLHours  *int    `json:"ttlHours"`  // อายุกล่อง (ถ้าไม่ส่ง → ตาม plan)
}

func HandleCreateMailbox(c *fiber.Ctx) error {
	var req CreateMailboxRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid request body"})
	}

	// Validate domain
	var domain models.Domain
	if req.DomainID != "" {
		if err := db.DB.Where("id = ? AND status = ?", req.DomainID, "ACTIVE").First(&domain).Error; err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Domain not found or inactive"})
		}
	} else {
		// ใช้ domain แรกที่เป็น public (ไม่มี tenant)
		if err := db.DB.Where("tenant_id IS NULL AND status = ?", "ACTIVE").First(&domain).Error; err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "No public domain available"})
		}
	}

	// Generate or validate local part
	localPart := strings.ToLower(strings.TrimSpace(req.LocalPart))
	if localPart == "" {
		localPart = namegen.Generate() // e.g. "cool.fox42"
	}

	// Sanitize local part — only allow a-z, 0-9, dots, dashes, underscores
	for _, ch := range localPart {
		if !((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '-' || ch == '_') {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Invalid mailbox name. Use only a-z, 0-9, dots, dashes, underscores."})
		}
	}

	if len(localPart) < 1 || len(localPart) > 64 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Mailbox name must be 1-64 characters"})
	}

	// Check uniqueness
	var existing models.Mailbox
	if err := db.DB.Where("local_part = ? AND domain_id = ? AND status = ?", localPart, domain.ID, "ACTIVE").First(&existing).Error; err == nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Mailbox name already taken"})
	}

	// Determine TTL
	ttlHours := 24 // default
	if req.TTLHours != nil && *req.TTLHours > 0 {
		ttlHours = *req.TTLHours
	}

	// Tenant ID
	tenantID := req.TenantID
	if tenantID == "" {
		tenantID = "anonymous" // allow anonymous creation for flows that don't require auth
	}

	now := time.Now()
	expiresAt := now.Add(time.Duration(ttlHours) * time.Hour)

	mailbox := models.Mailbox{
		ID:        uuid.New().String(),
		LocalPart: localPart,
		DomainID:  domain.ID,
		TenantID:  tenantID,
		Status:    "ACTIVE",
		ExpiresAt: &expiresAt,
		CreatedAt: now,
	}

	if err := db.DB.Create(&mailbox).Error; err != nil {
		logger.Log.Error("Failed to create mailbox", zap.Error(err))
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Failed to create mailbox"})
	}

	// Register in Redis for O(1) SMTP validation
	fullAddress := localPart + "@" + domain.DomainName
	db.Redis.SAdd(context.Background(), "system:active_mailboxes", fullAddress)

	logger.Log.Info("Mailbox created",
		zap.String("id", mailbox.ID),
		zap.String("address", fullAddress),
		zap.String("tenant", tenantID),
		zap.Int("ttl_hours", ttlHours),
	)

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":        mailbox.ID,
		"address":   fullAddress,
		"localPart": localPart,
		"domain":    domain.DomainName,
		"domainId":  domain.ID,
		"expiresAt": expiresAt,
		"status":    "ACTIVE",
	})
}

// ---------------------------------------------------------------------------
// GET /v1/mailbox/:id/messages — ดึงข้อความทั้งหมดในกล่อง
// ---------------------------------------------------------------------------

func HandleGetMessages(c *fiber.Ctx) error {
	mailboxID := c.Params("id")

	var mailbox models.Mailbox
	if err := db.DB.First(&mailbox, "id = ?", mailboxID).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Mailbox not found"})
	}

	var messages []models.Message
	db.DB.Where("mailbox_id = ?", mailboxID).
		Order("received_at DESC").
		Limit(100).
		Find(&messages)

	// Build safe response (strip HTMLBody for list view)
	type messageSummary struct {
		ID               string    `json:"id"`
		From             string    `json:"from"`
		Subject          string    `json:"subject"`
		SpamScore        float64   `json:"spamScore"`
		IsSpam           bool      `json:"isSpam"`
		QuarantineAction string    `json:"quarantineAction"`
		HasHTML          bool      `json:"hasHtml"`
		ReceivedAt       time.Time `json:"receivedAt"`
		ExpiresAt        time.Time `json:"expiresAt"`
	}

	summaries := make([]messageSummary, len(messages))
	for i, m := range messages {
		summaries[i] = messageSummary{
			ID:               m.ID,
			From:             m.FromAddress,
			Subject:          m.Subject,
			SpamScore:        m.SpamScore,
			IsSpam:           m.QuarantineAction != "ACCEPT",
			QuarantineAction: m.QuarantineAction,
			HasHTML:          m.HTMLBody != "",
			ReceivedAt:       m.ReceivedAt,
			ExpiresAt:        m.ExpiresAt,
		}
	}

	return c.JSON(fiber.Map{
		"mailboxId": mailboxID,
		"count":     len(summaries),
		"messages":  summaries,
	})
}

// ---------------------------------------------------------------------------
// GET /v1/message/:id — ดึง message เต็ม (text + sanitized HTML + attachments)
// ---------------------------------------------------------------------------

func HandleGetMessage(c *fiber.Ctx) error {
	messageID := c.Params("id")

	var msg models.Message
	if err := db.DB.Preload("Attachments").First(&msg, "id = ?", messageID).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Message not found"})
	}

	type attachmentInfo struct {
		ID          string `json:"id"`
		Filename    string `json:"filename"`
		ContentType string `json:"contentType"`
		SizeBytes   int64  `json:"sizeBytes"`
	}

	attachments := make([]attachmentInfo, len(msg.Attachments))
	for i, a := range msg.Attachments {
		attachments[i] = attachmentInfo{
			ID:          a.ID,
			Filename:    a.Filename,
			ContentType: a.ContentType,
			SizeBytes:   a.SizeBytes,
		}
	}

	return c.JSON(fiber.Map{
		"id":               msg.ID,
		"mailboxId":        msg.MailboxID,
		"from":             msg.FromAddress,
		"to":               msg.ToAddress,
		"subject":          msg.Subject,
		"textBody":         msg.TextBody,
		"htmlBody":         msg.HTMLBody, // Already sanitized at ingest time
		"spamScore":        msg.SpamScore,
		"quarantineAction": msg.QuarantineAction,
		"attachments":      attachments,
		"receivedAt":       msg.ReceivedAt,
		"expiresAt":        msg.ExpiresAt,
	})
}

// ---------------------------------------------------------------------------
// DELETE /v1/mailbox/:id — ลบ mailbox
// ---------------------------------------------------------------------------

func HandleDeleteMailbox(c *fiber.Ctx) error {
	mailboxID := c.Params("id")

	var mailbox models.Mailbox
	if err := db.DB.Preload("Domain").First(&mailbox, "id = ?", mailboxID).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Mailbox not found"})
	}

	// Set status to DELETED (soft delete)
	mailbox.Status = "DELETED"
	db.DB.Save(&mailbox)

	// Remove from Redis active set
	fullAddress := mailbox.LocalPart + "@" + mailbox.Domain.DomainName
	db.Redis.SRem(context.Background(), "system:active_mailboxes", fullAddress)

	logger.Log.Info("Mailbox deleted via SDK",
		zap.String("id", mailboxID),
		zap.String("address", fullAddress),
	)

	return c.JSON(fiber.Map{"status": "deleted", "id": mailboxID})
}

// ---------------------------------------------------------------------------
// GET /v1/domains — ดึงรายการ domain ที่ใช้งานได้
// ---------------------------------------------------------------------------

func HandleListDomains(c *fiber.Ctx) error {
	var domains []models.Domain
	db.DB.Where("status = ?", "ACTIVE").Find(&domains)

	type domainInfo struct {
		ID         string `json:"id"`
		DomainName string `json:"domainName"`
		IsPublic   bool   `json:"isPublic"`
	}

	result := make([]domainInfo, len(domains))
	for i, d := range domains {
		result[i] = domainInfo{
			ID:         d.ID,
			DomainName: d.DomainName,
			IsPublic:   d.TenantID == nil,
		}
	}

	return c.JSON(fiber.Map{
		"count":   len(result),
		"domains": result,
	})
}
