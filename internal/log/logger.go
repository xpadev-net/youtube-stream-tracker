package log

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var logger *zap.Logger

// Init initializes the global logger with JSON output.
func Init(environment string) error {
	var cfg zap.Config

	if environment == "production" {
		cfg = zap.NewProductionConfig()
	} else {
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	cfg.EncoderConfig.TimeKey = "timestamp"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.OutputPaths = []string{"stdout"}
	cfg.ErrorOutputPaths = []string{"stderr"}

	var err error
	logger, err = cfg.Build(zap.AddCallerSkip(1))
	if err != nil {
		return err
	}

	return nil
}

// InitJSON initializes the global logger with JSON output regardless of environment.
func InitJSON() error {
	cfg := zap.NewProductionConfig()
	cfg.EncoderConfig.TimeKey = "timestamp"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.OutputPaths = []string{"stdout"}
	cfg.ErrorOutputPaths = []string{"stderr"}

	var err error
	logger, err = cfg.Build(zap.AddCallerSkip(1))
	if err != nil {
		return err
	}

	return nil
}

// Logger returns the global logger instance.
func Logger() *zap.Logger {
	if logger == nil {
		// Fallback to a default logger if Init hasn't been called
		logger, _ = zap.NewProduction()
	}
	return logger
}

// Sync flushes any buffered log entries.
func Sync() {
	if logger != nil {
		_ = logger.Sync()
	}
}

// Info logs an info message.
func Info(msg string, fields ...zap.Field) {
	Logger().Info(msg, fields...)
}

// Error logs an error message.
func Error(msg string, fields ...zap.Field) {
	Logger().Error(msg, fields...)
}

// Warn logs a warning message.
func Warn(msg string, fields ...zap.Field) {
	Logger().Warn(msg, fields...)
}

// Debug logs a debug message.
func Debug(msg string, fields ...zap.Field) {
	Logger().Debug(msg, fields...)
}

// Fatal logs a fatal message and exits.
func Fatal(msg string, fields ...zap.Field) {
	Logger().Fatal(msg, fields...)
	os.Exit(1)
}

// With creates a child logger with the given fields.
func With(fields ...zap.Field) *zap.Logger {
	return Logger().With(fields...)
}
