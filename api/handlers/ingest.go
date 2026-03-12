package handlers

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/microcosm-cc/bluemonday"
	"go.uber.org/zap"

	"tempmail/shared/apiutil"
	"tempmail/shared/db"
	"tempmail/shared/logger"
	"tempmail/shared/models"
)

var s3Client *s3.Client
var r2BucketName string // cached at init

// HTML sanitizer — strips all dangerous tags, attributes, and JS
var htmlSanitizer = bluemonday.UGCPolicy()

// ---------------------------------------------------------------------------
// CACHED INGEST SETTINGS — avoids Redis HGet per email
// ---------------------------------------------------------------------------

type ingestSettings struct {
	maxAttachments    int
	maxAttSizeBytes   int64
	maxMsgSizeBytes   int64
	spamRejectThresh  float64
	cachedUntil       time.Time
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

	// Rebuild from Redis
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

func init() {
	accountId := os.Getenv("R2_ACCOUNT_ID")
	accessKey := os.Getenv("R2_ACCESS_KEY_ID")
	secretKey := os.Getenv("R2_SECRET_ACCESS_KEY")

	if accountId != "" && accessKey != "" && secretKey != "" {
		r2Resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
			return aws.Endpoint{
				URL: fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountId),
			}, nil
		})

		cfg, err := awsconfig.LoadDefaultConfig(context.TODO(),
			awsconfig.WithEndpointResolverWithOptions(r2Resolver),
			awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
			awsconfig.WithRegion("auto"),
		)
		if err != nil {
			if logger.Log != nil {
				logger.Log.Fatal("Unable to load R2 SDK config", zap.Error(err))
			}
			os.Exit(1)
		}

		s3Client = s3.NewFromConfig(cfg)
		r2BucketName = os.Getenv("R2_BUCKET_NAME") // cache bucket name
		if logger.Log != nil {
			logger.Log.Info("Cloudflare R2 Client initialized")
		}
	} else {
		if logger.Log != nil {
			logger.Log.Warn("Cloudflare R2 credentials not found — object storage bypassed")
		}
	}
}



// parsedEmail holds extracted components from RFC822
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

// HandleMailIngest processes incoming mail from the edge (multipart/form-data)
func HandleMailIngest(c *fiber.Ctx) error {
	// Read metadata from form fields
	fromAddr := c.FormValue("from")
	toAddress := strings.ToLower(strings.TrimSpace(c.FormValue("to")))
	quarantineAction := c.FormValue("quarantineAction", "ACCEPT")
	spamScore := 0.0
	if v := c.FormValue("spamScore"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			spamScore = parsed
		}
	}

	// Read raw email from file field — zero base64 overhead
	fileHeader, err := c.FormFile("rawEmail")
	if err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "missing_raw_email", "Missing rawEmail file")
	}
	file, err := fileHeader.Open()
	if err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "read_error", "Cannot read rawEmail")
	}
	defer file.Close()
	rawEmailBuffer, err := io.ReadAll(file)
	if err != nil {
		return apiutil.SendError(c, fiber.StatusBadRequest, "read_error", "Failed to read email data")
	}

	parts := strings.Split(toAddress, "@")
	if len(parts) != 2 {
		return apiutil.SendError(c, fiber.StatusBadRequest, "invalid_recipient", "Invalid recipient")
	}

	localPart, domainName := parts[0], parts[1]

	// 1. Validate routing
	var domain models.Domain
	if err := db.DB.Where("domain_name = ?", domainName).First(&domain).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "domain_not_found", "Domain not found")
	}

	var mailbox models.Mailbox
	if err := db.DB.Where("local_part = ? AND domain_id = ? AND status = ?", localPart, domain.ID, "ACTIVE").First(&mailbox).Error; err != nil {
		return apiutil.SendError(c, fiber.StatusNotFound, "mailbox_not_found", "Mailbox not found")
	}

	// 2. Parse RFC822 email
	parsed := parseRFC822(rawEmailBuffer)

	// 3. Sanitize HTML body — strip XSS, scripts, iframes
	sanitizedHTML := ""
	if parsed.HTMLBody != "" {
		sanitizedHTML = htmlSanitizer.Sanitize(parsed.HTMLBody)
	}

	// 4. Upload raw email to R2
	rawKey := ""
	if s3Client != nil {
		rawKey = fmt.Sprintf("mail/%s/%s/%s.eml", domainName, localPart, uuid.New().String())

		_, err := s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
			Bucket:      aws.String(r2BucketName),
			Key:         aws.String(rawKey),
			Body:        bytes.NewReader(rawEmailBuffer),
			ContentType: aws.String("message/rfc822"),
		})
		if err != nil {
			logger.Log.Warn("Failed to upload to R2 (skipping, mail will still be saved)", zap.Error(err), zap.String("key", rawKey))
			rawKey = "" // clear key so DB record doesn't reference a missing file
		}
	}

	// 5. Determine message retention from admin settings (Redis)
	retentionHours := 24 // default 1 day
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
		FromAddress:      fromAddr,
		ToAddress:        toAddress,
		Subject:          truncateString(parsed.Subject, 500),
		TextBody:         parsed.TextBody,
		HTMLBody:         sanitizedHTML,
		S3KeyRaw:         rawKey,
		SpamScore:        spamScore,
		QuarantineAction: quarantineAction,
		ExpiresAt:        time.Now().Add(time.Duration(retentionHours) * time.Hour),
	}

	if err := db.DB.Create(&msg).Error; err != nil {
		logger.Log.Error("Failed to insert message", zap.Error(err))
		return apiutil.SendError(c, fiber.StatusInternalServerError, "database_error", "Database error")
	}

	// 7. Upload and record attachments (with configurable caps from cached settings)
	sets := getIngestSettings()
	attSaved := 0

	for _, att := range parsed.Attachments {
		// Cap: max number of attachments per message
		if attSaved >= sets.maxAttachments {
			logger.Log.Warn("Attachment count cap reached, skipping remaining",
				zap.Int("max", sets.maxAttachments), zap.Int("total", len(parsed.Attachments)))
			break
		}

		// Cap: max size per attachment
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
			_, err := s3Client.PutObject(context.TODO(), &s3.PutObjectInput{
				Bucket:      aws.String(r2BucketName),
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
			ContentType: att.ContentType,
			SizeBytes:   int64(len(att.Data)),
			S3Key:       attKey,
		}
		if err := db.DB.Create(&attachment).Error; err != nil {
			logger.Log.Error("Failed to save attachment metadata", zap.Error(err))
		} else {
			attSaved++
		}
	}

	logger.Log.Info("Message ingested",
		zap.String("id", msgID),
		zap.String("to", toAddress),
		zap.String("from", fromAddr),
		zap.Float64("spam_score", spamScore),
		zap.Int("attachments", len(parsed.Attachments)),
	)

	// 8. Fire webhook notification (async — non-blocking)
	FireMessageWebhook(mailbox.ID, msgID, toAddress, fromAddr, truncateString(parsed.Subject, 200))

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"id":          msgID,
		"status":      "ingested",
		"r2_key":      rawKey,
		"attachments": len(parsed.Attachments),
	})
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

	// Extract subject
	result.Subject = msg.Header.Get("Subject")

	// Decode MIME content type
	contentType := msg.Header.Get("Content-Type")
	if contentType == "" {
		// Plain text email
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
	} else if mediaType == "text/html" {
		body, _ := io.ReadAll(msg.Body)
		result.HTMLBody = string(body)
	} else {
		body, _ := io.ReadAll(msg.Body)
		result.TextBody = string(body)
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

		// Check for nested multipart
		if strings.HasPrefix(mediaType, "multipart/") {
			result = parseMIMEParts(bytes.NewReader(body), params["boundary"], result)
			continue
		}

		// Check if this is an attachment
		if strings.HasPrefix(disposition, "attachment") || (disposition != "" && part.FileName() != "") {
			data := body
			// Handle base64-encoded attachments
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
	// Decode MIME-encoded filenames (e.g. =?UTF-8?B?...?=)
	dec := new(mime.WordDecoder)
	if decoded, err := dec.DecodeHeader(name); err == nil {
		name = decoded
	}
	// Remove path separators and null bytes
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
