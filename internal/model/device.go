// Package model contains transport- and storage-neutral domain data.
package model

import "time"

// Device is a persisted Modbus device configuration.
type Device struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"`
	Enabled        bool      `json:"enabled"`
	Address        string    `json:"address"`
	Port           int       `json:"port"`
	SlaveID        int       `json:"slave_id"`
	ByteOrder      string    `json:"byte_order"`
	TimeoutSec     int       `json:"timeout_sec"`
	ScanIntervalMs int       `json:"scan_interval_ms"`
	ConfigVersion  int64     `json:"config_version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}
