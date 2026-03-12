package main

import (
	"log"
	"os"

	"github.com/emersion/go-smtp"
	"go.uber.org/zap"
	"tempmail/shared/db"
	"tempmail/shared/logger"
)

func main() {
	if err := logger.InitLogger("mail-edge"); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Log.Sync()

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}

	if err := db.InitRedis(redisURL); err != nil {
		logger.Log.Fatal("Failed to connect to Redis", zap.Error(err))
	}

	be := &Backend{}

	s := smtp.NewServer(be)

	s.Addr = ":2525" // bind internally to 2525
	if port := os.Getenv("PORT"); port != "" {
		s.Addr = ":" + port
	}

	s.Domain = "tempmail.local"
	s.MaxMessageBytes = 25 * 1024 * 1024
	s.MaxRecipients = 50
	s.AllowInsecureAuth = true

	logger.Log.Info("Starting mail-edge SMTP server", zap.String("addr", s.Addr))
	if err := s.ListenAndServe(); err != nil {
		logger.Log.Fatal("SMTP server failed", zap.Error(err))
	}
}
