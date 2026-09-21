package collector

import (
	"context"
	"testing"
	"time"

	"modbus-scan/internal/config"
	"modbus-scan/internal/model"
	devruntime "modbus-scan/internal/runtime"
	"modbus-scan/internal/udm"
)

func TestReconnectStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cm := NewConnManager(&config.DeviceConfig{Modbus: config.ModbusConfig{Address: "127.0.0.1", Port: 1, SlaveID: 1, TimeoutSec: 1}}, ctx)
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		cm.ReconnectWithBackoff()
	}()
	select {
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("reconnect did not stop after context cancellation")
	}
}

func TestCollectorUpdateValuePublishesSameTimestamp(t *testing.T) {
	var tag string
	var event udm.Value
	u := udm.New()
	c := &Collector{udm: u, publish: func(gotTag string, got udm.Value) { tag, event = gotTag, got }}
	c.updateValue("Speed", float64(42))
	snapshot := u.Snapshot()["Speed"]
	if tag != "Speed" || event.Value != float64(42) || !event.UpdatedAt.Equal(snapshot.UpdatedAt) {
		t.Fatalf("tag=%q event=%#v snapshot=%#v", tag, event, snapshot)
	}
}

func TestRuntimeFactoryRejectsEmptyPoints(t *testing.T) {
	factory := NewRuntimeFactory()
	_, err := factory.New(model.Device{ID: 1, Name: "plc"}, nil, func(devruntime.StatusEvent) {})
	if err == nil {
		t.Fatal("expected empty points error")
	}
}

func TestApplyGlobalByteOrder(t *testing.T) {
	tests := map[string][]byte{
		"ABCD": {0x01, 0x02, 0x03, 0x04},
		"DCBA": {0x04, 0x03, 0x02, 0x01},
		"CDAB": {0x03, 0x04, 0x01, 0x02},
		"BADC": {0x02, 0x01, 0x04, 0x03},
	}
	for order, want := range tests {
		t.Run(order, func(t *testing.T) {
			collector := &Collector{byteOrder: order}
			got := collector.applyGlobalByteOrder([]byte{0x01, 0x02, 0x03, 0x04})
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("got %v, want %v", got, want)
				}
			}
		})
	}
}
