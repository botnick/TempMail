package logger

import (
	"os"
	"strconv"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var Log *zap.Logger

// InitLogger initializes the high-performance global zap logger with lumberjack rotation
func InitLogger(serviceName string) error {
	logFilePath := getEnvOrDefault("LOG_FILE_PATH", "/var/log/tempmail/"+serviceName+".log")
	maxSizeMb, _ := strconv.Atoi(getEnvOrDefault("LOG_MAX_SIZE_MB", "100"))
	maxAgeDays, _ := strconv.Atoi(getEnvOrDefault("LOG_MAX_AGE_DAYS", "14"))
	maxBackups, _ := strconv.Atoi(getEnvOrDefault("LOG_MAX_BACKUPS", "10"))
	logLevel := getEnvOrDefault("LOG_LEVEL", "info")

	// Lumberjack handles log rotation and auto-deletion based on MaxAge
	lumberjackLogger := &lumberjack.Logger{
		Filename:   logFilePath,
		MaxSize:    maxSizeMb,  // megabytes
		MaxBackups: maxBackups, // number of old logs to keep
		MaxAge:     maxAgeDays,    // days to retain old logs before auto-deleting
		Compress:   true,       // compress rotated logs
	}

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	var core zapcore.Core

	// In development, log to console + file. In prod, mostly file or stdout depending on docker setup.
	if os.Getenv("NODE_ENV") == "development" {
		consoleEncoder := zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
		jsonEncoder := zapcore.NewJSONEncoder(encoderConfig)

		core = zapcore.NewTee(
			zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stdout), getZapLevel(logLevel)),
			zapcore.NewCore(jsonEncoder, zapcore.AddSync(lumberjackLogger), getZapLevel(logLevel)),
		)
	} else {
		// Production high-performance JSON writing
		jsonEncoder := zapcore.NewJSONEncoder(encoderConfig)
		// Usually in Docker you write to stdout, but we are adding file-sync logic per user request
		core = zapcore.NewTee(
			zapcore.NewCore(jsonEncoder, zapcore.AddSync(os.Stdout), getZapLevel(logLevel)),
			zapcore.NewCore(jsonEncoder, zapcore.AddSync(lumberjackLogger), getZapLevel(logLevel)),
		)
	}

	Log = zap.New(core, zap.AddCaller(), zap.Fields(zap.String("service", serviceName)))
	
	// Replace global zap logger
	zap.ReplaceGlobals(Log)

	Log.Info("Logger initialized successfully", 
		zap.String("path", logFilePath), 
		zap.Int("retention_days", maxAgeDays),
		zap.Int("max_size_mb", maxSizeMb))

	return nil
}

func getEnvOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getZapLevel(lvl string) zapcore.Level {
	switch lvl {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}
