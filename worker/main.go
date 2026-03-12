package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hibiken/asynq"
	"go.uber.org/zap"

	appconfig "tempmail/shared/config"
	"tempmail/shared/db"
	"tempmail/shared/logger"
	"tempmail/shared/models"
)

var s3Client *s3.Client
var r2Bucket string

const (
	TypeRetentionCleanup = "maintenance:retention_cleanup"
	TypeMailboxExpire    = "maintenance:mailbox_expire"
)

func initR2() {
	r2Bucket = os.Getenv("R2_BUCKET_NAME")
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
			logger.Log.Fatal("unable to load SDK config", zap.Error(err))
		}

		s3Client = s3.NewFromConfig(cfg)
		logger.Log.Info("Cloudflare R2 Client Initialized in Worker")
	} else {
		logger.Log.Warn("Cloudflare R2 credentials not found. Object storage cleanup will be bypassed.")
	}
}

func main() {
	cfg := appconfig.Load()

	if err := logger.InitLogger("worker"); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer logger.Log.Sync()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		logger.Log.Fatal("DATABASE_URL environment variable is required")
	}

	if err := db.InitPostgres(dbURL); err != nil {
		logger.Log.Fatal("Failed to connect to PostgreSQL", zap.Error(err))
	}

	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		logger.Log.Fatal("REDIS_URL environment variable is required")
	}

	// Initialize Redis client for mailbox cleanup (SREM from active set)
	if err := db.InitRedis(redisURL); err != nil {
		logger.Log.Fatal("Failed to connect to Redis", zap.Error(err))
	}

	opt, err := asynq.ParseRedisURI(redisURL)
	if err != nil {
		logger.Log.Fatal("Failed to parse Redis URI", zap.Error(err))
	}

	initR2()

	// Initialize Server and Mux
	srv := asynq.NewServer(
		opt,
		asynq.Config{
			Concurrency: cfg.Worker.Concurrency,
			Queues: map[string]int{
				"maintenance": 10,
			},
			Logger: &zapAsynqLogger{l: logger.Log},
		},
	)

	mux := asynq.NewServeMux()
	mux.HandleFunc(TypeRetentionCleanup, HandleRetentionCleanup)
	mux.HandleFunc(TypeMailboxExpire, HandleMailboxExpire)

	// Scheduling periodic tasks
	scheduler := asynq.NewScheduler(opt, &asynq.SchedulerOpts{
		Logger: &zapAsynqLogger{l: logger.Log},
	})
	
	// Enqueue cleanup based on config schedule
	_, err = scheduler.Register(cfg.Worker.RetentionCron, asynq.NewTask(TypeRetentionCleanup, nil, asynq.Queue("maintenance")))
	if err != nil {
		logger.Log.Fatal("Failed to register retention cron", zap.Error(err))
	}

	// Enqueue mailbox expiry based on config schedule
	_, err = scheduler.Register(cfg.Worker.MailboxExpireCron, asynq.NewTask(TypeMailboxExpire, nil, asynq.Queue("maintenance")))
	if err != nil {
		logger.Log.Fatal("Failed to register mailbox expire cron", zap.Error(err))
	}

	// Run scheduler in a goroutine
	go func() {
		if err := scheduler.Run(); err != nil {
			logger.Log.Fatal("Scheduler run failed", zap.Error(err))
		}
	}()

	logger.Log.Info("Starting Asynq worker")
	if err := srv.Run(mux); err != nil {
		logger.Log.Fatal("Worker run failed", zap.Error(err))
	}
}

// ----------------------------------------------------------------------------
// HANDLERS
// ----------------------------------------------------------------------------

func HandleRetentionCleanup(ctx context.Context, t *asynq.Task) error {
	logger.Log.Info("Starting retention cleanup sweep")
	now := time.Now()

	const batchSize = 100
	var totalDeletedMsgs, totalDeletedAtts, totalDeletedR2 int

	for {
		// Process in batches to avoid OOM with large datasets
		var expiredMessages []models.Message
		if err := db.DB.Preload("Attachments").
			Where("expires_at < ?", now).
			Limit(batchSize).
			Find(&expiredMessages).Error; err != nil {
			logger.Log.Error("DB error fetching expired items", zap.Error(err))
			return err
		}

		if len(expiredMessages) == 0 {
			break // no more expired messages
		}

		for _, msg := range expiredMessages {
			// 1. Delete attachment R2 objects + DB records
			for _, att := range msg.Attachments {
				if s3Client != nil && att.S3Key != "" {
					_, err := s3Client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
						Bucket: aws.String(r2Bucket),
						Key:    aws.String(att.S3Key),
					})
					if err != nil {
						logger.Log.Error("Failed to delete attachment from R2", zap.Error(err), zap.String("key", att.S3Key))
					} else {
						totalDeletedR2++
					}
				}
				db.DB.Delete(&att)
				totalDeletedAtts++
			}

			// 2. Delete raw .eml from R2
			if s3Client != nil && msg.S3KeyRaw != "" {
				_, err := s3Client.DeleteObject(context.TODO(), &s3.DeleteObjectInput{
					Bucket: aws.String(r2Bucket),
					Key:    aws.String(msg.S3KeyRaw),
				})
				if err != nil {
					logger.Log.Error("Failed to delete R2 raw .eml", zap.Error(err), zap.String("key", msg.S3KeyRaw))
				} else {
					totalDeletedR2++
				}
			}

			// 3. Delete message from DB
			if err := db.DB.Delete(&msg).Error; err != nil {
				logger.Log.Error("Failed to delete postgres message", zap.Error(err), zap.String("id", msg.ID))
			} else {
				totalDeletedMsgs++
			}
		}

		logger.Log.Debug("Retention batch processed", zap.Int("batch_size", len(expiredMessages)))

		// If we got fewer than batchSize, we've processed everything
		if len(expiredMessages) < batchSize {
			break
		}
	}

	logger.Log.Info("Retention sweep complete",
		zap.Int("messages_deleted", totalDeletedMsgs),
		zap.Int("attachments_deleted", totalDeletedAtts),
		zap.Int("r2_objects_deleted", totalDeletedR2),
	)
	return nil
}

func HandleMailboxExpire(ctx context.Context, t *asynq.Task) error {
	logger.Log.Info("Starting mailbox expiry sweep")
	now := time.Now()

	// Fetch expiring mailboxes WITH domain info (needed for Redis key)
	var expiring []models.Mailbox
	db.DB.Preload("Domain").Where("status = ? AND expires_at < ?", "ACTIVE", now).Find(&expiring)

	if len(expiring) == 0 {
		logger.Log.Info("No mailboxes to expire")
		return nil
	}

	// Remove each from Redis active set so mail-edge rejects new mail immediately
	for _, mb := range expiring {
		fullAddress := mb.LocalPart + "@" + mb.Domain.DomainName
		db.Redis.SRem(context.Background(), "system:active_mailboxes", fullAddress)
	}

	// Bulk update status in Postgres
	ids := make([]string, len(expiring))
	for i, mb := range expiring {
		ids[i] = mb.ID
	}
	result := db.DB.Model(&models.Mailbox{}).Where("id IN ?", ids).Update("status", "DELETED")
	if result.Error != nil {
		logger.Log.Error("Failed to update mailbox statuses", zap.Error(result.Error))
		return result.Error
	}

	logger.Log.Info("Mailbox expire sweep complete",
		zap.Int64("expired", result.RowsAffected),
		zap.Int("redis_removed", len(expiring)),
	)
	return nil
}

// ----------------------------------------------------------------------------
// ASYNQ LOGGER ADAPTER (maps Asynq internal logs to our Zap core)
// ----------------------------------------------------------------------------

type zapAsynqLogger struct {
	l *zap.Logger
}

func (zl *zapAsynqLogger) Debug(args ...interface{}) {
	zl.l.Debug(fmt.Sprint(args...))
}
func (zl *zapAsynqLogger) Info(args ...interface{}) {
	zl.l.Info(fmt.Sprint(args...))
}
func (zl *zapAsynqLogger) Warn(args ...interface{}) {
	zl.l.Warn(fmt.Sprint(args...))
}
func (zl *zapAsynqLogger) Error(args ...interface{}) {
	zl.l.Error(fmt.Sprint(args...))
}
func (zl *zapAsynqLogger) Fatal(args ...interface{}) {
	zl.l.Fatal(fmt.Sprint(args...))
}
