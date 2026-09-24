package collector

import (
	"testing"
	"time"

	"modbus-scan/internal/kafkapub"
	"modbus-scan/internal/model"
	devruntime "modbus-scan/internal/runtime"
	"modbus-scan/internal/udm"
)

type snapshotRecorder struct{ snapshots []kafkapub.ScanSnapshot }

func (r *snapshotRecorder) Submit(snapshot kafkapub.ScanSnapshot) {
	r.snapshots = append(r.snapshots, snapshot)
}
func (r *snapshotRecorder) ResetDevice(int64) {}

func TestRuntimeState(t *testing.T) {
	tests := []struct {
		name string
		got  int32
		want string
	}{
		{name: "online", got: StateOnline, want: devruntime.StateOnline},
		{name: "reconnecting", got: StateReconnecting, want: devruntime.StateReconnecting},
		{name: "offline", got: StateOffline, want: devruntime.StateOffline},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runtimeState(tt.got); got != tt.want {
				t.Fatalf("runtimeState(%d) = %q, want %q", tt.got, got, tt.want)
			}
		})
	}
}

func TestRuntimePublishesDeviceSnapshot(t *testing.T) {
	recorder := &snapshotRecorder{}
	runner := &runtimeRunner{device: model.Device{ID: 7, Name: "PLC"}, values: udm.New(), snapshots: recorder}
	runner.values.UpdateAt("A", 1.0, time.Now().UTC())
	runner.publishSnapshot(map[string]any{"A": 1.0}, time.Now().UTC())
	if len(recorder.snapshots) != 1 || recorder.snapshots[0].DeviceID != 7 || recorder.snapshots[0].CurrentValues["A"] != 1.0 {
		t.Fatalf("snapshots = %#v", recorder.snapshots)
	}
}
