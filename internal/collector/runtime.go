package collector

import (
	"context"
	"fmt"
	"sync"
	"time"

	"modbus-scan/internal/config"
	"modbus-scan/internal/model"
	"modbus-scan/internal/realtime"
	devruntime "modbus-scan/internal/runtime"
	"modbus-scan/internal/udm"
)

// RuntimeFactory adapts the collector to the runtime manager boundary.
type RuntimeFactory struct{ publish func(realtime.ValueEvent) }

// NewRuntimeFactory creates a collector runtime factory.
func NewRuntimeFactory(publishers ...func(realtime.ValueEvent)) *RuntimeFactory {
	factory := &RuntimeFactory{}
	if len(publishers) > 0 {
		factory.publish = publishers[0]
	}
	return factory
}

// New creates an immutable device runner.
func (f *RuntimeFactory) New(device model.Device, points []model.Point, sink devruntime.StatusSink) (devruntime.Runner, error) {
	if len(points) == 0 {
		return nil, fmt.Errorf("device has no configured points")
	}
	return &runtimeRunner{device: device, points: append([]model.Point(nil), points...), sink: sink, values: udm.New(), publish: f.publish}, nil
}

type runtimeRunner struct {
	device  model.Device
	points  []model.Point
	sink    devruntime.StatusSink
	values  *udm.UniversalDataModel
	mu      sync.Mutex
	conn    *ConnManager
	publish func(realtime.ValueEvent)
}

func (r *runtimeRunner) Run(ctx context.Context) {
	cfg := &config.DeviceConfig{Name: r.device.Name, Enabled: true, Modbus: config.ModbusConfig{Address: r.device.Address, Port: r.device.Port, SlaveID: byte(r.device.SlaveID), ByteOrder: r.device.ByteOrder, TimeoutSec: r.device.TimeoutSec, ScanIntervalMs: r.device.ScanIntervalMs}}
	configured := make([]config.PointConfig, 0, len(r.points))
	for _, point := range r.points {
		configured = append(configured, config.PointConfig{TagName: point.TagName, RegType: point.RegType, Address: point.Address, DataType: point.DataType, BitOffset: point.BitOffset, BitLen: point.BitLen, Writeable: point.Writeable == 1})
	}
	manager := NewConnManager(cfg, ctx)
	r.mu.Lock()
	r.conn = manager
	r.mu.Unlock()
	r.sink(devruntime.StatusEvent{State: devruntime.StateStarting, At: time.Now().UTC()})
	if err := manager.Connect(); err != nil {
		r.sink(devruntime.StatusEvent{State: devruntime.StateReconnecting, LastError: err.Error(), At: time.Now().UTC()})
		manager.ReconnectWithBackoff()
		if ctx.Err() != nil {
			return
		}
	}
	if manager.State() == StateOffline {
		r.sink(devruntime.StatusEvent{State: devruntime.StateOffline, LastError: "connection attempts exhausted", At: time.Now().UTC()})
	} else {
		r.sink(devruntime.StatusEvent{State: devruntime.StateOnline, At: time.Now().UTC()})
	}
	go r.monitorConnectionState(ctx, manager)
	publish := func(tag string, value udm.Value) {
		if r.publish != nil {
			r.publish(realtime.ValueEvent{DeviceID: r.device.ID, TagName: tag, Value: value.Value, UpdatedAt: value.UpdatedAt})
		}
	}
	collector := NewCollector(manager, r.values, config.OptimizeChunks(configured), cfg, publish)
	collector.ScanLoop(ctx, time.Duration(r.device.ScanIntervalMs)*time.Millisecond)
}

func (r *runtimeRunner) monitorConnectionState(ctx context.Context, manager *ConnManager) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	last := manager.State()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := manager.State()
			if current == last {
				continue
			}
			last = current
			r.sink(devruntime.StatusEvent{State: runtimeState(current), At: time.Now().UTC()})
		}
	}
}

func runtimeState(state int32) string {
	switch state {
	case StateReconnecting:
		return devruntime.StateReconnecting
	case StateOffline:
		return devruntime.StateOffline
	default:
		return devruntime.StateOnline
	}
}

func (r *runtimeRunner) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn == nil {
		return nil
	}
	return r.conn.Close()
}

func (r *runtimeRunner) Values() map[string]udm.Value { return r.values.Snapshot() }
