package kafkapub

import (
	"context"
	"reflect"
	"testing"
	"time"

	"modbus-scan/internal/model"
)

func nextMessage(t *testing.T, c *Coordinator) Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	message, err := c.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func assertNoQueuedMessage(t *testing.T, c *Coordinator) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if message, err := c.Next(ctx); err == nil {
		t.Fatalf("unexpected message = %#v", message)
	}
}

func TestChangeModeBuildsBaselineThenPublishesOnlyChanges(t *testing.T) {
	now := time.Date(2026, 9, 24, 2, 0, 0, 0, time.UTC)
	c := NewCoordinator(4, func() time.Time { return now }, nil)
	c.ApplyDeviceConfig(model.DeviceKafkaConfig{DeviceID: 1, Enabled: true, Topic: "plc", Mode: model.KafkaModeChange, FullIntervalSec: 60})
	c.Submit(ScanSnapshot{DeviceID: 1, DeviceName: "PLC", CollectedAt: now, SuccessfulValues: map[string]any{"A": 1.0, "B": true}, CurrentValues: map[string]any{"A": 1.0, "B": true}})
	assertNoQueuedMessage(t, c)
	now = now.Add(time.Second)
	c.Submit(ScanSnapshot{DeviceID: 1, DeviceName: "PLC", CollectedAt: now, SuccessfulValues: map[string]any{"A": 2.0, "B": true}, CurrentValues: map[string]any{"A": 2.0, "B": true}})
	got := nextMessage(t, c)
	if got.Type != model.KafkaModeChange || got.Topic != "plc" || !reflect.DeepEqual(got.Points, map[string]any{"A": 2.0}) {
		t.Fatalf("message = %#v", got)
	}
}

func TestChangeModeIgnoresMissingValuesAndResetsOnTopicChange(t *testing.T) {
	now := time.Now().UTC()
	c := NewCoordinator(4, func() time.Time { return now }, nil)
	cfg := model.DeviceKafkaConfig{DeviceID: 1, Enabled: true, Topic: "first", Mode: model.KafkaModeChange, FullIntervalSec: 60}
	c.ApplyDeviceConfig(cfg)
	c.Submit(ScanSnapshot{DeviceID: 1, SuccessfulValues: map[string]any{"A": 1.0}, CurrentValues: map[string]any{"A": 1.0}})
	c.Submit(ScanSnapshot{DeviceID: 1, SuccessfulValues: map[string]any{}, CurrentValues: map[string]any{"A": 1.0}})
	assertNoQueuedMessage(t, c)
	cfg.Topic = "second"
	c.ApplyDeviceConfig(cfg)
	c.Submit(ScanSnapshot{DeviceID: 1, SuccessfulValues: map[string]any{"A": 2.0}, CurrentValues: map[string]any{"A": 2.0}})
	assertNoQueuedMessage(t, c)
}

func TestFullModePublishesImmediatelyThenAtInterval(t *testing.T) {
	now := time.Date(2026, 9, 24, 2, 0, 0, 0, time.UTC)
	c := NewCoordinator(4, func() time.Time { return now }, nil)
	c.ApplyDeviceConfig(model.DeviceKafkaConfig{DeviceID: 1, Enabled: true, Topic: "plc", Mode: model.KafkaModeFull, FullIntervalSec: 30})
	snapshot := ScanSnapshot{DeviceID: 1, DeviceName: "PLC", CollectedAt: now, SuccessfulValues: map[string]any{"A": 1.0}, CurrentValues: map[string]any{"A": 1.0, "B": true}}
	c.Submit(snapshot)
	if got := nextMessage(t, c); got.Type != model.KafkaModeFull || len(got.Points) != 2 {
		t.Fatalf("first full message = %#v", got)
	}
	now = now.Add(29 * time.Second)
	c.Submit(snapshot)
	assertNoQueuedMessage(t, c)
	now = now.Add(time.Second)
	snapshot.CollectedAt = now
	c.Submit(snapshot)
	if got := nextMessage(t, c); got.Type != model.KafkaModeFull {
		t.Fatalf("periodic message = %#v", got)
	}
}

func TestCoordinatorDropsOldestMessage(t *testing.T) {
	var dropped uint64
	c := NewCoordinator(2, time.Now, func(total uint64) { dropped = total })
	for id := int64(1); id <= 3; id++ {
		c.ApplyDeviceConfig(model.DeviceKafkaConfig{DeviceID: id, Enabled: true, Topic: "plc", Mode: model.KafkaModeFull, FullIntervalSec: 60})
		c.Submit(ScanSnapshot{DeviceID: id, CurrentValues: map[string]any{"A": id}})
	}
	if dropped != 1 || c.Dropped() != 1 {
		t.Fatalf("dropped callback=%d counter=%d", dropped, c.Dropped())
	}
	if got := nextMessage(t, c); got.Device.ID != 2 {
		t.Fatalf("first retained message = %#v", got)
	}
	if got := nextMessage(t, c); got.Device.ID != 3 {
		t.Fatalf("second retained message = %#v", got)
	}
}
