package main

import (
	"log"
	"os"

	"github.com/emersion/go-smtp"
	"go.uber.org/zap"
	"tempmail/shared/config"
	"tempmail/shared/db"
	"tempmail/shared/logger"
)

func main() {
	cfg := config.Load()

	if err := logger.InitLogger("mail-edge"); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Log.Sync()

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		logger.Log.Fatal("REDIS_URL environment variable is required")
	}

	if err := db.InitRedis(redisURL); err != nil {
		logger.Log.Fatal("Failed to connect to Redis", zap.Error(err))
	}

	// Initialize SMTP rate limiter from config
	smtpRateLimiter = NewRateLimiter(cfg.SMTP.RateLimitPerMin, smtpRateLimiter.window)

	be := &Backend{}

	s := smtp.NewServer(be)

	s.Addr = ":" + cfg.SMTP.Port
	s.Domain = cfg.SMTP.Domain
	s.MaxMessageBytes = int64(cfg.SMTP.MaxMessageSizeMB) * 1024 * 1024
	s.MaxRecipients = cfg.SMTP.MaxRecipients
	s.AllowInsecureAuth = true

	logger.Log.Info("Starting mail-edge SMTP server",
		zap.String("addr", s.Addr),
		zap.String("domain", s.Domain),
		zap.Int("maxMessageMB", cfg.SMTP.MaxMessageSizeMB),
		zap.Int("maxRecipients", cfg.SMTP.MaxRecipients),
		zap.String("timezone", cfg.App.Timezone),
	)
	if err := s.ListenAndServe(); err != nil {
		logger.Log.Fatal("SMTP server failed", zap.Error(err))
	}
}
