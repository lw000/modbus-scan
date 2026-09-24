package kafkapub

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"
	"time"

	"modbus-scan/internal/model"
)

const (
	KafkaStateDisabled     = "disabled"
	KafkaStateConnecting   = "connecting"
	KafkaStateOnline       = "online"
	KafkaStateReconnecting = "reconnecting"
	KafkaStateStopping     = "stopping"
	KafkaStateError        = "error"
)

// Manager owns the live producer and queue worker.
type Manager struct {
	coordinator *Coordinator
	factory     ProducerFactory
	mu          sync.RWMutex
	producer    Producer
	settings    model.KafkaSettings
	status      model.KafkaStatus
	cancel      context.CancelFunc
	done        chan struct{}
	notify      chan struct{}
	workerCtx   context.Context
}

// NewManager creates a Kafka lifecycle manager.
func NewManager(coordinator *Coordinator, factory ProducerFactory) *Manager {
	return &Manager{coordinator: coordinator, factory: factory, status: model.KafkaStatus{State: KafkaStateDisabled}, notify: make(chan struct{}, 1)}
}

// Start starts the queue worker and applies persisted settings.
func (m *Manager) Start(ctx context.Context, settings model.KafkaSettings) error {
	m.mu.Lock()
	if m.cancel == nil {
		workerCtx, cancel := context.WithCancel(ctx)
		m.cancel, m.done, m.workerCtx = cancel, make(chan struct{}), workerCtx
		go m.run(workerCtx)
	}
	m.mu.Unlock()
	if !settings.Enabled {
		m.coordinator.SetEnabled(false)
		m.mu.Lock()
		m.settings, m.status = settings, model.KafkaStatus{State: KafkaStateDisabled}
		m.mu.Unlock()
		return nil
	}
	producer, err := m.factory.Open(ctx, settings)
	if err != nil {
		m.mu.Lock()
		m.settings, m.status = settings, model.KafkaStatus{State: KafkaStateReconnecting, LastError: err.Error()}
		m.mu.Unlock()
		m.coordinator.SetEnabled(true)
		go m.reconnect(m.workerCtx)
		return nil
	}
	m.install(settings, producer)
	return nil
}

func (m *Manager) reconnect(ctx context.Context) {
	for delay := time.Second; ; delay = min(delay*2, 30*time.Second) {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		m.mu.RLock()
		settings := m.settings
		m.mu.RUnlock()
		producer, err := m.factory.Open(ctx, settings)
		if err == nil {
			m.install(settings, producer)
			return
		}
		m.mu.Lock()
		m.status.LastError = err.Error()
		m.mu.Unlock()
	}
}

func (m *Manager) install(settings model.KafkaSettings, producer Producer) {
	m.coordinator.SetEnabled(true)
	m.mu.Lock()
	old := m.producer
	m.settings, m.producer, m.status = settings, producer, model.KafkaStatus{State: KafkaStateOnline, DroppedMessages: m.coordinator.Dropped()}
	m.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	select {
	case m.notify <- struct{}{}:
	default:
	}
}

// ApplySettings atomically replaces or disables the producer.
func (m *Manager) ApplySettings(ctx context.Context, settings model.KafkaSettings) (model.KafkaStatus, error) {
	if !settings.Enabled {
		m.coordinator.SetEnabled(false)
		m.mu.Lock()
		old := m.producer
		m.producer = nil
		m.settings = settings
		m.status = model.KafkaStatus{State: KafkaStateDisabled, DroppedMessages: m.coordinator.Dropped()}
		m.mu.Unlock()
		if old != nil {
			_ = old.Close()
		}
		return m.Status(), nil
	}
	producer, err := m.factory.Open(ctx, settings)
	if err != nil {
		return m.Status(), err
	}
	m.install(settings, producer)
	return m.Status(), nil
}

// ApplyDeviceConfig hot-applies one device policy.
func (m *Manager) ApplyDeviceConfig(cfg model.DeviceKafkaConfig) {
	m.coordinator.ApplyDeviceConfig(cfg)
}

// Status returns a detached runtime status.
func (m *Manager) Status() model.KafkaStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := m.status
	status.DroppedMessages = m.coordinator.Dropped()
	return status
}

func (m *Manager) run(ctx context.Context) {
	defer close(m.done)
	for {
		m.mu.RLock()
		producer := m.producer
		m.mu.RUnlock()
		if producer == nil {
			select {
			case <-ctx.Done():
				return
			case <-m.notify:
				continue
			}
		}
		message, err := m.coordinator.Next(ctx)
		if err != nil {
			return
		}
		body, err := json.Marshal(message)
		if err != nil {
			continue
		}
		if err := producer.Send(ctx, ProducerMessage{Topic: message.Topic, Key: strconv.FormatInt(message.Device.ID, 10), Value: body}); err != nil {
			m.mu.Lock()
			m.status = model.KafkaStatus{State: KafkaStateReconnecting, LastError: fmt.Sprintf("send Kafka message: %v", err), DroppedMessages: m.coordinator.Dropped()}
			m.mu.Unlock()
		}
	}
}

// Stop stops admission, cancels the worker, and closes the producer.
func (m *Manager) Stop(ctx context.Context) error {
	m.coordinator.StopAccepting()
	m.mu.Lock()
	cancel, done, producer := m.cancel, m.done, m.producer
	m.status.State = KafkaStateStopping
	m.producer = nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if producer != nil {
		return producer.Close()
	}
	return nil
}
