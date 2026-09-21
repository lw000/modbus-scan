package appconfig

import (
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
	cfg, err := Load(writeConfig(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Host != "127.0.0.1" || cfg.Server.Port != 8080 {
		t.Fatalf("server defaults = %s:%d", cfg.Server.Host, cfg.Server.Port)
	}
	if cfg.Database.Path != "data/modbus-scan.db" {
		t.Fatalf("database path = %q", cfg.Database.Path)
	}
	if cfg.Log.Level != "info" || cfg.Log.Format != "text" {
		t.Fatalf("log defaults = %q/%q", cfg.Log.Level, cfg.Log.Format)
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
