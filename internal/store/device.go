package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"modbus-scan/internal/model"
)

const deviceColumns = `id, name, enabled, address, port, slave_id, byte_order, timeout_sec, scan_interval_ms, config_version, created_at, updated_at`

type scanner interface{ Scan(dest ...any) error }

func scanDevice(row scanner) (model.Device, error) {
	var d model.Device
	var enabled int
	err := row.Scan(&d.ID, &d.Name, &enabled, &d.Address, &d.Port, &d.SlaveID, &d.ByteOrder, &d.TimeoutSec, &d.ScanIntervalMs, &d.ConfigVersion, &d.CreatedAt, &d.UpdatedAt)
	d.Enabled = enabled != 0
	return d, err
}

// CreateDevice inserts a device.
func (s *Store) CreateDevice(ctx context.Context, d model.Device) (model.Device, error) {
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Device{}, fmt.Errorf("begin create device: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT INTO devices(name, enabled, address, port, slave_id, byte_order, timeout_sec, scan_interval_ms, config_version, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)`, d.Name, d.Enabled, d.Address, d.Port, d.SlaveID, d.ByteOrder, d.TimeoutSec, d.ScanIntervalMs, now, now)
	if err != nil {
		return model.Device{}, mapError("create device", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.Device{}, fmt.Errorf("create device ID: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO device_kafka_configs(device_id, created_at, updated_at) VALUES(?, ?, ?)`, id, now, now); err != nil {
		return model.Device{}, mapError("create device Kafka config", err)
	}
	if err := tx.Commit(); err != nil {
		return model.Device{}, fmt.Errorf("commit create device: %w", err)
	}
	return s.GetDevice(ctx, id)
}

// GetDevice returns one device.
func (s *Store) GetDevice(ctx context.Context, id int64) (model.Device, error) {
	d, err := scanDevice(s.db.QueryRowContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE id = ?`, id))
	return d, mapError("get device", err)
}

// ListDevices returns all devices by ID.
func (s *Store) ListDevices(ctx context.Context) ([]model.Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+deviceColumns+` FROM devices ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()
	devices := make([]model.Device, 0)
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, fmt.Errorf("scan device: %w", err)
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	return devices, nil
}

// UpdateDevice replaces editable fields and increments the configuration version.
func (s *Store) UpdateDevice(ctx context.Context, d model.Device) (model.Device, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE devices SET name=?, address=?, port=?, slave_id=?, byte_order=?, timeout_sec=?, scan_interval_ms=?, config_version=config_version+1, updated_at=? WHERE id=?`, d.Name, d.Address, d.Port, d.SlaveID, d.ByteOrder, d.TimeoutSec, d.ScanIntervalMs, time.Now().UTC(), d.ID)
	if err != nil {
		return model.Device{}, mapError("update device", err)
	}
	if err := requireAffected("update device", result); err != nil {
		return model.Device{}, err
	}
	return s.GetDevice(ctx, d.ID)
}

// SetDeviceEnabled persists the desired runtime state without changing config version.
func (s *Store) SetDeviceEnabled(ctx context.Context, id int64, enabled bool) (model.Device, error) {
	result, err := s.db.ExecContext(ctx, `UPDATE devices SET enabled=?, updated_at=? WHERE id=?`, enabled, time.Now().UTC(), id)
	if err != nil {
		return model.Device{}, mapError("set device enabled", err)
	}
	if err := requireAffected("set device enabled", result); err != nil {
		return model.Device{}, err
	}
	return s.GetDevice(ctx, id)
}

// DeleteDevice removes a device and its points.
func (s *Store) DeleteDevice(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM devices WHERE id=?`, id)
	if err != nil {
		return mapError("delete device", err)
	}
	return requireAffected("delete device", result)
}

func requireAffected(operation string, result sql.Result) error {
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows affected: %w", operation, err)
	}
	if n == 0 {
		return fmt.Errorf("%s: %w", operation, ErrNotFound)
	}
	return nil
}
