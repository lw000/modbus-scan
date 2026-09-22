package appconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	path := writeConfig(t, "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Host != "127.0.0.1" || cfg.Server.Port != 8080 {
		t.Fatalf("server defaults = %s:%d", cfg.Server.Host, cfg.Server.Port)
	}
	if want := filepath.Join(filepath.Dir(path), "data", "modbus-scan.db"); cfg.Database.Path != want {
		t.Fatalf("database path = %q, want %q", cfg.Database.Path, want)
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "text" {
		t.Fatalf("log defaults = %q/%q", cfg.Log.Level, cfg.Log.Format)
	}
}

func TestLoadResolvesRuntimePathsRelativeToConfig(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "configs")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, "config.toml")
	content := `[database]
path = "../data/app.db"

[log]
console = false
file = "../logs/app.log"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "data", "app.db"); cfg.Database.Path != want {
		t.Fatalf("database path = %q, want %q", cfg.Database.Path, want)
	}
	if want := filepath.Join(root, "logs", "app.log"); cfg.Log.File != want {
		t.Fatalf("log path = %q, want %q", cfg.Log.File, want)
	}
}

func TestLoadPreservesAbsoluteRuntimePaths(t *testing.T) {
	root := t.TempDir()
	databasePath := filepath.Join(root, "data", "app.db")
	logPath := filepath.Join(root, "logs", "app.log")
	path := writeConfig(t, "[database]\npath = "+fmt.Sprintf("%q", databasePath)+
		"\n[log]\nconsole = false\nfile = "+fmt.Sprintf("%q", logPath)+"\n")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Path != filepath.Clean(databasePath) {
		t.Fatalf("database path = %q, want %q", cfg.Database.Path, filepath.Clean(databasePath))
	}
	if cfg.Log.File != filepath.Clean(logPath) {
		t.Fatalf("log path = %q, want %q", cfg.Log.File, filepath.Clean(logPath))
	}
}

func TestLoadCompleteConfiguration(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
[server]
host = "0.0.0.0"
port = 9000
read_header_timeout_sec = 2
read_timeout_sec = 3
write_timeout_sec = 4
idle_timeout_sec = 5
shutdown_timeout_sec = 6

[database]
path = "var/app.db"
busy_timeout_ms = 1234

[log]
level = "debug"
format = "json"
console = false
file = "var/app.log"
max_size_mb = 20
max_backups = 4
max_age_days = 7
compress = false
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Port != 9000 || cfg.Database.BusyTimeoutMs != 1234 || cfg.Log.MaxBackups != 4 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := map[string]string{
		"port":       "[server]\nport = 70000\n",
		"host":       "[server]\nhost = \"not a host\"\n",
		"database":   "[database]\npath = \" \"\n",
		"log level":  "[log]\nlevel = \"trace\"\n",
		"log format": "[log]\nformat = \"yaml\"\n",
		"timeout":    "[server]\nread_timeout_sec = -1\n",
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, content)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
