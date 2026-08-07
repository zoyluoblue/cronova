package main

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
)

// buildLogger constructs the process-wide slog.Logger from the log config and
// re-points the stdlib `log` package at it, so every line this binary emits —
// scheduler slog, API access log, and main's own log.Printf — shares one
// stream, one level filter, and one format (text or JSON).
func buildLogger(level, format string) (*slog.Logger, error) {
	var lvl slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		lvl = slog.LevelInfo
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("invalid log.level %q (use debug|info|warn|error)", level)
	}
	var h slog.Handler
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "text":
		h = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	case "json":
		h = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	default:
		return nil, fmt.Errorf("invalid log.format %q (use text|json)", format)
	}
	logger := slog.New(h)
	slog.SetDefault(logger)
	// Bridge the stdlib logger (main.go's log.Printf lines) into the same
	// handler at info level, stripping its own timestamp prefix.
	log.SetFlags(0)
	log.SetOutput(slogWriter{logger})
	return logger, nil
}

// slogWriter adapts io.Writer (stdlib log output) onto a slog.Logger.
type slogWriter struct{ l *slog.Logger }

func (w slogWriter) Write(p []byte) (int, error) {
	w.l.Info(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
