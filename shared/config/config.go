package config

import (
	"os"
	"runtime"
	"strconv"
	"time"
)

// ---------------------------------------------------------------------------
// Config holds all application configuration loaded from environment variables
// with sensible defaults. This is the single source of truth for all startup
// parameters — NO magic numbers in application code.
//
// Go community standard: https://12factor.net/config
// ---------------------------------------------------------------------------

// App holds the global configuration instance, initialized once at startup.
var App *Config

// Config is the root configuration struct.
type Config struct {
	App      AppConfig
	API      APIConfig
	SMTP     SMTPConfig
	Worker   WorkerConfig
	Security SecurityConfig
	DB       DBConfig
	Redis    RedisConfig
}

// DBConfig holds PostgreSQL connection pool configuration.
type DBConfig struct {
	MaxOpenConns    int `env:"DB_MAX_OPEN_CONNS"`
	MaxIdleConns    int `env:"DB_MAX_IDLE_CONNS"`
	ConnMaxLifetime time.Duration `env:"DB_CONN_MAX_LIFETIME"`
	ConnMaxIdleTime time.Duration `env:"DB_CONN_MAX_IDLE_TIME"`
}

// RedisConfig holds Redis connection pool configuration.
type RedisConfig struct {
	PoolSize     int `env:"REDIS_POOL_SIZE"`
	MinIdleConns int `env:"REDIS_MIN_IDLE_CONNS"`
}

// APIConfig holds API server configuration.
type APIConfig struct {
	Port                 string        `env:"PORT"                    default:"4000"`
	BodyLimitMB          int           `env:"BODY_LIMIT_MB"           default:"40"`
	Concurrency          int           `env:"API_CONCURRENCY"`
	PublicRateLimitPerMin int          `env:"PUBLIC_RATE_LIMIT"       default:"60"`
	LoginRateLimitPerMin  int          `env:"LOGIN_RATE_LIMIT"        default:"10"`
	ReadTimeout          time.Duration `env:"API_READ_TIMEOUT"        default:"30s"`
	WriteTimeout         time.Duration `env:"API_WRITE_TIMEOUT"       default:"30s"`
	IdleTimeout          time.Duration `env:"API_IDLE_TIMEOUT"        default:"120s"`
}

// SMTPConfig holds mail-edge SMTP server configuration.
type SMTPConfig struct {
	Port               string        `env:"SMTP_PORT"               default:"2525"`
	Domain             string        `env:"SMTP_DOMAIN"             default:"tempmail.local"`
	MaxMessageSizeMB   int           `env:"SMTP_MAX_MESSAGE_MB"     default:"25"`
	MaxRecipients      int           `env:"SMTP_MAX_RECIPIENTS"     default:"50"`
	RateLimitPerMin    int           `env:"SMTP_RATE_LIMIT"         default:"50"`
	RspamdTimeout      time.Duration `env:"RSPAMD_TIMEOUT"          default:"10s"`
	IngestTimeout      time.Duration `env:"INGEST_TIMEOUT"          default:"30s"`
}

// WorkerConfig holds background worker configuration.
type WorkerConfig struct {
	Concurrency        int    `env:"WORKER_CONCURRENCY"`
	IngestPriority     int    `env:"WORKER_INGEST_PRIORITY"    default:"60"`
	MaintenancePriority int   `env:"WORKER_MAINT_PRIORITY"     default:"10"`
	RetentionCron      string `env:"RETENTION_CRON"             default:"@hourly"`
	MailboxExpireCron   string `env:"MAILBOX_EXPIRE_CRON"        default:"*/5 * * * *"`
}

// SecurityConfig holds security-related configuration.
type SecurityConfig struct {
	AdminAPIKey        string `env:"ADMIN_API_KEY"            default:""`
	AdminUsername      string `env:"ADMIN_USERNAME"           default:"admin"`
	AdminPanelPath     string `env:"ADMIN_PANEL_PATH"         default:""`
}

// AppConfig holds general application configuration.
type AppConfig struct {
	Timezone string `env:"TZ" default:"Asia/Bangkok"`
}

// Load reads environment variables and populates the global Config.
// Call this once at application startup.
// cpuScale returns a dynamic multiplier based on available CPUs.
// On 1 CPU → 1x, on 4 CPU → 4x, etc. Pools scale linearly.
func cpuScale(base int) int {
	n := runtime.NumCPU()
	result := base * n
	if result < base {
		return base
	}
	return result
}

func Load() *Config {
	tz := envStr("TZ", "Asia/Bangkok")
	os.Setenv("TZ", tz)
	if loc, err := time.LoadLocation(tz); err == nil {
		time.Local = loc
	}

	// Dynamic defaults based on CPU count:
	// 1 CPU: PG=25, Redis=50, Worker=10
	// 2 CPU: PG=50, Redis=100, Worker=20
	// 4 CPU: PG=100, Redis=200, Worker=40
	cfg := &Config{
		App: AppConfig{
			Timezone: tz,
		},
		API: APIConfig{
			Port:                 envStr("PORT", "4000"),
			BodyLimitMB:          envInt("BODY_LIMIT_MB", 40),
			Concurrency:          envInt("API_CONCURRENCY", cpuScale(256*1024)),
			PublicRateLimitPerMin: envInt("PUBLIC_RATE_LIMIT", 60),
			LoginRateLimitPerMin:  envInt("LOGIN_RATE_LIMIT", 10),
			ReadTimeout:          envDuration("API_READ_TIMEOUT", 15*time.Second),
			WriteTimeout:         envDuration("API_WRITE_TIMEOUT", 0), // Must be 0 for long-lived SSE
			IdleTimeout:          envDuration("API_IDLE_TIMEOUT", 60*time.Second),
		},
		SMTP: SMTPConfig{
			Port:             envStr("SMTP_PORT", "2525"),
			Domain:           envStr("SMTP_DOMAIN", "tempmail.local"),
			MaxMessageSizeMB: envInt("SMTP_MAX_MESSAGE_MB", 25),
			MaxRecipients:    envInt("SMTP_MAX_RECIPIENTS", 50),
			RateLimitPerMin:  envInt("SMTP_RATE_LIMIT", 50),
			RspamdTimeout:    envDuration("RSPAMD_TIMEOUT", 10*time.Second),
			IngestTimeout:    envDuration("INGEST_TIMEOUT", 30*time.Second),
		},
		Worker: WorkerConfig{
			Concurrency:        envInt("WORKER_CONCURRENCY", cpuScale(10)),
			IngestPriority:     envInt("WORKER_INGEST_PRIORITY", 60),
			MaintenancePriority: envInt("WORKER_MAINT_PRIORITY", 10),
			RetentionCron:      envStr("RETENTION_CRON", "@hourly"),
			MailboxExpireCron:   envStr("MAILBOX_EXPIRE_CRON", "*/5 * * * *"),
		},
		Security: SecurityConfig{
			AdminAPIKey:    envStr("ADMIN_API_KEY", ""),
			AdminUsername:  envStr("ADMIN_USERNAME", "admin"),
			AdminPanelPath: envStr("ADMIN_PANEL_PATH", ""),
		},
		DB: DBConfig{
			MaxOpenConns:    envInt("DB_MAX_OPEN_CONNS", cpuScale(25)),
			MaxIdleConns:    envInt("DB_MAX_IDLE_CONNS", cpuScale(5)),
			ConnMaxLifetime: envDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute),
			ConnMaxIdleTime: envDuration("DB_CONN_MAX_IDLE_TIME", 5*time.Minute),
		},
		Redis: RedisConfig{
			PoolSize:     envInt("REDIS_POOL_SIZE", cpuScale(50)),
			MinIdleConns: envInt("REDIS_MIN_IDLE_CONNS", cpuScale(5)),
		},
	}

	App = cfg
	return cfg
}

// BodyLimitBytes returns BodyLimitMB converted to bytes.
func (c *APIConfig) BodyLimitBytes() int {
	return c.BodyLimitMB * 1024 * 1024
}

// MaxMessageBytes returns MaxMessageSizeMB converted to bytes.
func (c *SMTPConfig) MaxMessageBytes() int64 {
	return int64(c.MaxMessageSizeMB) * 1024 * 1024
}

// ---------------------------------------------------------------------------
// Environment variable helpers
// ---------------------------------------------------------------------------

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
