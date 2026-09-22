package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"modbus-scan/internal/winservice"
)

type recordingManager struct {
	operation      string
	executablePath string
	configPath     string
	state          winservice.State
	err            error
}

func (m *recordingManager) Install(executablePath, configPath string) error {
	m.operation = "install"
	m.executablePath = executablePath
	m.configPath = configPath
	return m.err
}

func (m *recordingManager) Uninstall() error {
	m.operation = "uninstall"
	return m.err
}

func (m *recordingManager) Start() error {
	m.operation = "start"
	return m.err
}

func (m *recordingManager) Stop(context.Context) error {
	m.operation = "stop"
	return m.err
}

func (m *recordingManager) Restart(context.Context) error {
	m.operation = "restart"
	return m.err
}

func (m *recordingManager) Status() (winservice.State, error) {
	m.operation = "status"
	return m.state, m.err
}

func TestDispatchInstallUsesAbsolutePaths(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	mgr := &recordingManager{}
	var stdout bytes.Buffer
	handled, err := dispatchCommand(context.Background(), []string{"install", "-config", configPath}, filepath.Join("bin", "modbus-scan.exe"), mgr, &stdout)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || mgr.operation != "install" {
		t.Fatalf("handled = %v, operation = %q", handled, mgr.operation)
	}
	if !filepath.IsAbs(mgr.executablePath) || !filepath.IsAbs(mgr.configPath) {
		t.Fatalf("paths are not absolute: executable=%q config=%q", mgr.executablePath, mgr.configPath)
	}
}

func TestDispatchServiceCommands(t *testing.T) {
	tests := []string{"uninstall", "start", "stop", "restart"}
	for _, command := range tests {
		t.Run(command, func(t *testing.T) {
			mgr := &recordingManager{}
			handled, err := dispatchCommand(context.Background(), []string{command}, "modbus-scan.exe", mgr, &bytes.Buffer{})
			if err != nil {
				t.Fatal(err)
			}
			if !handled || mgr.operation != command {
				t.Fatalf("handled = %v, operation = %q", handled, mgr.operation)
			}
		})
	}
}

func TestDispatchStatusPrintsStableState(t *testing.T) {
	mgr := &recordingManager{state: winservice.StateRunning}
	var stdout bytes.Buffer
	handled, err := dispatchCommand(context.Background(), []string{"status"}, "modbus-scan.exe", mgr, &stdout)
	if err != nil || !handled {
		t.Fatalf("handled = %v, error = %v", handled, err)
	}
	if got, want := stdout.String(), "running\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestDispatchLeavesLegacyFlagsUnhandled(t *testing.T) {
	handled, err := dispatchCommand(context.Background(), []string{"-config", "config.toml"}, "modbus-scan.exe", &recordingManager{}, &bytes.Buffer{})
	if err != nil || handled {
		t.Fatalf("handled = %v, error = %v", handled, err)
	}
}

func TestDispatchRejectsInvalidServiceArguments(t *testing.T) {
	tests := [][]string{
		{"unknown"},
		{"start", "extra"},
		{"install", "-unexpected"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout bytes.Buffer
			handled, err := dispatchCommand(context.Background(), args, "modbus-scan.exe", &recordingManager{}, &stdout)
			if !handled || err == nil {
				t.Fatalf("handled = %v, error = %v", handled, err)
			}
			if !strings.Contains(stdout.String(), "Usage:") {
				t.Fatalf("output = %q, want usage", stdout.String())
			}
		})
	}
}

func TestDispatchWrapsManagerError(t *testing.T) {
	sentinel := errors.New("access denied")
	handled, err := dispatchCommand(context.Background(), []string{"start"}, "modbus-scan.exe", &recordingManager{err: sentinel}, &bytes.Buffer{})
	if !handled || !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "start service") {
		t.Fatalf("handled = %v, error = %v", handled, err)
	}
}
