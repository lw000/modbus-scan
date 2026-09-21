package config

import (
	"fmt"
	"log"
	"strings"

	"github.com/BurntSushi/toml"
)

// DeviceConfig Modbus 设备连接配置
type DeviceConfig struct {
	Name    string
	Enabled bool
	CSV     string
	Modbus  ModbusConfig
}

// ModbusConfig Modbus TCP 连接参数。
type ModbusConfig struct {
	Address        string `toml:"address"`
	Port           int    `toml:"port"`
	SlaveID        byte   `toml:"slave_id"`
	ByteOrder      string `toml:"byte_order"`
	TimeoutSec     int    `toml:"timeout_sec"`
	ScanIntervalMs int    `toml:"scan_interval_ms"`
}

type configFile struct {
	Modbus  ModbusConfig      `toml:"modbus"`
	Devices []rawDeviceConfig `toml:"devices"`
}

type rawDeviceConfig struct {
	Name           string `toml:"name"`
	Enabled        *bool  `toml:"enabled"`
	CSV            string `toml:"csv"`
	Address        string `toml:"address"`
	Port           int    `toml:"port"`
	SlaveID        byte   `toml:"slave_id"`
	ByteOrder      string `toml:"byte_order"`
	TimeoutSec     int    `toml:"timeout_sec"`
	ScanIntervalMs int    `toml:"scan_interval_ms"`
}

// LoadDeviceConfig 从 TOML 文件加载设备连接配置并进行校验
func LoadDeviceConfig(filePath string) (*DeviceConfig, error) {
	devices, err := LoadDeviceConfigs(filePath, "")
	if err != nil {
		return nil, err
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("TOML 配置错误: 没有有效的设备配置")
	}
	return devices[0], nil
}

// LoadDeviceConfigs 从 TOML 文件加载设备列表，并兼容旧版 [modbus] 单设备配置。
func LoadDeviceConfigs(filePath string, defaultCSV string) ([]*DeviceConfig, error) {
	var fileCfg configFile
	if _, err := toml.DecodeFile(filePath, &fileCfg); err != nil {
		return nil, fmt.Errorf("解析 TOML 配置文件失败: %w", err)
	}

	if len(fileCfg.Devices) > 0 {
		devices := make([]*DeviceConfig, 0, len(fileCfg.Devices))
		for i, raw := range fileCfg.Devices {
			device := &DeviceConfig{
				Name:    strings.TrimSpace(raw.Name),
				Enabled: true,
				CSV:     strings.TrimSpace(raw.CSV),
				Modbus: ModbusConfig{
					Address:        raw.Address,
					Port:           raw.Port,
					SlaveID:        raw.SlaveID,
					ByteOrder:      raw.ByteOrder,
					TimeoutSec:     raw.TimeoutSec,
					ScanIntervalMs: raw.ScanIntervalMs,
				},
			}
			if raw.Enabled != nil {
				device.Enabled = *raw.Enabled
			}
			if device.Name == "" {
				device.Name = fmt.Sprintf("device-%d", i+1)
			}
			if device.CSV == "" {
				device.CSV = defaultCSV
			}
			if err := normalizeDeviceConfig(device); err != nil {
				return nil, fmt.Errorf("TOML 配置错误: devices[%d] (%s): %w", i, device.Name, err)
			}
			devices = append(devices, device)
		}
		log.Printf("[INFO] 成功加载 %d 个设备配置", len(devices))
		return devices, nil
	}

	if fileCfg.Modbus.Address == "" && fileCfg.Modbus.Port == 0 && fileCfg.Modbus.SlaveID == 0 {
		return nil, fmt.Errorf("TOML 配置错误: 未找到 [[devices]] 或 [modbus] 配置")
	}

	device := &DeviceConfig{
		Name:    "default",
		Enabled: true,
		CSV:     defaultCSV,
		Modbus:  fileCfg.Modbus,
	}
	if err := normalizeDeviceConfig(device); err != nil {
		return nil, err
	}
	log.Printf("[INFO] 成功加载设备配置: %s:%d, SlaveID=%d, ByteOrder=%s, Timeout=%ds, ScanInterval=%dms",
		device.Modbus.Address, device.Modbus.Port, device.Modbus.SlaveID, device.Modbus.ByteOrder,
		device.Modbus.TimeoutSec, device.Modbus.ScanIntervalMs)
	return []*DeviceConfig{device}, nil
}

func normalizeDeviceConfig(cfg *DeviceConfig) error {
	if cfg.Modbus.Address == "" || cfg.Modbus.Port <= 0 {
		return fmt.Errorf("address 和 port 必须有效")
	}
	if cfg.Modbus.SlaveID == 0 {
		return fmt.Errorf("slave_id 必须为 1-247 之间的有效值")
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
		return fmt.Errorf("byte_order 必须为 ABCD/DCBA/CDAB/BADC 之一，当前值: %s", cfg.Modbus.ByteOrder)
	}

	return nil
}
