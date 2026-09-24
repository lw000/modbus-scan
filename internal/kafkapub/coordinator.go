package kafkapub

import (
	"context"
	"reflect"
	"sync"
	"time"

	"modbus-scan/internal/model"
)

// ScanSnapshot is one completed device scan with detached value maps.
type ScanSnapshot struct {
	DeviceID         int64
	DeviceName       string
	CollectedAt      time.Time
	SuccessfulValues map[string]any
	CurrentValues    map[string]any
}

// DeviceIdentity identifies the source device in the Kafka contract.
type DeviceIdentity struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Message is the versioned Kafka JSON contract plus its routing Topic.
type Message struct {
	SchemaVersion int            `json:"schema_version"`
	Type          string         `json:"message_type"`
	Device        DeviceIdentity `json:"device"`
	CollectedAt   time.Time      `json:"collected_at"`
	Points        map[string]any `json:"points"`
	Topic         string         `json:"-"`
}

type deviceState struct {
	lastValues       map[string]any
	lastFullQueuedAt time.Time
}

// Coordinator applies device policies and owns the bounded outbound queue.
type Coordinator struct {
	mu       sync.Mutex
	configs  map[int64]model.DeviceKafkaConfig
	states   map[int64]*deviceState
	now      func() time.Time
	queue    *messageQueue
	onDrop   func(uint64)
	lastWarn time.Time
	enabled  bool
}

// NewCoordinator creates a coordinator with oldest-drop overflow behavior.
func NewCoordinator(capacity int, now func() time.Time, onDrop func(uint64)) *Coordinator {
	if now == nil {
		now = time.Now
	}
	return &Coordinator{
		configs: make(map[int64]model.DeviceKafkaConfig),
		states:  make(map[int64]*deviceState),
		now:     now,
		queue:   newMessageQueue(capacity),
		onDrop:  onDrop,
		enabled: true,
	}
}

// ApplyDeviceConfig hot-applies a device policy and resets incompatible state.
func (c *Coordinator) ApplyDeviceConfig(cfg model.DeviceKafkaConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	previous, exists := c.configs[cfg.DeviceID]
	c.configs[cfg.DeviceID] = cfg
	if !cfg.Enabled || !exists || !previous.Enabled || previous.Topic != cfg.Topic || previous.Mode != cfg.Mode {
		delete(c.states, cfg.DeviceID)
	}
}

// ResetDevice clears comparison and scheduling state for a stopped runner.
func (c *Coordinator) ResetDevice(deviceID int64) {
	c.mu.Lock()
	delete(c.states, deviceID)
	c.mu.Unlock()
}

// Submit evaluates one scan and never waits for Kafka network I/O.
func (c *Coordinator) Submit(snapshot ScanSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.enabled {
		return
	}
	cfg, ok := c.configs[snapshot.DeviceID]
	if !ok || !cfg.Enabled {
		return
	}
	state := c.states[snapshot.DeviceID]
	if state == nil {
		state = &deviceState{lastValues: make(map[string]any)}
		c.states[snapshot.DeviceID] = state
	}
	if cfg.Mode == model.KafkaModeChange {
		c.submitChanges(cfg, state, snapshot)
		return
	}
	c.submitFull(cfg, state, snapshot)
}

func (c *Coordinator) submitChanges(cfg model.DeviceKafkaConfig, state *deviceState, snapshot ScanSnapshot) {
	if len(state.lastValues) == 0 {
		for tag, value := range snapshot.SuccessfulValues {
			state.lastValues[tag] = value
		}
		return
	}
	changes := make(map[string]any)
	for tag, value := range snapshot.SuccessfulValues {
		if previous, exists := state.lastValues[tag]; !exists || !reflect.DeepEqual(previous, value) {
			changes[tag] = value
		}
		state.lastValues[tag] = value
	}
	if len(changes) != 0 {
		c.enqueue(cfg, snapshot, model.KafkaModeChange, changes)
	}
}

func (c *Coordinator) submitFull(cfg model.DeviceKafkaConfig, state *deviceState, snapshot ScanSnapshot) {
	now := c.now().UTC()
	if !state.lastFullQueuedAt.IsZero() && now.Sub(state.lastFullQueuedAt) < time.Duration(cfg.FullIntervalSec)*time.Second {
		return
	}
	if len(snapshot.CurrentValues) == 0 {
		return
	}
	c.enqueue(cfg, snapshot, model.KafkaModeFull, snapshot.CurrentValues)
	state.lastFullQueuedAt = now
}

func (c *Coordinator) enqueue(cfg model.DeviceKafkaConfig, snapshot ScanSnapshot, messageType string, points map[string]any) {
	collectedAt := snapshot.CollectedAt.UTC()
	if collectedAt.IsZero() {
		collectedAt = c.now().UTC()
	}
	message := Message{
		SchemaVersion: 1,
		Type:          messageType,
		Device:        DeviceIdentity{ID: snapshot.DeviceID, Name: snapshot.DeviceName},
		CollectedAt:   collectedAt,
		Points:        cloneValues(points),
		Topic:         cfg.Topic,
	}
	total, dropped := c.queue.push(message)
	if dropped && c.onDrop != nil {
		now := c.now()
		if c.lastWarn.IsZero() || now.Sub(c.lastWarn) >= time.Minute {
			c.lastWarn = now
			c.onDrop(total)
		}
	}
}

func cloneValues(values map[string]any) map[string]any {
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

// Next waits for the next outbound message or context cancellation.
func (c *Coordinator) Next(ctx context.Context) (Message, error) { return c.queue.next(ctx) }

// StopAccepting rejects future messages while leaving queued messages drainable.
func (c *Coordinator) StopAccepting() { c.queue.stopAccepting() }

// Dropped returns the cumulative queue overflow count.
func (c *Coordinator) Dropped() uint64 { return c.queue.droppedCount() }

// SetEnabled controls global admission; disabling also discards queued messages.
func (c *Coordinator) SetEnabled(enabled bool) {
	c.mu.Lock()
	c.enabled = enabled
	c.mu.Unlock()
	if !enabled {
		c.queue.discardAll()
	}
}
