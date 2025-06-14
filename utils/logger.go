package utils

import (
	"log/slog"
)

// Log provides structured logging with subsystem identification
// Example usage:
//
//	utils.Log(slog.LevelDebug, "sse", "Connection established", "payment_id", paymentID, "connection_count", 3)
//	utils.Log(slog.LevelInfo, "stripe", "Payment succeeded", "payment_id", paymentID, "amount", 50.00)
func Log(level slog.Level, subsystem string, msg string, keysAndValues ...interface{}) {
	attrs := []slog.Attr{
		slog.String("subsystem", subsystem),
	}

	// Convert key-value pairs to slog attributes
	for i := 0; i < len(keysAndValues); i += 2 {
		if i+1 < len(keysAndValues) {
			key := keysAndValues[i].(string)
			value := keysAndValues[i+1]
			attrs = append(attrs, slog.Any(key, value))
		}
	}

	slog.LogAttrs(nil, level, msg, attrs...)
}

// Convenience functions for common log levels
func Debug(subsystem string, msg string, keysAndValues ...interface{}) {
	Log(slog.LevelDebug, subsystem, msg, keysAndValues...)
}

func Info(subsystem string, msg string, keysAndValues ...interface{}) {
	Log(slog.LevelInfo, subsystem, msg, keysAndValues...)
}

func Warn(subsystem string, msg string, keysAndValues ...interface{}) {
	Log(slog.LevelWarn, subsystem, msg, keysAndValues...)
}

func Error(subsystem string, msg string, keysAndValues ...interface{}) {
	Log(slog.LevelError, subsystem, msg, keysAndValues...)
}
