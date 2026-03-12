package db

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	tempmailLogger "tempmail/shared/logger"
)

var (
	DB    *gorm.DB
	Redis *redis.Client
)

// InitPostgres connects to the primary database
func InitPostgres(dsn string) error {
	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger:                 logger.Default.LogMode(logger.Warn),
		SkipDefaultTransaction: true, // ~30% faster for single-row writes (no implicit BEGIN/COMMIT)
		PrepareStmt:            true, // cache prepared statements for repeated queries
	})
	if err != nil {
		return err
	}

	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}

	// Connection pool tuning for production load
	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(30 * time.Minute) // prevent stale connections
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)  // reclaim idle connections

	if tempmailLogger.Log != nil {
		tempmailLogger.Log.Info("PostgreSQL connected successfully")
	}
	return nil
}

// InitRedis connects to the caching layer
func InitRedis(url string) error {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return err
	}

	// Pool tuning for high-concurrency workloads
	opts.PoolSize = 200
	opts.MinIdleConns = 30

	Redis = redis.NewClient(opts)

	if err := Redis.Ping(context.Background()).Err(); err != nil {
		return err
	}

	if tempmailLogger.Log != nil {
		tempmailLogger.Log.Info("Redis connected successfully", zap.String("url", url))
	}
	return nil
}
