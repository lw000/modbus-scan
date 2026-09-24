package kafkapub

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"modbus-scan/internal/model"
)

type fakeProducer struct {
	mu     sync.Mutex
	sent   []ProducerMessage
	closed bool
}

func (p *fakeProducer) Send(_ context.Context, message ProducerMessage) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, message)
	return nil
}
func (p *fakeProducer) Close() error { p.mu.Lock(); p.closed = true; p.mu.Unlock(); return nil }

type fakeProducerFactory struct {
	producer Producer
	err      error
}

func (f *fakeProducerFactory) Open(context.Context, model.KafkaSettings) (Producer, error) {
	return f.producer, f.err
}

func validSettings(enabled bool) model.KafkaSettings {
	return model.KafkaSettings{Enabled: enabled, Brokers: []string{"127.0.0.1:9092"}, ClientID: "test", KafkaVersion: "3.0.0", QueueCapacity: 4}
}

func TestManagerStartsDisabledWithoutOpeningProducer(t *testing.T) {
	coordinator := NewCoordinator(4, time.Now, nil)
	manager := NewManager(coordinator, &fakeProducerFactory{err: errors.New("must not open")})
	if err := manager.Start(context.Background(), validSettings(false)); err != nil {
		t.Fatal(err)
	}
	if got := manager.Status().State; got != KafkaStateDisabled {
		t.Fatalf("state = %q", got)
	}
}

func TestManagerAppliesProducerAndSendsJSON(t *testing.T) {
	producer := &fakeProducer{}
	coordinator := NewCoordinator(4, time.Now, nil)
	manager := NewManager(coordinator, &fakeProducerFactory{producer: producer})
	if err := manager.Start(context.Background(), validSettings(true)); err != nil {
		t.Fatal(err)
	}
	coordinator.ApplyDeviceConfig(model.DeviceKafkaConfig{DeviceID: 9, Enabled: true, Topic: "plc", Mode: model.KafkaModeFull, FullIntervalSec: 60})
	coordinator.Submit(ScanSnapshot{DeviceID: 9, DeviceName: "PLC", CurrentValues: map[string]any{"A": 1.0}})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		producer.mu.Lock()
		count := len(producer.sent)
		producer.mu.Unlock()
		if count == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	producer.mu.Lock()
	defer producer.mu.Unlock()
	if len(producer.sent) != 1 || producer.sent[0].Topic != "plc" || producer.sent[0].Key != "9" {
		t.Fatalf("sent = %#v", producer.sent)
	}
}

func TestManagerRetainsOldProducerWhenReplacementFails(t *testing.T) {
	old := &fakeProducer{}
	factory := &fakeProducerFactory{producer: old}
	manager := NewManager(NewCoordinator(4, time.Now, nil), factory)
	if err := manager.Start(context.Background(), validSettings(true)); err != nil {
		t.Fatal(err)
	}
	factory.err = errors.New("offline")
	if _, err := manager.ApplySettings(context.Background(), validSettings(true)); err == nil {
		t.Fatal("replacement error = nil")
	}
	old.mu.Lock()
	defer old.mu.Unlock()
	if old.closed {
		t.Fatal("old producer was closed")
	}
}
