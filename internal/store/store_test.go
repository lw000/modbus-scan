package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"modbus-scan/internal/model"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func testDevice(name string) model.Device {
	return model.Device{Name: name, Address: "127.0.0.1", Port: 502, SlaveID: 1, ByteOrder: "ABCD", TimeoutSec: 5, ScanIntervalMs: 1000}
}

func TestOpenCreatesSchemaAndCanReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "test.db")
	for i := 0; i < 2; i++ {
		s, err := Open(context.Background(), path, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenMigratesPointDescription(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "version-1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at DATETIME NOT NULL);
CREATE TABLE devices (
 id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 0,
 address TEXT NOT NULL, port INTEGER NOT NULL, slave_id INTEGER NOT NULL, byte_order TEXT NOT NULL,
 timeout_sec INTEGER NOT NULL, scan_interval_ms INTEGER NOT NULL, config_version INTEGER NOT NULL DEFAULT 1,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
);
CREATE TABLE points (
 id INTEGER PRIMARY KEY AUTOINCREMENT, device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
 tag_name TEXT NOT NULL, reg_type TEXT NOT NULL, address INTEGER NOT NULL, data_type TEXT NOT NULL,
 bit_offset INTEGER NOT NULL, bit_len INTEGER NOT NULL, writeable INTEGER NOT NULL,
 created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL, UNIQUE(device_id, tag_name)
);
INSERT INTO schema_migrations(version, applied_at) VALUES(1, CURRENT_TIMESTAMP);
INSERT INTO devices(id, name, address, port, slave_id, byte_order, timeout_sec, scan_interval_ms, created_at, updated_at)
 VALUES(1, 'plc', '127.0.0.1', 502, 1, 'ABCD', 5, 1000, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
INSERT INTO points(device_id, tag_name, reg_type, address, data_type, bit_offset, bit_len, writeable, created_at, updated_at)
 VALUES(1, 'Speed', 'HoldingReg', 1, 'UInt16', 0, 16, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	point, err := s.GetPoint(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if point.Description != "" || point.Writeable != 1 {
		t.Fatalf("migrated point = %#v", point)
	}
}

func TestDeviceCRUDAndVersion(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	created, err := s.CreateDevice(ctx, testDevice("plc-1"))
	if err != nil {
		t.Fatal(err)
	}
	if created.ID == 0 || created.ConfigVersion != 1 {
		t.Fatalf("created = %#v", created)
	}
	created.Port = 1502
	updated, err := s.UpdateDevice(ctx, created)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Port != 1502 || updated.ConfigVersion != 2 {
		t.Fatalf("updated = %#v", updated)
	}
	enabled, err := s.SetDeviceEnabled(ctx, created.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Enabled || enabled.ConfigVersion != 2 {
		t.Fatalf("enabled = %#v", enabled)
	}
	list, err := s.ListDevices(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %#v, err = %v", list, err)
	}
}

func TestCreateDeviceRejectsDuplicateName(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	if _, err := s.CreateDevice(ctx, testDevice("same")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDevice(ctx, testDevice("same")); !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v", err)
	}
}

func TestPointMutationsIncrementVersionAndCascade(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	device, err := s.CreateDevice(ctx, testDevice("plc"))
	if err != nil {
		t.Fatal(err)
	}
	point, err := s.CreatePoint(ctx, model.Point{DeviceID: device.ID, TagName: "Speed", Description: "主轴速度", RegType: "HoldingReg", Address: 1, DataType: "UInt16", BitLen: 16, Writeable: 1})
	if err != nil {
		t.Fatal(err)
	}
	if point.ID == 0 || point.Description != "主轴速度" || point.Writeable != 1 {
		t.Fatalf("point = %#v", point)
	}
	device, err = s.GetDevice(ctx, device.ID)
	if err != nil || device.ConfigVersion != 2 {
		t.Fatalf("device = %#v, err = %v", device, err)
	}
	page, err := s.ListPoints(ctx, device.ID, PointFilter{Limit: 50})
	if err != nil || page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("page = %#v, err = %v", page, err)
	}
	if err := s.DeleteDevice(ctx, device.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPoint(ctx, device.ID, point.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v", err)
	}
}

func TestReplacePointsRollsBackOnConflict(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	device, err := s.CreateDevice(ctx, testDevice("plc"))
	if err != nil {
		t.Fatal(err)
	}
	original := model.Point{DeviceID: device.ID, TagName: "Original", RegType: "HoldingReg", DataType: "UInt16", BitLen: 16}
	if _, err := s.CreatePoint(ctx, original); err != nil {
		t.Fatal(err)
	}
	duplicate := []model.Point{
		{TagName: "Same", RegType: "HoldingReg", DataType: "UInt16", BitLen: 16},
		{TagName: "Same", RegType: "HoldingReg", Address: 1, DataType: "UInt16", BitLen: 16},
	}
	if err := s.ReplacePoints(ctx, device.ID, duplicate); !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v", err)
	}
	points, err := s.ListAllPoints(ctx, device.ID)
	if err != nil || len(points) != 1 || points[0].TagName != "Original" {
		t.Fatalf("points = %#v, err = %v", points, err)
	}
}

func TestDeletePointsIsAtomicAndBumpsVersionOnce(t *testing.T) {
	ctx := context.Background()
	s := openTestStore(t)
	first, _ := s.CreateDevice(ctx, testDevice("first"))
	second, _ := s.CreateDevice(ctx, testDevice("second"))
	a, _ := s.CreatePoint(ctx, model.Point{DeviceID: first.ID, TagName: "A", RegType: "HoldingReg", DataType: "UInt16", BitLen: 16})
	b, _ := s.CreatePoint(ctx, model.Point{DeviceID: first.ID, TagName: "B", RegType: "HoldingReg", DataType: "UInt16", BitLen: 16})
	foreign, _ := s.CreatePoint(ctx, model.Point{DeviceID: second.ID, TagName: "C", RegType: "HoldingReg", DataType: "UInt16", BitLen: 16})
	before, _ := s.GetDevice(ctx, first.ID)
	if _, err := s.DeletePoints(ctx, first.ID, []int64{a.ID, foreign.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-device delete error = %v", err)
	}
	if page, _ := s.ListPoints(ctx, first.ID, PointFilter{Limit: 50}); page.Total != 2 {
		t.Fatalf("atomic rollback total = %d", page.Total)
	}
	deleted, err := s.DeletePoints(ctx, first.ID, []int64{a.ID, b.ID})
	if err != nil || deleted != 2 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	after, _ := s.GetDevice(ctx, first.ID)
	if after.ConfigVersion != before.ConfigVersion+1 {
		t.Fatalf("versions %d -> %d", before.ConfigVersion, after.ConfigVersion)
	}
}
