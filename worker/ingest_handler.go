package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3svc "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/microcosm-cc/bluemonday"
	"go.uber.org/zap"

	"tempmail/shared/db"
	"tempmail/shared/logger"
	"tempmail/shared/models"
	"tempmail/shared/tasks"
)

// HTML sanitizer — strips all dangerous tags, attributes, and JS
var htmlSanitizer = bluemonday.UGCPolicy()

// ---------------------------------------------------------------------------
// CACHED INGEST SETTINGS — avoids Redis HGet per email
// ---------------------------------------------------------------------------

type ingestSettings struct {
	maxAttachments   int
	maxAttSizeBytes  int64
	maxMsgSizeBytes  int64
	spamRejectThresh float64
	cachedUntil      time.Time
}

var (
	cachedIngSettings ingestSettings
	ingSettingsMu     sync.RWMutex
)

func getIngestSettings() ingestSettings {
	ingSettingsMu.RLock()
	if time.Now().Before(cachedIngSettings.cachedUntil) {
		s := cachedIngSettings
		ingSettingsMu.RUnlock()
		return s
	}
	ingSettingsMu.RUnlock()

	ctx := context.Background()
	s := ingestSettings{
		maxAttachments:   10,
		maxAttSizeBytes:  10 * 1024 * 1024,
		maxMsgSizeBytes:  25 * 1024 * 1024,
		spamRejectThresh: 15.0,
		cachedUntil:      time.Now().Add(1 * time.Minute),
	}

	if v, err := db.Redis.HGet(ctx, "system:settings", "max_attachments").Result(); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			s.maxAttachments = n
		}
	}
	if v, err := db.Redis.HGet(ctx, "system:settings", "max_attachment_size_mb").Result(); err == nil && v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			s.maxAttSizeBytes = n * 1024 * 1024
		}
	}
	if v, err := db.Redis.HGet(ctx, "system:settings", "max_message_size_mb").Result(); err == nil && v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			s.maxMsgSizeBytes = n * 1024 * 1024
		}
	}
	if v, err := db.Redis.HGet(ctx, "system:settings", "spam_reject_threshold").Result(); err == nil && v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			s.spamRejectThresh = n
		}
	}

	ingSettingsMu.Lock()
	cachedIngSettings = s
	ingSettingsMu.Unlock()

	return s
}

// ---------------------------------------------------------------------------
// PARSED EMAIL TYPES
// ---------------------------------------------------------------------------

type parsedEmail struct {
	Subject     string
	TextBody    string
	HTMLBody    string
	Attachments []parsedAttachment
}

type parsedAttachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// ---------------------------------------------------------------------------
// MAIN HANDLER — processes queued mail tasks
// ---------------------------------------------------------------------------

func HandleMailIngest(ctx context.Context, t *asynq.Task) error {
	payload, err := tasks.ParseMailIngestPayload(t.Payload())
	if err != nil {
		logger.Log.Error("Failed to parse mail ingest payload", zap.Error(err))
		return fmt.Errorf("parse payload: %w", err)
	}

	fromAddr := payload.From
	toAddress := strings.ToLower(strings.TrimSpace(payload.To))
	spamScore := payload.SpamScore
	quarantineAction := payload.QuarantineAction
	rawEmailBuffer := payload.RawEmail

	parts := strings.Split(toAddress, "@")
	if len(parts) != 2 {
		logger.Log.Error("Invalid recipient address", zap.String("to", toAddress))
		return fmt.Errorf("invalid recipient: %s", toAddress)
	}

	localPart, domainName := parts[0], parts[1]

	// 1. Validate routing
	var domain models.Domain
	if err := db.DB.Where("domain_name = ?", domainName).First(&domain).Error; err != nil {
		logger.Log.Warn("Domain not found, skipping", zap.String("domain", domainName))
		return nil // don't retry — domain doesn't exist
	}

	var mailbox models.Mailbox
	if err := db.DB.Where("local_part = ? AND domain_id = ? AND status = ?", localPart, domain.ID, "ACTIVE").First(&mailbox).Error; err != nil {
		logger.Log.Warn("Mailbox not found or inactive, skipping",
			zap.String("local_part", localPart), zap.String("domain", domainName))
		return nil // don't retry — mailbox doesn't exist
	}

	// 2. Parse RFC822 email
	parsed := parseRFC822(rawEmailBuffer)

	// 3. Sanitize HTML body
	sanitizedHTML := ""
	if parsed.HTMLBody != "" {
		sanitizedHTML = htmlSanitizer.Sanitize(parsed.HTMLBody)
	}

	// 4. Raw email upload removed — only attachments are stored in R2 to save space.
	//    Email body (text + HTML) is stored in Postgres.

	// 5. Determine retention
	retentionHours := 24
	if v, err := db.Redis.HGet(context.Background(), "system:settings", "default_message_ttl_hours").Result(); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			retentionHours = n
		}
	}

	// 6. Create message record
	msgID := uuid.New().String()
	msg := models.Message{
		ID:               msgID,
		MailboxID:        mailbox.ID,
		FromAddress:      truncateString(fromAddr, 255),
		ToAddress:        truncateString(toAddress, 255),
		Subject:          truncateString(parsed.Subject, 500),
		TextBody:         parsed.TextBody,
		HTMLBody:         sanitizedHTML,
		S3KeyRaw:         "",
		SpamScore:        spamScore,
		QuarantineAction: truncateString(quarantineAction, 20),
		ExpiresAt:        time.Now().Add(time.Duration(retentionHours) * time.Hour),
	}

	if err := db.DB.Create(&msg).Error; err != nil {
		logger.Log.Error("Failed to insert message", zap.Error(err))
		return fmt.Errorf("db insert: %w", err) // retry on DB error
	}

	// 7. Upload and record attachments
	sets := getIngestSettings()
	attSaved := 0

	for _, att := range parsed.Attachments {
		if attSaved >= sets.maxAttachments {
			logger.Log.Warn("Attachment count cap reached",
				zap.Int("max", sets.maxAttachments), zap.Int("total", len(parsed.Attachments)))
			break
		}

		if int64(len(att.Data)) > sets.maxAttSizeBytes {
			logger.Log.Warn("Attachment exceeds size limit, skipping",
				zap.String("filename", att.Filename),
				zap.Int64("size", int64(len(att.Data))),
				zap.Int64("max", sets.maxAttSizeBytes))
			continue
		}

		attKey := ""
		if s3Client != nil {
			attKey = fmt.Sprintf("attachments/%s/%s", msgID, uuid.New().String())
			_, err := s3Client.PutObject(context.TODO(), &s3svc.PutObjectInput{
				Bucket:      aws.String(r2Bucket),
				Key:         aws.String(attKey),
				Body:        bytes.NewReader(att.Data),
				ContentType: aws.String(att.ContentType),
			})
			if err != nil {
				logger.Log.Error("Failed to upload attachment", zap.Error(err), zap.String("filename", att.Filename))
				continue
			}
		}

		attachment := models.Attachment{
			ID:          uuid.New().String(),
			MessageID:   msgID,
			Filename:    truncateString(att.Filename, 255),
			ContentType: truncateString(att.ContentType, 100),
			SizeBytes:   int64(len(att.Data)),
			S3Key:       truncateString(attKey, 255),
		}
		if err := db.DB.Create(&attachment).Error; err != nil {
			logger.Log.Error("Failed to save attachment metadata", zap.Error(err))
		} else {
			attSaved++
		}
	}

	logger.Log.Info("Mail ingest task processed",
		zap.String("id", msgID),
		zap.String("to", toAddress),
		zap.String("from", fromAddr),
		zap.Float64("spam_score", spamScore),
		zap.Int("attachments", attSaved),
	)

	// 8. Fire webhook notification (async — non-blocking)
	fireWebhookFromWorker(mailbox.ID, msgID, toAddress, fromAddr, truncateString(parsed.Subject, 200))

	// 9. Publish SSE event for admin panel real-time refresh
	db.Redis.Publish(ctx, "mail:events", "new_message")

	// 10. Audit log — every ingest is traceable
	systemUser := "system:worker"
	db.DB.Create(&models.AuditLog{
		ID:        msgID,
		UserID:    &systemUser,
		Action:    "mail_ingested",
		TargetID:  toAddress,
		Reason:    fmt.Sprintf("From: %s, Subject: %s, SpamScore: %.1f, Attachments: %d", fromAddr, truncateString(parsed.Subject, 80), spamScore, attSaved),
		IPAddress: "127.0.0.1",
	})

	return nil
}

// ---------------------------------------------------------------------------
// WEBHOOK NOTIFICATION (fire-and-forget from worker)
// ---------------------------------------------------------------------------

func fireWebhookFromWorker(mailboxID, msgID, to, from, subject string) {
	ctx := context.Background()
	url, err := db.Redis.HGet(ctx, "webhook:"+mailboxID, "url").Result()
	if err != nil || url == "" {
		return // no webhook configured
	}

	go func() {
		// Use proper JSON marshaling to prevent injection
		type webhookPayload struct {
			Event     string `json:"event"`
			MailboxID string `json:"mailbox_id"`
			MessageID string `json:"message_id"`
			To        string `json:"to"`
			From      string `json:"from"`
			Subject   string `json:"subject"`
		}
		payload, err := json.Marshal(webhookPayload{
			Event:     "new_message",
			MailboxID: mailboxID,
			MessageID: msgID,
			To:        to,
			From:      from,
			Subject:   subject,
		})
		if err != nil {
			return
		}

		req, err := http.NewRequest("POST", url, bytes.NewReader(payload))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")

		if secret, serr := db.Redis.HGet(ctx, "webhook:"+mailboxID, "secret").Result(); serr == nil && secret != "" {
			req.Header.Set("X-Webhook-Secret", secret)
		}

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			logger.Log.Debug("Webhook delivery failed", zap.Error(err), zap.String("url", url))
			return
		}
		resp.Body.Close()
	}()
}

// ---------------------------------------------------------------------------
// RFC822 PARSER
// ---------------------------------------------------------------------------

func parseRFC822(raw []byte) parsedEmail {
	result := parsedEmail{}

	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		logger.Log.Debug("Failed to parse RFC822, storing raw", zap.Error(err))
		result.TextBody = string(raw)
		return result
	}

	// Decode RFC 2047 encoded subject (e.g. =?UTF-8?B?...?= → readable text)
	rawSubject := msg.Header.Get("Subject")
	dec := new(mime.WordDecoder)
	if decoded, err := dec.DecodeHeader(rawSubject); err == nil {
		result.Subject = decoded
	} else {
		result.Subject = rawSubject
	}

	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		body, _ := io.ReadAll(msg.Body)
		result.TextBody = string(body)
		return result
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		body, _ := io.ReadAll(msg.Body)
		result.TextBody = string(body)
		return result
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		result = parseMIMEParts(msg.Body, params["boundary"], result)
	} else {
		body, _ := io.ReadAll(msg.Body)
		// Decode Content-Transfer-Encoding (base64 / quoted-printable)
		encoding := msg.Header.Get("Content-Transfer-Encoding")
		if strings.EqualFold(encoding, "base64") {
			if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body))); err == nil {
				body = decoded
			}
		} else if strings.EqualFold(encoding, "quoted-printable") {
			if decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body))); err == nil {
				body = decoded
			}
		}
		if mediaType == "text/html" {
			result.HTMLBody = string(body)
		} else {
			result.TextBody = string(body)
		}
	}

	return result
}

func parseMIMEParts(r io.Reader, boundary string, result parsedEmail) parsedEmail {
	if boundary == "" {
		return result
	}

	mr := multipart.NewReader(r, boundary)
	for {
		part, err := mr.NextPart()
		if err != nil {
			break
		}

		partContentType := part.Header.Get("Content-Type")
		disposition := part.Header.Get("Content-Disposition")
		mediaType, params, _ := mime.ParseMediaType(partContentType)

		body, err := io.ReadAll(part)
		if err != nil {
			continue
		}

		// Nested multipart
		if strings.HasPrefix(mediaType, "multipart/") {
			result = parseMIMEParts(bytes.NewReader(body), params["boundary"], result)
			continue
		}

		// Attachment
		if strings.HasPrefix(disposition, "attachment") || (disposition != "" && part.FileName() != "") {
			data := body
			encoding := part.Header.Get("Content-Transfer-Encoding")
			if strings.EqualFold(encoding, "base64") {
				decoded, err := base64.StdEncoding.DecodeString(string(body))
				if err == nil {
					data = decoded
				}
			}

			result.Attachments = append(result.Attachments, parsedAttachment{
				Filename:    sanitizeFilename(part.FileName()),
				ContentType: mediaType,
				Data:        data,
			})
			continue
		}

		// Body content
		switch mediaType {
		case "text/plain":
			result.TextBody = string(body)
		case "text/html":
			result.HTMLBody = string(body)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// HELPERS
// ---------------------------------------------------------------------------

func truncateString(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}

func sanitizeFilename(name string) string {
	if name == "" {
		return "unnamed"
	}
	dec := new(mime.WordDecoder)
	if decoded, err := dec.DecodeHeader(name); err == nil {
		name = decoded
	}
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.ReplaceAll(name, "\x00", "")
	rs := []rune(name)
	if len(rs) > 150 {
		ext := ""
		if idx := strings.LastIndex(name, "."); idx > 0 {
			ext = name[idx:]
		}
		name = string(rs[:150-len([]rune(ext))]) + ext
	}
	return name
}
