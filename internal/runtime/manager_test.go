package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"modbus-scan/internal/model"
	"modbus-scan/internal/udm"
)

type fakeSource struct {
	mu      sync.Mutex
	devices map[int64]model.Device
	points  map[int64][]model.Point
}

func (f *fakeSource) GetDevice(_ context.Context, id int64) (model.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.devices[id]
	if !ok {
		return model.Device{}, errors.New("not found")
	}
	return d, nil
}
func (f *fakeSource) ListDevices(context.Context) ([]model.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]model.Device, 0, len(f.devices))
	for _, d := range f.devices {
		out = append(out, d)
	}
	return out, nil
}
func (f *fakeSource) ListAllPoints(_ context.Context, id int64) ([]model.Point, error) {
	return f.points[id], nil
}
func (f *fakeSource) SetDeviceEnabled(_ context.Context, id int64, enabled bool) (model.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.devices[id]
	d.Enabled = enabled
	f.devices[id] = d
	return d, nil
}

type fakeRunner struct {
	started chan struct{}
	stopped chan struct{}
	once    sync.Once
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{started: make(chan struct{}), stopped: make(chan struct{})}
}
func (r *fakeRunner) Run(ctx context.Context) {
	close(r.started)
	<-ctx.Done()
	r.once.Do(func() { close(r.stopped) })
}
func (r *fakeRunner) Close() error { r.once.Do(func() { close(r.stopped) }); return nil }
func (r *fakeRunner) Values() map[string]udm.Value {
	return map[string]udm.Value{"Speed": {Value: 1.0}}
}

type fakeFactory struct {
	mu    sync.Mutex
	count int
}

func (f *fakeFactory) New(_ model.Device, _ []model.Point, sink StatusSink) (Runner, error) {
	f.mu.Lock()
	f.count++
	f.mu.Unlock()
	sink(StatusEvent{State: StateOnline, At: time.Now()})
	return newFakeRunner(), nil
}

func newTestManager() (*Manager, *fakeSource, *fakeFactory) {
	source := &fakeSource{devices: map[int64]model.Device{1: {ID: 1, Name: "plc", ConfigVersion: 1}}, points: map[int64][]model.Point{}}
	factory := &fakeFactory{}
	return NewManager(source, factory), source, factory
}

func TestStartAndStopArePersistentAndIdempotent(t *testing.T) {
	m, source, factory := newTestManager()
	ctx := context.Background()
	if err := m.Start(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if factory.count != 1 {
		t.Fatalf("runner count = %d", factory.count)
	}
	if !source.devices[1].Enabled {
		t.Fatal("enabled state was not persisted")
	}
	if err := m.Stop(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if source.devices[1].Enabled {
		t.Fatal("stopped state was not persisted")
	}
}

func TestRestartLoadsNewConfigVersion(t *testing.T) {
	m, source, factory := newTestManager()
	ctx := context.Background()
	if err := m.Start(ctx, 1); err != nil {
		t.Fatal(err)
	}
	source.mu.Lock()
	d := source.devices[1]
	d.ConfigVersion = 2
	source.devices[1] = d
	source.mu.Unlock()
	snapshot, err := m.Snapshot(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.ConfigPending {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if err := m.Restart(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if factory.count != 2 {
		t.Fatalf("runner count = %d", factory.count)
	}
	snapshot, err = m.Snapshot(ctx, 1)
	if err != nil || snapshot.LoadedConfigVersion != 2 || snapshot.ConfigPending {
		t.Fatalf("snapshot = %#v, err = %v", snapshot, err)
	}
	_ = m.Stop(ctx, 1)
}

func TestStartEnabledAndStopAll(t *testing.T) {
	m, source, factory := newTestManager()
	d := source.devices[1]
	d.Enabled = true
	source.devices[1] = d
	if errs := m.StartEnabled(context.Background()); len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
	if factory.count != 1 {
		t.Fatalf("runner count = %d", factory.count)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := m.StopAll(ctx); err != nil {
		t.Fatal(err)
	}
}
