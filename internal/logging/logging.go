// Package logging constructs the process-wide structured logger.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"modbus-scan/internal/appconfig"

	"gopkg.in/natefinch/lumberjack.v2"
)

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

// New constructs a logger with configured console and rotating-file outputs.
func New(cfg appconfig.LogConfig) (*slog.Logger, io.Closer, error) {
	var console io.Writer
	if cfg.Console {
		console = os.Stderr
	}

	var file io.Writer
	closer := io.Closer(nopCloser{})
	if strings.TrimSpace(cfg.File) != "" {
		dir := filepath.Dir(cfg.File)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, fmt.Errorf("create log directory: %w", err)
		}
		writer := &lumberjack.Logger{
			Filename:   cfg.File,
			MaxSize:    cfg.MaxSizeMB,
			MaxBackups: cfg.MaxBackups,
			MaxAge:     cfg.MaxAgeDays,
			Compress:   cfg.Compress,
		}
		file = writer
		closer = writer
	}
	logger, _, err := newWithWriters(cfg, console, file)
	if err != nil {
		_ = closer.Close()
		return nil, nil, err
	}
	return logger, closer, nil
}

func newWithWriters(cfg appconfig.LogConfig, console io.Writer, file io.Writer) (*slog.Logger, io.Closer, error) {
	writers := make([]io.Writer, 0, 2)
	if console != nil {
		writers = append(writers, console)
	}
	if file != nil {
		writers = append(writers, file)
	}
	if len(writers) == 0 {
		return nil, nil, fmt.Errorf("create logger: no output writer")
	}

	var level slog.Level
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "info", "":
		level = slog.LevelInfo
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		return nil, nil, fmt.Errorf("create logger: invalid level %q", cfg.Level)
	}

	output := io.MultiWriter(writers...)
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "json":
		handler = slog.NewJSONHandler(output, opts)
	case "text", "":
		handler = slog.NewTextHandler(output, opts)
	default:
		return nil, nil, fmt.Errorf("create logger: invalid format %q", cfg.Format)
	}
	return slog.New(handler), nopCloser{}, nil
}
