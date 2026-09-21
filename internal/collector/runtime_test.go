package collector

import (
	"testing"

	devruntime "modbus-scan/internal/runtime"
)

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
