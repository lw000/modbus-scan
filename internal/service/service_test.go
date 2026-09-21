package service

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"modbus-scan/internal/model"
	devruntime "modbus-scan/internal/runtime"
	"modbus-scan/internal/store"
)

type fakeRuntime struct{ starts, stops, restarts int }

func (f *fakeRuntime) Start(context.Context, int64) error   { f.starts++; return nil }
func (f *fakeRuntime) Stop(context.Context, int64) error    { f.stops++; return nil }
func (f *fakeRuntime) Restart(context.Context, int64) error { f.restarts++; return nil }
func (f *fakeRuntime) Snapshot(_ context.Context, id int64) (devruntime.Snapshot, error) {
	return devruntime.Snapshot{DeviceID: id, State: devruntime.StateStopped}, nil
}

func openServices(t *testing.T) (*DeviceService, *PointService, *store.Store, *fakeRuntime) {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "service.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	runtime := &fakeRuntime{}
	return NewDeviceService(s, runtime), NewPointService(s), s, runtime
}

func TestDeviceServiceValidatesAndDoesNotRestartOnUpdate(t *testing.T) {
	devices, _, _, runtime := openServices(t)
	ctx := context.Background()
	if _, err := devices.Create(ctx, model.Device{Name: "bad", Address: "127.0.0.1", Port: 70000, SlaveID: 1}); err == nil {
		t.Fatal("expected invalid port error")
	}
	created, err := devices.Create(ctx, model.Device{Name: " plc ", Address: "127.0.0.1", Port: 502, SlaveID: 1, ByteOrder: "abcd", TimeoutSec: 5, ScanIntervalMs: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if created.Name != "plc" || created.ByteOrder != "ABCD" {
		t.Fatalf("device = %#v", created)
	}
	created.Port = 1502
	if _, err := devices.Update(ctx, created.ID, created); err != nil {
		t.Fatal(err)
	}
	if runtime.restarts != 0 {
		t.Fatal("update restarted the device")
	}
}

func TestDeleteStopsDeviceFirst(t *testing.T) {
	devices, _, _, runtime := openServices(t)
	created, err := devices.Create(context.Background(), model.Device{Name: "plc", Address: "127.0.0.1", Port: 502, SlaveID: 1, ByteOrder: "ABCD", TimeoutSec: 5, ScanIntervalMs: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err := devices.Delete(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if runtime.stops != 1 {
		t.Fatalf("stop calls = %d", runtime.stops)
	}
}

func TestPointImportIsStrictAndAtomic(t *testing.T) {
	devices, points, _, _ := openServices(t)
	device, err := devices.Create(context.Background(), model.Device{Name: "plc", Address: "127.0.0.1", Port: 502, SlaveID: 1, ByteOrder: "ABCD", TimeoutSec: 5, ScanIntervalMs: 1000})
	if err != nil {
		t.Fatal(err)
	}
	valid := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\nA,HoldingReg,0,UInt16,0,16,0,温度\n"
	count, rowErrors, err := points.Import(context.Background(), device.ID, strings.NewReader(valid))
	if err != nil || len(rowErrors) != 0 || count != 1 {
		t.Fatalf("count=%d rows=%#v err=%v", count, rowErrors, err)
	}
	invalid := "TagName,RegType,Address,DataType,BitOffset,BitLen,Writeable,Description\nB,HoldingReg,1,UInt16,0,0,0,\n"
	if _, rowErrors, err = points.Import(context.Background(), device.ID, strings.NewReader(invalid)); err != nil || len(rowErrors) == 0 {
		t.Fatalf("rows=%#v err=%v", rowErrors, err)
	}
	page, err := points.List(context.Background(), device.ID, store.PointFilter{Limit: 50})
	if err != nil || page.Total != 1 || page.Items[0].TagName != "A" {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	var output bytes.Buffer
	if err := points.Export(context.Background(), device.ID, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "A,HoldingReg") {
		t.Fatalf("output=%q", output.String())
	}
}

func TestPointServiceTrimsDescription(t *testing.T) {
	devices, points, _, _ := openServices(t)
	device, err := devices.Create(context.Background(), model.Device{Name: "plc", Address: "127.0.0.1", Port: 502, SlaveID: 1, ByteOrder: "ABCD", TimeoutSec: 5, ScanIntervalMs: 1000})
	if err != nil {
		t.Fatal(err)
	}
	created, err := points.Create(context.Background(), device.ID, model.Point{TagName: "A", Description: "  温度  ", RegType: "HoldingReg", DataType: "UInt16", BitLen: 16})
	if err != nil {
		t.Fatal(err)
	}
	if created.Description != "温度" {
		t.Fatalf("description = %q", created.Description)
	}
}
