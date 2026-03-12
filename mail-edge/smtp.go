package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-smtp"
	"go.uber.org/zap"
	"tempmail/shared/config"
	"tempmail/shared/db"
	"tempmail/shared/logger"
)

// ---------------------------------------------------------------------------
// RATE LIMITER — per-IP connection tracking
// ---------------------------------------------------------------------------

type RateLimiter struct {
	mu       sync.Mutex
	counters map[string]*ipCounter
	limit    int
	window   time.Duration
}

type ipCounter struct {
	count    int
	resetAt  time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		counters: make(map[string]*ipCounter),
		limit:    limit,
		window:   window,
	}
	// Background cleanup of stale entries
	go func() {
		ticker := time.NewTicker(window)
		defer ticker.Stop()
		for range ticker.C {
			rl.mu.Lock()
			now := time.Now()
			for ip, c := range rl.counters {
				if now.After(c.resetAt) {
					delete(rl.counters, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	c, exists := rl.counters[ip]
	if !exists || now.After(c.resetAt) {
		rl.counters[ip] = &ipCounter{count: 1, resetAt: now.Add(rl.window)}
		return true
	}
	c.count++
	return c.count <= rl.limit
}

// Global rate limiter: 50 connections per IP per minute
var smtpRateLimiter = NewRateLimiter(50, time.Minute)

// ---------------------------------------------------------------------------
// SMTP BACKEND
// ---------------------------------------------------------------------------

type Backend struct{}

func (bkd *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	remoteAddr := c.Conn().RemoteAddr().String()
	ip := strings.Split(remoteAddr, ":")[0]

	if !smtpRateLimiter.Allow(ip) {
		logger.Log.Warn("SMTP rate limit exceeded", zap.String("ip", ip))
		return nil, &smtp.SMTPError{
			Code:         421,
			EnhancedCode: smtp.EnhancedCode{4, 7, 0},
			Message:      "Too many connections, try again later",
		}
	}

	logger.Log.Debug("New SMTP session", zap.String("remote", remoteAddr))
	return &Session{RemoteIP: ip}, nil
}

// ---------------------------------------------------------------------------
// SMTP SESSION
// ---------------------------------------------------------------------------

type Session struct {
	From     string
	To       string
	RemoteIP string
}

// AuthPlain — no authentication required for inbound MTA (receive-only)
func (s *Session) AuthPlain(username, password string) error {
	return smtp.ErrAuthUnsupported
}

func (s *Session) Mail(from string, opts *smtp.MailOptions) error {
	logger.Log.Debug("MAIL FROM", zap.String("from", from), zap.String("ip", s.RemoteIP))
	s.From = from
	return nil
}

func (s *Session) Rcpt(to string, opts *smtp.RcptOptions) error {
	to = strings.ToLower(strings.TrimSpace(to))
	logger.Log.Debug("Validating RCPT", zap.String("to", to))

	// O(1) Redis validation — no dev bypass, always enforced
	isValid, err := db.Redis.SIsMember(context.Background(), "system:active_mailboxes", to).Result()
	if err != nil {
		logger.Log.Error("Redis error during RCPT validation", zap.Error(err))
		return &smtp.SMTPError{
			Code:         451,
			EnhancedCode: smtp.EnhancedCode{4, 3, 0},
			Message:      "Temporary internal error",
		}
	}

	if !isValid {
		logger.Log.Info("Rejected unknown recipient", zap.String("to", to), zap.String("ip", s.RemoteIP))
		return &smtp.SMTPError{
			Code:         550,
			EnhancedCode: smtp.EnhancedCode{5, 1, 1},
			Message:      "Mailbox unavailable",
		}
	}

	logger.Log.Info("Accepted recipient", zap.String("to", to))
	s.To = to
	return nil
}

func (s *Session) Data(r io.Reader) error {
	// Enforce max message size from config
	maxSize := config.App.SMTP.MaxMessageBytes()
	limitedReader := io.LimitReader(r, maxSize+1)

	buf := new(bytes.Buffer)
	n, err := io.Copy(buf, limitedReader)
	if err != nil {
		logger.Log.Error("Error reading DATA", zap.Error(err))
		return err
	}
	if n > maxSize {
		logger.Log.Warn("Message exceeds size limit", zap.Int64("size", n), zap.String("from", s.From))
		return &smtp.SMTPError{
			Code:         552,
			EnhancedCode: smtp.EnhancedCode{5, 3, 4},
			Message:      "Message size exceeds limit",
		}
	}

	rawBytes := buf.Bytes()

	// Real Rspamd integration — FAIL-CLOSE: reject if Rspamd is unreachable
	spamScore, action, err := checkRspamd(rawBytes, s.From, s.RemoteIP)
	if err != nil {
		logger.Log.Error("Rspamd check failed — rejecting message (fail-close policy)", zap.Error(err))
		return &smtp.SMTPError{
			Code:         451,
			EnhancedCode: smtp.EnhancedCode{4, 7, 1},
			Message:      "Spam check temporarily unavailable, try again later",
		}
	}

	spamThreshold := getSpamThreshold()

	if action == "reject" || spamScore > spamThreshold {
		logger.Log.Warn("Message rejected as spam",
			zap.Float64("score", spamScore),
			zap.String("action", action),
			zap.String("from", s.From),
			zap.String("ip", s.RemoteIP),
		)
		return &smtp.SMTPError{
			Code:         550,
			EnhancedCode: smtp.EnhancedCode{5, 7, 1},
			Message:      "Message rejected: spam detected",
		}
	}

	quarantine := "ACCEPT"
	if action == "add header" || action == "soft reject" || action == "greylist" {
		quarantine = "QUARANTINE"
	}

	// Post to internal API
	if err := pushToAPI(s.From, s.To, rawBytes, spamScore, quarantine); err != nil {
		logger.Log.Error("API submission failed", zap.Error(err), zap.String("from", s.From))
		return &smtp.SMTPError{
			Code:         451,
			EnhancedCode: smtp.EnhancedCode{4, 3, 0},
			Message:      "Temporary processing failure",
		}
	}

	logger.Log.Info("Message accepted",
		zap.String("from", s.From),
		zap.String("to", s.To),
		zap.Float64("spam_score", spamScore),
		zap.String("action", quarantine),
		zap.Int("size_bytes", len(rawBytes)),
	)

	return nil
}

func (s *Session) Reset() {}

func (s *Session) Logout() error {
	return nil
}

// ---------------------------------------------------------------------------
// RSPAMD INTEGRATION
// ---------------------------------------------------------------------------

type rspamdResponse struct {
	Score         float64                       `json:"score"`
	RequiredScore float64                      `json:"required_score"`
	Action        string                       `json:"action"`
	Symbols       map[string]rspamdSymbol      `json:"symbols"`
}

type rspamdSymbol struct {
	Name        string  `json:"name"`
	Score       float64 `json:"score"`
	Description string  `json:"description"`
}

func checkRspamd(rawEmail []byte, from string, ip string) (float64, string, error) {
	rspamdURL := os.Getenv("RSPAMD_URL")
	if rspamdURL == "" {
		rspamdURL = "http://rspamd:11333"
	}

	req, err := http.NewRequest("POST", rspamdURL+"/checkv2", bytes.NewReader(rawEmail))
	if err != nil {
		return 0, "", fmt.Errorf("rspamd request creation failed: %w", err)
	}

	req.Header.Set("From", from)
	req.Header.Set("IP", ip)
	req.Header.Set("Content-Type", "message/rfc822")

	if password := os.Getenv("RSPAMD_PASSWORD"); password != "" {
		req.Header.Set("Password", password)
	}

	client := &http.Client{Timeout: config.App.SMTP.RspamdTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("rspamd request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("rspamd returned status %d", resp.StatusCode)
	}

	var result rspamdResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, "", fmt.Errorf("rspamd response decode failed: %w", err)
	}

	logger.Log.Debug("Rspamd check result",
		zap.Float64("score", result.Score),
		zap.String("action", result.Action),
		zap.Float64("required_score", result.RequiredScore),
	)

	return result.Score, result.Action, nil
}

func getSpamThreshold() float64 {
	threshold := os.Getenv("SPAM_REJECT_THRESHOLD")
	if threshold == "" {
		return 15.0 // Default high threshold, Rspamd action is primary
	}
	val, err := strconv.ParseFloat(threshold, 64)
	if err != nil {
		return 15.0
	}
	return val
}

// ---------------------------------------------------------------------------
// INTERNAL API PUSH
// ---------------------------------------------------------------------------

func pushToAPI(from, to string, rawData []byte, spamScore float64, action string) error {
	apiURL := os.Getenv("INTERNAL_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:4000/internal/mail/ingest"
	}
	apiToken, err := db.Redis.Get(context.Background(), "system:api_token").Result()
	if err != nil || apiToken == "" {
		return errors.New("API_TOKEN not found in Redis — create a key from admin panel")
	}

	// Build multipart/form-data — no base64 overhead
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)

	// Metadata fields
	writer.WriteField("from", from)
	writer.WriteField("to", to)
	writer.WriteField("spamScore", strconv.FormatFloat(spamScore, 'f', 4, 64))
	writer.WriteField("quarantineAction", action)

	// Raw email as binary file part — zero encoding overhead
	part, err := writer.CreateFormFile("rawEmail", "message.eml")
	if err != nil {
		return fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := part.Write(rawData); err != nil {
		return fmt.Errorf("failed to write raw email: %w", err)
	}
	writer.Close()

	req, err := http.NewRequest("POST", apiURL, body)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+apiToken)

	client := &http.Client{Timeout: config.App.SMTP.IngestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
