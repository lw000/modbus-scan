// Package appconfig loads and validates process-level service configuration.
package appconfig

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config contains service infrastructure configuration.
type Config struct {
	Server   ServerConfig   `toml:"server"`
	Database DatabaseConfig `toml:"database"`
	Log      LogConfig      `toml:"log"`
}

// ServerConfig configures the HTTP server.
type ServerConfig struct {
	Host                 string `toml:"host"`
	Port                 int    `toml:"port"`
	ReadHeaderTimeoutSec int    `toml:"read_header_timeout_sec"`
	ReadTimeoutSec       int    `toml:"read_timeout_sec"`
	WriteTimeoutSec      int    `toml:"write_timeout_sec"`
	IdleTimeoutSec       int    `toml:"idle_timeout_sec"`
	ShutdownTimeoutSec   int    `toml:"shutdown_timeout_sec"`
}

// DatabaseConfig configures the SQLite database.
type DatabaseConfig struct {
	Path          string `toml:"path"`
	BusyTimeoutMs int    `toml:"busy_timeout_ms"`
}

// LogConfig configures structured logging and file rotation.
type LogConfig struct {
	Level      string `toml:"level"`
	Format     string `toml:"format"`
	Console    bool   `toml:"console"`
	File       string `toml:"file"`
	MaxSizeMB  int    `toml:"max_size_mb"`
	MaxBackups int    `toml:"max_backups"`
	MaxAgeDays int    `toml:"max_age_days"`
	Compress   bool   `toml:"compress"`
}

// Default returns the documented service defaults.
func Default() Config {
	return Config{
		Server: ServerConfig{
			Host:                 "127.0.0.1",
			Port:                 8080,
			ReadHeaderTimeoutSec: 5,
			ReadTimeoutSec:       15,
			WriteTimeoutSec:      30,
			IdleTimeoutSec:       60,
			ShutdownTimeoutSec:   15,
		},
		Database: DatabaseConfig{Path: "data/modbus-scan.db", BusyTimeoutMs: 5000},
		Log: LogConfig{
			Level: "info", Format: "text", Console: true,
			File: "logs/modbus-scan.log", MaxSizeMB: 50, MaxBackups: 10,
			MaxAgeDays: 30, Compress: true,
		},
	}
}

// Load reads a TOML configuration file and validates the result.
func Load(path string) (Config, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve config path: %w", err)
	}
	cfg := Default()
	if _, err := toml.DecodeFile(absPath, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode service config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	base := filepath.Dir(absPath)
	cfg.Database.Path = resolvePath(base, cfg.Database.Path)
	if cfg.Log.File != "" {
		cfg.Log.File = resolvePath(base, cfg.Log.File)
	}
	return cfg, nil
}

func resolvePath(base, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(base, path))
}

// Validate checks all externally supplied configuration values.
func (c Config) Validate() error {
	if c.Server.Host != "localhost" && net.ParseIP(c.Server.Host) == nil {
		return fmt.Errorf("server host must be an IP address or localhost")
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server port must be between 1 and 65535")
	}
	if c.Server.ReadHeaderTimeoutSec <= 0 || c.Server.ReadTimeoutSec <= 0 ||
		c.Server.WriteTimeoutSec <= 0 || c.Server.IdleTimeoutSec <= 0 ||
		c.Server.ShutdownTimeoutSec <= 0 {
		return fmt.Errorf("server timeouts must be positive")
	}
	if strings.TrimSpace(c.Database.Path) == "" {
		return fmt.Errorf("database path must not be empty")
	}
	if c.Database.BusyTimeoutMs <= 0 {
		return fmt.Errorf("database busy timeout must be positive")
	}
	level := strings.ToLower(c.Log.Level)
	if level != "debug" && level != "info" && level != "warn" && level != "error" {
		return fmt.Errorf("log level must be debug, info, warn, or error")
	}
	format := strings.ToLower(c.Log.Format)
	if format != "text" && format != "json" {
		return fmt.Errorf("log format must be text or json")
	}
	if !c.Log.Console && strings.TrimSpace(c.Log.File) == "" {
		return fmt.Errorf("at least one log output must be enabled")
	}
	if c.Log.File != "" && (c.Log.MaxSizeMB <= 0 || c.Log.MaxBackups < 0 || c.Log.MaxAgeDays < 0) {
		return fmt.Errorf("log rotation values are invalid")
	}
	return nil
}
