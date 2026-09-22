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
	mu              sync.Mutex
	devices         map[int64]model.Device
	points          map[int64][]model.Point
	lifecycleBlocks map[int64]*lifecycleBlock
}

type lifecycleBlock struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
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
	block := f.lifecycleBlocks[id]
	f.mu.Unlock()
	if block != nil {
		block.once.Do(func() { close(block.entered) })
		<-block.release
	}
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
	err   error
}

func (f *fakeFactory) New(_ model.Device, _ []model.Point, sink StatusSink) (Runner, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	f.count++
	sink(StatusEvent{State: StateOnline, At: time.Now()})
	return newFakeRunner(), nil
}

func newTestManager() (*Manager, *fakeSource, *fakeFactory) {
	source := &fakeSource{
		devices:         map[int64]model.Device{1: {ID: 1, Name: "plc", ConfigVersion: 1}},
		points:          map[int64][]model.Point{},
		lifecycleBlocks: make(map[int64]*lifecycleBlock),
	}
	factory := &fakeFactory{}
	return NewManager(source, factory), source, factory
}

func TestInteractiveLifecycleRejectsBusyDevice(t *testing.T) {
	tests := []struct {
		name string
		call func(*Manager) error
	}{
		{name: "start", call: func(m *Manager) error { return m.Start(context.Background(), 1) }},
		{name: "stop", call: func(m *Manager) error { return m.Stop(context.Background(), 1) }},
		{name: "restart", call: func(m *Manager) error { return m.Restart(context.Background(), 1) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, source, factory := newTestManager()
			block := &lifecycleBlock{entered: make(chan struct{}), release: make(chan struct{})}
			source.lifecycleBlocks[1] = block
			firstDone := make(chan error, 1)
			go func() { firstDone <- m.Start(context.Background(), 1) }()
			<-block.entered

			snapshot, err := m.Snapshot(context.Background(), 1)
			if err != nil || snapshot.Operation != "start" {
				t.Fatalf("snapshot=%#v err=%v", snapshot, err)
			}
			if err := tt.call(m); !errors.Is(err, ErrDeviceBusy) {
				t.Fatalf("error=%v, want ErrDeviceBusy", err)
			}
			if factory.count != 0 {
				t.Fatalf("runner count=%d before release", factory.count)
			}

			close(block.release)
			if err := <-firstDone; err != nil {
				t.Fatal(err)
			}
			snapshot, err = m.Snapshot(context.Background(), 1)
			if err != nil || snapshot.Operation != "" {
				t.Fatalf("snapshot=%#v err=%v", snapshot, err)
			}
			delete(source.lifecycleBlocks, 1)
			_ = m.Stop(context.Background(), 1)
		})
	}
}

func TestInteractiveLifecycleAllowsDifferentDevices(t *testing.T) {
	m, source, factory := newTestManager()
	source.devices[2] = model.Device{ID: 2, Name: "plc-2", ConfigVersion: 1}
	block := &lifecycleBlock{entered: make(chan struct{}), release: make(chan struct{})}
	source.lifecycleBlocks[1] = block
	firstDone := make(chan error, 1)
	go func() { firstDone <- m.Start(context.Background(), 1) }()
	<-block.entered

	if err := m.Start(context.Background(), 2); err != nil {
		t.Fatalf("start device 2: %v", err)
	}
	if factory.count != 1 {
		t.Fatalf("runner count=%d, want device 2 to start while device 1 is blocked", factory.count)
	}
	close(block.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if factory.count != 2 {
		t.Fatalf("runner count=%d, want 2", factory.count)
	}
	_ = m.StopAll(context.Background())
}

func TestStopAllWaitsForInteractiveOperation(t *testing.T) {
	m, source, _ := newTestManager()
	block := &lifecycleBlock{entered: make(chan struct{}), release: make(chan struct{})}
	source.lifecycleBlocks[1] = block
	startDone := make(chan error, 1)
	go func() { startDone <- m.Start(context.Background(), 1) }()
	<-block.entered

	stopAllStarted := make(chan struct{})
	stopAllDone := make(chan error, 1)
	go func() {
		close(stopAllStarted)
		stopAllDone <- m.StopAll(context.Background())
	}()
	<-stopAllStarted
	select {
	case err := <-stopAllDone:
		t.Fatalf("StopAll returned before lifecycle operation completed: %v", err)
	default:
	}
	close(block.release)
	if err := <-startDone; err != nil {
		t.Fatal(err)
	}
	if err := <-stopAllDone; err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Snapshot(context.Background(), 1)
	if err != nil || snapshot.State != StateStopped {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
}

func TestInteractiveLifecycleReleasesOperationAfterFailure(t *testing.T) {
	m, _, factory := newTestManager()
	factory.err = errors.New("factory failed")
	if err := m.Start(context.Background(), 1); err == nil {
		t.Fatal("expected start failure")
	}
	factory.err = nil
	if err := m.Start(context.Background(), 1); err != nil {
		t.Fatalf("retry start: %v", err)
	}
	_ = m.Stop(context.Background(), 1)
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

func TestRestartStartsStoppedDeviceAndPersistsEnabledState(t *testing.T) {
	m, source, factory := newTestManager()
	ctx := context.Background()

	if err := m.Stop(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := m.Restart(ctx, 1); err != nil {
		t.Fatalf("restart stopped device: %v", err)
	}
	if factory.count != 1 {
		t.Fatalf("runner count = %d, want 1", factory.count)
	}
	if !source.devices[1].Enabled {
		t.Fatal("restarted device enabled state was not persisted")
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
