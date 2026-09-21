// Package service implements transport-neutral application use cases.
package service

import (
	"context"
	"fmt"
	"strings"

	"modbus-scan/internal/model"
	devruntime "modbus-scan/internal/runtime"
	"modbus-scan/internal/store"
)

// ValidationError contains one or more invalid fields.
type ValidationError struct{ Fields []model.FieldError }

func (e *ValidationError) Error() string { return "validation failed" }

// DeviceRuntime controls and observes device runners.
type DeviceRuntime interface {
	Start(ctx context.Context, id int64) error
	Stop(ctx context.Context, id int64) error
	Restart(ctx context.Context, id int64) error
	Snapshot(ctx context.Context, id int64) (devruntime.Snapshot, error)
}

// DeviceStore is the persistence needed by DeviceService.
type DeviceStore interface {
	CreateDevice(ctx context.Context, d model.Device) (model.Device, error)
	GetDevice(ctx context.Context, id int64) (model.Device, error)
	ListDevices(ctx context.Context) ([]model.Device, error)
	UpdateDevice(ctx context.Context, d model.Device) (model.Device, error)
	DeleteDevice(ctx context.Context, id int64) error
	ListPoints(ctx context.Context, deviceID int64, filter store.PointFilter) (store.PointPage, error)
}

// DeviceView combines persisted configuration and transient runtime status.
type DeviceView struct {
	Device     model.Device        `json:"device"`
	PointCount int                 `json:"point_count"`
	Runtime    devruntime.Snapshot `json:"runtime"`
}

// DeviceService manages device use cases.
type DeviceService struct {
	store   DeviceStore
	runtime DeviceRuntime
}

// NewDeviceService creates a device service.
func NewDeviceService(store DeviceStore, runtime DeviceRuntime) *DeviceService {
	return &DeviceService{store: store, runtime: runtime}
}

// Create validates and persists a device without starting it automatically.
func (s *DeviceService) Create(ctx context.Context, input model.Device) (model.Device, error) {
	device, err := normalizeDevice(input)
	if err != nil {
		return model.Device{}, err
	}
	device.Enabled = false
	return s.store.CreateDevice(ctx, device)
}

// Update saves configuration without restarting the running device.
func (s *DeviceService) Update(ctx context.Context, id int64, input model.Device) (model.Device, error) {
	device, err := normalizeDevice(input)
	if err != nil {
		return model.Device{}, err
	}
	device.ID = id
	return s.store.UpdateDevice(ctx, device)
}

// Delete stops and removes a device.
func (s *DeviceService) Delete(ctx context.Context, id int64) error {
	if _, err := s.store.GetDevice(ctx, id); err != nil {
		return err
	}
	if err := s.runtime.Stop(ctx, id); err != nil {
		return fmt.Errorf("stop device before delete: %w", err)
	}
	return s.store.DeleteDevice(ctx, id)
}

// Get returns one configured device with runtime status.
func (s *DeviceService) Get(ctx context.Context, id int64) (DeviceView, error) {
	device, err := s.store.GetDevice(ctx, id)
	if err != nil {
		return DeviceView{}, err
	}
	page, err := s.store.ListPoints(ctx, id, store.PointFilter{Limit: 1})
	if err != nil {
		return DeviceView{}, err
	}
	snapshot, err := s.runtime.Snapshot(ctx, id)
	if err != nil {
		return DeviceView{}, err
	}
	return DeviceView{Device: device, PointCount: page.Total, Runtime: snapshot}, nil
}

// List returns all device views.
func (s *DeviceService) List(ctx context.Context) ([]DeviceView, error) {
	devices, err := s.store.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]DeviceView, 0, len(devices))
	for _, device := range devices {
		view, err := s.Get(ctx, device.ID)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *DeviceService) Start(ctx context.Context, id int64) error { return s.runtime.Start(ctx, id) }
func (s *DeviceService) Stop(ctx context.Context, id int64) error  { return s.runtime.Stop(ctx, id) }
func (s *DeviceService) Restart(ctx context.Context, id int64) error {
	return s.runtime.Restart(ctx, id)
}
func (s *DeviceService) Snapshot(ctx context.Context, id int64) (devruntime.Snapshot, error) {
	return s.runtime.Snapshot(ctx, id)
}

func normalizeDevice(device model.Device) (model.Device, error) {
	device.Name = strings.TrimSpace(device.Name)
	device.Address = strings.TrimSpace(device.Address)
	device.ByteOrder = strings.ToUpper(strings.TrimSpace(device.ByteOrder))
	if device.ByteOrder == "" {
		device.ByteOrder = "ABCD"
	}
	if device.TimeoutSec == 0 {
		device.TimeoutSec = 5
	}
	if device.ScanIntervalMs == 0 {
		device.ScanIntervalMs = 2000
	}
	fields := make([]model.FieldError, 0)
	if device.Name == "" || len(device.Name) > 128 {
		fields = append(fields, model.FieldError{Field: "name", Message: "must contain 1 to 128 characters"})
	}
	if device.Address == "" || len(device.Address) > 255 {
		fields = append(fields, model.FieldError{Field: "address", Message: "must contain 1 to 255 characters"})
	}
	if device.Port < 1 || device.Port > 65535 {
		fields = append(fields, model.FieldError{Field: "port", Message: "must be between 1 and 65535"})
	}
	if device.SlaveID < 1 || device.SlaveID > 247 {
		fields = append(fields, model.FieldError{Field: "slave_id", Message: "must be between 1 and 247"})
	}
	if device.ByteOrder != "ABCD" && device.ByteOrder != "DCBA" && device.ByteOrder != "CDAB" && device.ByteOrder != "BADC" {
		fields = append(fields, model.FieldError{Field: "byte_order", Message: "must be ABCD, DCBA, CDAB, or BADC"})
	}
	if device.TimeoutSec < 1 || device.TimeoutSec > 300 {
		fields = append(fields, model.FieldError{Field: "timeout_sec", Message: "must be between 1 and 300"})
	}
	if device.ScanIntervalMs < 100 || device.ScanIntervalMs > 3600000 {
		fields = append(fields, model.FieldError{Field: "scan_interval_ms", Message: "must be between 100 and 3600000"})
	}
	if len(fields) > 0 {
		return model.Device{}, &ValidationError{Fields: fields}
	}
	return device, nil
}
