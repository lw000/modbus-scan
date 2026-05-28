package config

import (
	"fmt"
	"log"
	"strings"

	"github.com/BurntSushi/toml"
)

// DeviceConfig Modbus 设备连接配置
type DeviceConfig struct {
	Modbus struct {
		Address        string `toml:"address"`
		Port           int    `toml:"port"`
		SlaveID        byte   `toml:"slave_id"`
		ByteOrder      string `toml:"byte_order"`
		TimeoutSec     int    `toml:"timeout_sec"`
		ScanIntervalMs int    `toml:"scan_interval_ms"`
	} `toml:"modbus"`
}

// LoadDeviceConfig 从 TOML 文件加载设备连接配置并进行校验
func LoadDeviceConfig(filePath string) (*DeviceConfig, error) {
	var cfg DeviceConfig
	if _, err := toml.DecodeFile(filePath, &cfg); err != nil {
		return nil, fmt.Errorf("解析 TOML 配置文件失败: %w", err)
	}
	if cfg.Modbus.Address == "" || cfg.Modbus.Port <= 0 {
		return nil, fmt.Errorf("TOML 配置错误: address 和 port 必须有效")
	}
	if cfg.Modbus.SlaveID == 0 {
		return nil, fmt.Errorf("TOML 配置错误: slave_id 必须为 1-247 之间的有效值")
	}
	if cfg.Modbus.TimeoutSec <= 0 {
		cfg.Modbus.TimeoutSec = 5
	}
	if cfg.Modbus.ScanIntervalMs <= 0 {
		cfg.Modbus.ScanIntervalMs = 2000
	}
	if cfg.Modbus.ByteOrder == "" {
		cfg.Modbus.ByteOrder = "ABCD"
	}
	cfg.Modbus.ByteOrder = strings.ToUpper(cfg.Modbus.ByteOrder)

	validOrders := map[string]bool{"ABCD": true, "DCBA": true, "CDAB": true, "BADC": true}
	if !validOrders[cfg.Modbus.ByteOrder] {
		return nil, fmt.Errorf("TOML 配置错误: byte_order 必须为 ABCD/DCBA/CDAB/BADC 之一，当前值: %s", cfg.Modbus.ByteOrder)
	}

	log.Printf("[INFO] 成功加载设备配置: %s:%d, SlaveID=%d, ByteOrder=%s, Timeout=%ds, ScanInterval=%dms",
		cfg.Modbus.Address, cfg.Modbus.Port, cfg.Modbus.SlaveID, cfg.Modbus.ByteOrder,
		cfg.Modbus.TimeoutSec, cfg.Modbus.ScanIntervalMs)
	return &cfg, nil
}
