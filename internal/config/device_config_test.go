package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDeviceConfig_Valid(t *testing.T) {
	toml := `[modbus]
address         = "192.168.1.1"
port            = 1502
slave_id        = 1
byte_order      = "CDAB"
timeout_sec     = 10
scan_interval_ms = 500
`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadDeviceConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Modbus.Address != "192.168.1.1" {
		t.Errorf("Address = %q", cfg.Modbus.Address)
	}
	if cfg.Modbus.Port != 1502 {
		t.Errorf("Port = %d", cfg.Modbus.Port)
	}
	if cfg.Modbus.SlaveID != 1 {
		t.Errorf("SlaveID = %d", cfg.Modbus.SlaveID)
	}
	if cfg.Modbus.ByteOrder != "CDAB" {
		t.Errorf("ByteOrder = %q", cfg.Modbus.ByteOrder)
	}
	if cfg.Modbus.TimeoutSec != 10 {
		t.Errorf("TimeoutSec = %d", cfg.Modbus.TimeoutSec)
	}
	if cfg.Modbus.ScanIntervalMs != 500 {
		t.Errorf("ScanIntervalMs = %d", cfg.Modbus.ScanIntervalMs)
	}
}

func TestLoadDeviceConfigs_MultipleDevices(t *testing.T) {
	toml := `[[devices]]
name = "plc-1"
address = "192.168.1.1"
port = 1502
slave_id = 1
byte_order = "cdab"
timeout_sec = 10
scan_interval_ms = 500
csv = "configs/points.csv"

[[devices]]
name = "plc-2"
enabled = false
address = "192.168.1.2"
port = 1503
slave_id = 2
byte_order = "ABCD"
csv = "configs/points-coil.csv"
`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	devices, err := LoadDeviceConfigs(path, "fallback.csv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("len(devices) = %d, want 2", len(devices))
	}

	if devices[0].Name != "plc-1" {
		t.Errorf("Name = %q", devices[0].Name)
	}
	if !devices[0].Enabled {
		t.Error("first device Enabled = false, want true by default")
	}
	if devices[0].Modbus.ByteOrder != "CDAB" {
		t.Errorf("ByteOrder = %q, want CDAB", devices[0].Modbus.ByteOrder)
	}
	if devices[0].CSV != "configs/points.csv" {
		t.Errorf("CSV = %q", devices[0].CSV)
	}

	if devices[1].Enabled {
		t.Error("second device Enabled = true, want false")
	}
	if devices[1].Modbus.TimeoutSec != 5 {
		t.Errorf("default TimeoutSec = %d, want 5", devices[1].Modbus.TimeoutSec)
	}
	if devices[1].Modbus.ScanIntervalMs != 2000 {
		t.Errorf("default ScanIntervalMs = %d, want 2000", devices[1].Modbus.ScanIntervalMs)
	}
}

func TestLoadDeviceConfigs_LegacyModbusUsesDefaultCSV(t *testing.T) {
	toml := `[modbus]
address = "10.0.0.1"
port = 502
slave_id = 1
`

	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.toml")
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	devices, err := LoadDeviceConfigs(path, "points.csv")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("len(devices) = %d, want 1", len(devices))
	}
	if devices[0].Name != "default" {
		t.Errorf("Name = %q, want default", devices[0].Name)
	}
	if !devices[0].Enabled {
		t.Error("legacy device should be enabled")
	}
	if devices[0].CSV != "points.csv" {
		t.Errorf("CSV = %q", devices[0].CSV)
	}
}

func TestLoadDeviceConfig_Defaults(t *testing.T) {
	toml := `[modbus]
address = "10.0.0.1"
port    = 502
slave_id = 1
`

	dir := t.TempDir()
	path := filepath.Join(dir, "defaults.toml")
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadDeviceConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Modbus.TimeoutSec != 5 {
		t.Errorf("default TimeoutSec = %d, want 5", cfg.Modbus.TimeoutSec)
	}
	if cfg.Modbus.ScanIntervalMs != 2000 {
		t.Errorf("default ScanIntervalMs = %d, want 2000", cfg.Modbus.ScanIntervalMs)
	}
	if cfg.Modbus.ByteOrder != "ABCD" {
		t.Errorf("default ByteOrder = %q, want ABCD", cfg.Modbus.ByteOrder)
	}
}

func TestLoadDeviceConfig_InvalidByteOrder(t *testing.T) {
	toml := `[modbus]
address   = "10.0.0.1"
port      = 502
slave_id  = 1
byte_order = "INVALID"
`

	dir := t.TempDir()
	path := filepath.Join(dir, "bad_order.toml")
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadDeviceConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid byte_order")
	}
}

func TestLoadDeviceConfig_EmptyAddress(t *testing.T) {
	toml := `[modbus]
address = ""
port    = 502
slave_id = 1
`

	dir := t.TempDir()
	path := filepath.Join(dir, "empty_addr.toml")
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadDeviceConfig(path)
	if err == nil {
		t.Fatal("expected error for empty address")
	}
}

func TestLoadDeviceConfig_ZeroPort(t *testing.T) {
	toml := `[modbus]
address   = "10.0.0.1"
port      = 0
slave_id  = 1
`

	dir := t.TempDir()
	path := filepath.Join(dir, "zero_port.toml")
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadDeviceConfig(path)
	if err == nil {
		t.Fatal("expected error for port <= 0")
	}
}

func TestLoadDeviceConfig_ZeroSlaveID(t *testing.T) {
	toml := `[modbus]
address   = "10.0.0.1"
port      = 502
slave_id  = 0
`

	dir := t.TempDir()
	path := filepath.Join(dir, "zero_slave.toml")
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadDeviceConfig(path)
	if err == nil {
		t.Fatal("expected error for slave_id = 0")
	}
}

func TestLoadDeviceConfig_CaseInsensitiveByteOrder(t *testing.T) {
	toml := `[modbus]
address    = "10.0.0.1"
port       = 502
slave_id   = 1
byte_order = "cdab"
`

	dir := t.TempDir()
	path := filepath.Join(dir, "case.toml")
	if err := os.WriteFile(path, []byte(toml), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadDeviceConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Modbus.ByteOrder != "CDAB" {
		t.Errorf("ByteOrder should be uppercase, got %q", cfg.Modbus.ByteOrder)
	}
}

func TestLoadDeviceConfig_FileNotFound(t *testing.T) {
	_, err := LoadDeviceConfig("non_existent_file.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}
