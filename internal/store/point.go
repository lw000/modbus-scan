package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"modbus-scan/internal/model"
)

const pointColumns = `id, device_id, tag_name, description, reg_type, address, data_type, bit_offset, bit_len, writeable, created_at, updated_at`

// PointFilter controls point pagination and filtering.
type PointFilter struct {
	Search, RegType string
	Limit, Offset   int
}

// PointPage is one page of points and its total result count.
type PointPage struct {
	Items []model.Point `json:"items"`
	Total int           `json:"total"`
}

func scanPoint(row scanner) (model.Point, error) {
	var p model.Point
	var address int
	err := row.Scan(&p.ID, &p.DeviceID, &p.TagName, &p.Description, &p.RegType, &address, &p.DataType, &p.BitOffset, &p.BitLen, &p.Writeable, &p.CreatedAt, &p.UpdatedAt)
	p.Address = uint16(address)
	return p, err
}

// CreatePoint inserts a point and increments its device version atomically.
func (s *Store) CreatePoint(ctx context.Context, p model.Point) (model.Point, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Point{}, fmt.Errorf("begin create point: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `INSERT INTO points(device_id, tag_name, description, reg_type, address, data_type, bit_offset, bit_len, writeable, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, p.DeviceID, p.TagName, p.Description, p.RegType, p.Address, p.DataType, p.BitOffset, p.BitLen, p.Writeable, now, now)
	if err != nil {
		return model.Point{}, mapError("create point", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return model.Point{}, fmt.Errorf("create point ID: %w", err)
	}
	if err := bumpVersion(ctx, tx, p.DeviceID, now); err != nil {
		return model.Point{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Point{}, fmt.Errorf("commit create point: %w", err)
	}
	return s.GetPoint(ctx, p.DeviceID, id)
}

// GetPoint returns one device-owned point.
func (s *Store) GetPoint(ctx context.Context, deviceID, pointID int64) (model.Point, error) {
	p, err := scanPoint(s.db.QueryRowContext(ctx, `SELECT `+pointColumns+` FROM points WHERE device_id=? AND id=?`, deviceID, pointID))
	return p, mapError("get point", err)
}

// ListPoints returns a filtered page.
func (s *Store) ListPoints(ctx context.Context, deviceID int64, f PointFilter) (PointPage, error) {
	where, args := pointWhere(deviceID, f)
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM points `+where, args...).Scan(&total); err != nil {
		return PointPage{}, fmt.Errorf("count points: %w", err)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit, f.Offset)
	rows, err := s.db.QueryContext(ctx, `SELECT `+pointColumns+` FROM points `+where+` ORDER BY reg_type, address, tag_name LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return PointPage{}, fmt.Errorf("list points: %w", err)
	}
	defer rows.Close()
	items := make([]model.Point, 0)
	for rows.Next() {
		p, err := scanPoint(rows)
		if err != nil {
			return PointPage{}, fmt.Errorf("scan point: %w", err)
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		return PointPage{}, fmt.Errorf("list points: %w", err)
	}
	return PointPage{Items: items, Total: total}, nil
}

func pointWhere(deviceID int64, f PointFilter) (string, []any) {
	parts := []string{"WHERE device_id=?"}
	args := []any{deviceID}
	if f.Search != "" {
		parts = append(parts, "tag_name LIKE ?")
		args = append(args, "%"+f.Search+"%")
	}
	if f.RegType != "" {
		parts = append(parts, "reg_type=?")
		args = append(args, f.RegType)
	}
	return strings.Join(parts, " AND "), args
}

// ListAllPoints returns all device points in stable order.
func (s *Store) ListAllPoints(ctx context.Context, deviceID int64) ([]model.Point, error) {
	page, err := s.ListPoints(ctx, deviceID, PointFilter{Limit: int(^uint(0) >> 1)})
	return page.Items, err
}

// UpdatePoint updates a point and its device version.
func (s *Store) UpdatePoint(ctx context.Context, p model.Point) (model.Point, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return model.Point{}, fmt.Errorf("begin update point: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	result, err := tx.ExecContext(ctx, `UPDATE points SET tag_name=?, description=?, reg_type=?, address=?, data_type=?, bit_offset=?, bit_len=?, writeable=?, updated_at=? WHERE id=? AND device_id=?`, p.TagName, p.Description, p.RegType, p.Address, p.DataType, p.BitOffset, p.BitLen, p.Writeable, now, p.ID, p.DeviceID)
	if err != nil {
		return model.Point{}, mapError("update point", err)
	}
	if err := requireAffected("update point", result); err != nil {
		return model.Point{}, err
	}
	if err := bumpVersion(ctx, tx, p.DeviceID, now); err != nil {
		return model.Point{}, err
	}
	if err := tx.Commit(); err != nil {
		return model.Point{}, fmt.Errorf("commit update point: %w", err)
	}
	return s.GetPoint(ctx, p.DeviceID, p.ID)
}

// DeletePoint removes a point and increments its device version.
func (s *Store) DeletePoint(ctx context.Context, deviceID, pointID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin delete point: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `DELETE FROM points WHERE device_id=? AND id=?`, deviceID, pointID)
	if err != nil {
		return mapError("delete point", err)
	}
	if err := requireAffected("delete point", result); err != nil {
		return err
	}
	if err := bumpVersion(ctx, tx, deviceID, time.Now().UTC()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete point: %w", err)
	}
	return nil
}

// DeletePoints atomically removes device-owned points and bumps the version once.
func (s *Store) DeletePoints(ctx context.Context, deviceID int64, pointIDs []int64) (int, error) {
	if len(pointIDs) == 0 {
		return 0, fmt.Errorf("delete points: %w", ErrNotFound)
	}
	marks := strings.TrimRight(strings.Repeat("?,", len(pointIDs)), ",")
	args := make([]any, 0, len(pointIDs)+1)
	args = append(args, deviceID)
	for _, id := range pointIDs {
		args = append(args, id)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin delete points: %w", err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM points WHERE device_id=? AND id IN (`+marks+`)`, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count delete points: %w", err)
	}
	if count != len(pointIDs) {
		return 0, fmt.Errorf("delete points: %w", ErrNotFound)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM points WHERE device_id=? AND id IN (`+marks+`)`, args...); err != nil {
		return 0, mapError("delete points", err)
	}
	if err := bumpVersion(ctx, tx, deviceID, time.Now().UTC()); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit delete points: %w", err)
	}
	return count, nil
}

// ReplacePoints atomically replaces all points for a device.
func (s *Store) ReplacePoints(ctx context.Context, deviceID int64, points []model.Point) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace points: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM points WHERE device_id=?`, deviceID); err != nil {
		return fmt.Errorf("delete old points: %w", err)
	}
	now := time.Now().UTC()
	for _, p := range points {
		_, err := tx.ExecContext(ctx, `INSERT INTO points(device_id, tag_name, description, reg_type, address, data_type, bit_offset, bit_len, writeable, created_at, updated_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, deviceID, p.TagName, p.Description, p.RegType, p.Address, p.DataType, p.BitOffset, p.BitLen, p.Writeable, now, now)
		if err != nil {
			return mapError("replace points", err)
		}
	}
	if err := bumpVersion(ctx, tx, deviceID, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace points: %w", err)
	}
	return nil
}

func bumpVersion(ctx context.Context, tx *sql.Tx, deviceID int64, now time.Time) error {
	result, err := tx.ExecContext(ctx, `UPDATE devices SET config_version=config_version+1, updated_at=? WHERE id=?`, now, deviceID)
	if err != nil {
		return fmt.Errorf("increment device version: %w", err)
	}
	return requireAffected("increment device version", result)
}
