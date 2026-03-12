package logger

import (
	"os"
	"strconv"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var Log *zap.Logger

// InitLogger initializes the global zap logger.
// When LOG_FILE_PATH is "stdout" (default for containers), logs go ONLY to stdout.
// When LOG_FILE_PATH is a file path, logs go to both stdout and the rotated file.
func InitLogger(serviceName string) error {
	logFilePath := getEnvOrDefault("LOG_FILE_PATH", "stdout")
	logLevel := getEnvOrDefault("LOG_LEVEL", "info")

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	jsonEncoder := zapcore.NewJSONEncoder(encoderConfig)
	level := getZapLevel(logLevel)

	var core zapcore.Core

	if logFilePath == "stdout" || logFilePath == "" {
		// ── Container / cloud-native mode: stdout only ──
		core = zapcore.NewCore(jsonEncoder, zapcore.AddSync(os.Stdout), level)
	} else {
		// ── Traditional mode: stdout + rotated log file ──
		maxSizeMb, _ := strconv.Atoi(getEnvOrDefault("LOG_MAX_SIZE_MB", "100"))
		maxAgeDays, _ := strconv.Atoi(getEnvOrDefault("LOG_MAX_AGE_DAYS", "14"))
		maxBackups, _ := strconv.Atoi(getEnvOrDefault("LOG_MAX_BACKUPS", "10"))

		fileWriter := &lumberjack.Logger{
			Filename:   logFilePath,
			MaxSize:    maxSizeMb,
			MaxBackups: maxBackups,
			MaxAge:     maxAgeDays,
			Compress:   true,
		}

		core = zapcore.NewTee(
			zapcore.NewCore(jsonEncoder, zapcore.AddSync(os.Stdout), level),
			zapcore.NewCore(jsonEncoder, zapcore.AddSync(fileWriter), level),
		)
	}

	Log = zap.New(core, zap.AddCaller(), zap.Fields(zap.String("service", serviceName)))
	zap.ReplaceGlobals(Log)

	Log.Info("Logger initialized",
		zap.String("output", logFilePath),
		zap.String("level", logLevel))

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
