// Package winservice integrates the application with Windows service management.
package winservice

import "context"

const (
	// Name is the stable Windows service name.
	Name = "modbus-scan"
	// DisplayName is the human-readable Windows service name.
	DisplayName = "Modbus Scan"
)

// State is a stable, printable service state.
type State string

const (
	StateStopped         State = "stopped"
	StateStartPending    State = "start_pending"
	StateStopPending     State = "stop_pending"
	StateRunning         State = "running"
	StateContinuePending State = "continue_pending"
	StatePausePending    State = "pause_pending"
	StatePaused          State = "paused"
	StateUnknown         State = "unknown"
)

// Manager controls the installed Windows service.
type Manager interface {
	Install(executablePath, configPath string) error
	Uninstall() error
	Start() error
	Stop(context.Context) error
	Restart(context.Context) error
	Status() (State, error)
}
