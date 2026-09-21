package logging

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modbus-scan/internal/appconfig"
)

func TestNewCreatesLogDirectoryAndFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "service.log")
	logger, closer, err := New(appconfig.LogConfig{
		Level: "info", Format: "text", File: path,
		MaxSizeMB: 1, MaxBackups: 1, MaxAgeDays: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	logger.Info("file-message")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "file-message") {
		t.Fatalf("file output = %q", data)
	}
}

func TestNewWithWritersFiltersDebug(t *testing.T) {
	var output bytes.Buffer
	logger, closer, err := newWithWriters(appconfig.LogConfig{Level: "info", Format: "json"}, &output, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	logger.DebugContext(context.Background(), "hidden")
	logger.InfoContext(context.Background(), "visible", slog.String("component", "test"))
	if strings.Contains(output.String(), "hidden") || !strings.Contains(output.String(), "visible") {
		t.Fatalf("unexpected output %q", output.String())
	}
}

func TestNewWithWritersTextFormat(t *testing.T) {
	var output bytes.Buffer
	logger, closer, err := newWithWriters(appconfig.LogConfig{Level: "debug", Format: "text"}, &output, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	logger.Debug("debug-message")
	if !strings.Contains(output.String(), "debug-message") {
		t.Fatalf("output = %q", output.String())
	}
}
