// Package runtime manages the lifecycle of device collectors.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"modbus-scan/internal/model"
	"modbus-scan/internal/udm"
)

const (
	StateStopped      = "stopped"
	StateStarting     = "starting"
	StateOnline       = "online"
	StateReconnecting = "reconnecting"
	StateOffline      = "offline"
	StateStopping     = "stopping"
	StateError        = "error"
)

// StatusEvent reports a collector state transition.
type StatusEvent struct {
	State           string
	LastError       string
	At              time.Time
	LastCollectedAt *time.Time
}

// StatusSink receives collector state transitions.
type StatusSink func(StatusEvent)

// Runner is one device collection instance.
type Runner interface {
	Run(ctx context.Context)
	Close() error
	Values() map[string]udm.Value
}

// Factory builds device runners from immutable configuration snapshots.
type Factory interface {
	New(device model.Device, points []model.Point, sink StatusSink) (Runner, error)
}

// ConfigSource supplies persisted device configuration.
type ConfigSource interface {
	GetDevice(ctx context.Context, id int64) (model.Device, error)
	ListDevices(ctx context.Context) ([]model.Device, error)
	ListAllPoints(ctx context.Context, deviceID int64) ([]model.Point, error)
	SetDeviceEnabled(ctx context.Context, id int64, enabled bool) (model.Device, error)
}

// Snapshot is the read-only runtime state exposed to services.
type Snapshot struct {
	DeviceID            int64                `json:"device_id"`
	State               string               `json:"state"`
	LastError           string               `json:"last_error,omitempty"`
	LastCollectedAt     *time.Time           `json:"last_collected_at,omitempty"`
	LoadedConfigVersion int64                `json:"loaded_config_version"`
	ConfigPending       bool                 `json:"config_pending"`
	Values              map[string]udm.Value `json:"values,omitempty"`
}

type deviceRuntime struct {
	opMu sync.Mutex
	mu   sync.RWMutex

	cancel              context.CancelFunc
	runner              Runner
	done                chan struct{}
	state               string
	lastError           string
	lastCollectedAt     *time.Time
	loadedConfigVersion int64
	lastValues          map[string]udm.Value
}

// Manager owns all device runtime instances.
type Manager struct {
	source  ConfigSource
	factory Factory
	mu      sync.Mutex
	devices map[int64]*deviceRuntime
}

// NewManager creates an empty runtime registry.
func NewManager(source ConfigSource, factory Factory) *Manager {
	return &Manager{source: source, factory: factory, devices: make(map[int64]*deviceRuntime)}
}

func (m *Manager) entry(id int64) *deviceRuntime {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry := m.devices[id]
	if entry == nil {
		entry = &deviceRuntime{state: StateStopped, lastValues: make(map[string]udm.Value)}
		m.devices[id] = entry
	}
	return entry
}

// Start persists enabled state and starts a device if it is not already running.
func (m *Manager) Start(ctx context.Context, id int64) error {
	entry := m.entry(id)
	entry.opMu.Lock()
	defer entry.opMu.Unlock()
	device, err := m.source.SetDeviceEnabled(ctx, id, true)
	if err != nil {
		return fmt.Errorf("enable device: %w", err)
	}
	return m.startLocked(ctx, entry, device)
}

func (m *Manager) startLocked(ctx context.Context, entry *deviceRuntime, device model.Device) error {
	entry.mu.RLock()
	running := entry.runner != nil
	entry.mu.RUnlock()
	if running {
		return nil
	}
	points, err := m.source.ListAllPoints(ctx, device.ID)
	if err != nil {
		entry.setError(err)
		return fmt.Errorf("load device points: %w", err)
	}
	sink := func(event StatusEvent) {
		entry.mu.Lock()
		defer entry.mu.Unlock()
		if event.State != "" {
			entry.state = event.State
		}
		entry.lastError = event.LastError
		if event.LastCollectedAt != nil {
			at := *event.LastCollectedAt
			entry.lastCollectedAt = &at
		}
	}
	runner, err := m.factory.New(device, points, sink)
	if err != nil {
		entry.setError(err)
		return fmt.Errorf("create device runner: %w", err)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	entry.mu.Lock()
	entry.cancel = cancel
	entry.runner = runner
	entry.done = done
	entry.state = StateStarting
	entry.lastError = ""
	entry.loadedConfigVersion = device.ConfigVersion
	entry.mu.Unlock()
	go func() {
		defer close(done)
		runner.Run(runCtx)
		entry.mu.Lock()
		if entry.runner == runner {
			entry.lastValues = runner.Values()
			entry.runner = nil
			entry.cancel = nil
			entry.state = StateStopped
		}
		entry.mu.Unlock()
	}()
	return nil
}

func (entry *deviceRuntime) setError(err error) {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	entry.state = StateError
	entry.lastError = err.Error()
}

// Stop persists disabled state and stops the active runner.
func (m *Manager) Stop(ctx context.Context, id int64) error {
	entry := m.entry(id)
	entry.opMu.Lock()
	defer entry.opMu.Unlock()
	if _, err := m.source.SetDeviceEnabled(ctx, id, false); err != nil {
		return fmt.Errorf("disable device: %w", err)
	}
	return stopLocked(ctx, entry)
}

func stopLocked(ctx context.Context, entry *deviceRuntime) error {
	entry.mu.Lock()
	runner, cancel, done := entry.runner, entry.cancel, entry.done
	if runner == nil {
		entry.state = StateStopped
		entry.mu.Unlock()
		return nil
	}
	entry.state = StateStopping
	entry.lastValues = runner.Values()
	entry.mu.Unlock()
	cancel()
	closeErr := runner.Close()
	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("stop device: %w", ctx.Err())
	}
	if closeErr != nil {
		return fmt.Errorf("close device runner: %w", closeErr)
	}
	return nil
}

// Restart reloads and starts the latest enabled configuration.
func (m *Manager) Restart(ctx context.Context, id int64) error {
	entry := m.entry(id)
	entry.opMu.Lock()
	defer entry.opMu.Unlock()
	device, err := m.source.GetDevice(ctx, id)
	if err != nil {
		return fmt.Errorf("get device: %w", err)
	}
	if !device.Enabled {
		return errors.New("device is disabled")
	}
	if err := stopLocked(ctx, entry); err != nil {
		return err
	}
	return m.startLocked(ctx, entry, device)
}

// StartEnabled starts all persistently enabled devices and isolates failures.
func (m *Manager) StartEnabled(ctx context.Context) []error {
	devices, err := m.source.ListDevices(ctx)
	if err != nil {
		return []error{fmt.Errorf("list enabled devices: %w", err)}
	}
	errs := make([]error, 0)
	for _, device := range devices {
		if !device.Enabled {
			continue
		}
		entry := m.entry(device.ID)
		entry.opMu.Lock()
		err := m.startLocked(ctx, entry, device)
		entry.opMu.Unlock()
		if err != nil {
			errs = append(errs, fmt.Errorf("start device %d: %w", device.ID, err))
		}
	}
	return errs
}

// StopAll stops every active device without changing persistent enable state.
func (m *Manager) StopAll(ctx context.Context) error {
	m.mu.Lock()
	entries := make([]*deviceRuntime, 0, len(m.devices))
	for _, entry := range m.devices {
		entries = append(entries, entry)
	}
	m.mu.Unlock()
	var errs []error
	for _, entry := range entries {
		entry.opMu.Lock()
		err := stopLocked(ctx, entry)
		entry.opMu.Unlock()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Snapshot returns current runtime state and configuration staleness.
func (m *Manager) Snapshot(ctx context.Context, id int64) (Snapshot, error) {
	device, err := m.source.GetDevice(ctx, id)
	if err != nil {
		return Snapshot{}, fmt.Errorf("get device snapshot: %w", err)
	}
	entry := m.entry(id)
	entry.mu.RLock()
	runner := entry.runner
	snapshot := Snapshot{
		DeviceID: id, State: entry.state, LastError: entry.lastError,
		LastCollectedAt: entry.lastCollectedAt, LoadedConfigVersion: entry.loadedConfigVersion,
		ConfigPending: entry.loadedConfigVersion != 0 && device.ConfigVersion != entry.loadedConfigVersion,
		Values:        copyValues(entry.lastValues),
	}
	entry.mu.RUnlock()
	if runner != nil {
		snapshot.Values = copyValues(runner.Values())
	}
	return snapshot, nil
}

func copyValues(values map[string]udm.Value) map[string]udm.Value {
	result := make(map[string]udm.Value, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
