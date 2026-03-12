package db

import (
	"context"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"tempmail/shared/config"
	tempmailLogger "tempmail/shared/logger"
)

var (
	DB    *gorm.DB
	Redis *redis.Client
)

// InitPostgres connects to the primary database using dynamic pool sizes from config.
func InitPostgres(dsn string) error {
	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger:                 logger.Default.LogMode(logger.Warn),
		SkipDefaultTransaction: true,
		PrepareStmt:            true,
	})
	if err != nil {
		return err
	}

	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}

	// Dynamic pool — scales with CPU count, overridable via env
	cfg := config.App
	sqlDB.SetMaxOpenConns(cfg.DB.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.DB.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.DB.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.DB.ConnMaxIdleTime)

	if tempmailLogger.Log != nil {
		tempmailLogger.Log.Info("PostgreSQL connected",
			zap.Int("maxOpen", cfg.DB.MaxOpenConns),
			zap.Int("maxIdle", cfg.DB.MaxIdleConns),
		)
	}
	return nil
}

// InitRedis connects to the caching layer using dynamic pool sizes from config.
func InitRedis(url string) error {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return err
	}

	// Dynamic pool — scales with CPU count, overridable via env
	cfg := config.App
	opts.PoolSize = cfg.Redis.PoolSize
	opts.MinIdleConns = cfg.Redis.MinIdleConns

	Redis = redis.NewClient(opts)

	if err := Redis.Ping(context.Background()).Err(); err != nil {
		return err
	}

	if tempmailLogger.Log != nil {
		tempmailLogger.Log.Info("Redis connected",
			zap.String("url", url),
			zap.Int("poolSize", cfg.Redis.PoolSize),
			zap.Int("minIdle", cfg.Redis.MinIdleConns),
		)
	}
	return nil
}
