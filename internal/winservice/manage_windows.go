//go:build windows

package winservice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const servicePollInterval = 200 * time.Millisecond

type windowsManager struct{}

// NewManager creates a manager backed by the Windows SCM.
func NewManager() Manager {
	return windowsManager{}
}

func (windowsManager) Install(executablePath, configPath string) (retErr error) {
	manager, err := mgr.Connect()
	if err != nil {
		return administrativeError("connect to Windows service manager", err)
	}
	defer func() {
		retErr = errors.Join(retErr, closeError("disconnect Windows service manager", manager.Disconnect()))
	}()

	service, err := manager.CreateService(Name, executablePath, mgr.Config{
		DisplayName: DisplayName,
		StartType:   mgr.StartAutomatic,
	}, "-config", configPath)
	if err != nil {
		return administrativeError("create Windows service", err)
	}
	defer func() {
		retErr = errors.Join(retErr, closeError("close Windows service", service.Close()))
	}()
	return nil
}

func (windowsManager) Uninstall() (retErr error) {
	manager, service, err := openService()
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, closeError("close Windows service", service.Close()))
		retErr = errors.Join(retErr, closeError("disconnect Windows service manager", manager.Disconnect()))
	}()
	status, err := service.Query()
	if err != nil {
		return fmt.Errorf("query Windows service before uninstall: %w", err)
	}
	if status.State != svc.Stopped {
		return fmt.Errorf("Windows service must be stopped before uninstall (current state: %s)", mapState(status.State))
	}
	if err := service.Delete(); err != nil {
		return administrativeError("delete Windows service", err)
	}
	return nil
}

func (windowsManager) Start() (retErr error) {
	manager, service, err := openService()
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, closeError("close Windows service", service.Close()))
		retErr = errors.Join(retErr, closeError("disconnect Windows service manager", manager.Disconnect()))
	}()
	status, err := service.Query()
	if err != nil {
		return fmt.Errorf("query Windows service before start: %w", err)
	}
	if status.State == svc.Running {
		return nil
	}
	if status.State != svc.StartPending {
		if err := service.Start(); err != nil {
			return administrativeError("start Windows service", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := waitForState(ctx, service.Query, svc.Running, servicePollInterval); err != nil {
		return fmt.Errorf("wait for Windows service to start: %w", err)
	}
	return nil
}

func (windowsManager) Stop(ctx context.Context) (retErr error) {
	manager, service, err := openService()
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, closeError("close Windows service", service.Close()))
		retErr = errors.Join(retErr, closeError("disconnect Windows service manager", manager.Disconnect()))
	}()
	if err := stopService(ctx, service); err != nil {
		return err
	}
	return nil
}

func (windowsManager) Restart(ctx context.Context) (retErr error) {
	manager, service, err := openService()
	if err != nil {
		return err
	}
	defer func() {
		retErr = errors.Join(retErr, closeError("close Windows service", service.Close()))
		retErr = errors.Join(retErr, closeError("disconnect Windows service manager", manager.Disconnect()))
	}()
	if err := stopService(ctx, service); err != nil {
		return err
	}
	if err := service.Start(); err != nil {
		return administrativeError("restart Windows service", err)
	}
	if err := waitForState(ctx, service.Query, svc.Running, servicePollInterval); err != nil {
		return fmt.Errorf("wait for restarted Windows service: %w", err)
	}
	return nil
}

func (windowsManager) Status() (state State, retErr error) {
	manager, service, err := openService()
	if err != nil {
		return StateUnknown, err
	}
	defer func() {
		retErr = errors.Join(retErr, closeError("close Windows service", service.Close()))
		retErr = errors.Join(retErr, closeError("disconnect Windows service manager", manager.Disconnect()))
	}()
	status, err := service.Query()
	if err != nil {
		return StateUnknown, fmt.Errorf("query Windows service: %w", err)
	}
	return mapState(status.State), nil
}

func openService() (*mgr.Mgr, *mgr.Service, error) {
	manager, err := mgr.Connect()
	if err != nil {
		return nil, nil, administrativeError("connect to Windows service manager", err)
	}
	service, err := manager.OpenService(Name)
	if err != nil {
		closeErr := manager.Disconnect()
		return nil, nil, errors.Join(administrativeError("open Windows service", err), closeError("disconnect Windows service manager", closeErr))
	}
	return manager, service, nil
}

func stopService(ctx context.Context, service *mgr.Service) error {
	status, err := service.Query()
	if err != nil {
		return fmt.Errorf("query Windows service before stop: %w", err)
	}
	if status.State == svc.Stopped {
		return nil
	}
	if status.State != svc.StopPending {
		if _, err := service.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return administrativeError("request Windows service stop", err)
		}
	}
	if err := waitForState(ctx, service.Query, svc.Stopped, servicePollInterval); err != nil {
		return fmt.Errorf("wait for Windows service to stop: %w", err)
	}
	return nil
}

func waitForState(ctx context.Context, query func() (svc.Status, error), wanted svc.State, interval time.Duration) error {
	for {
		status, err := query()
		if err != nil {
			return fmt.Errorf("query Windows service status: %w", err)
		}
		if status.State == wanted {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func mapState(state svc.State) State {
	switch state {
	case svc.Stopped:
		return StateStopped
	case svc.StartPending:
		return StateStartPending
	case svc.StopPending:
		return StateStopPending
	case svc.Running:
		return StateRunning
	case svc.ContinuePending:
		return StateContinuePending
	case svc.PausePending:
		return StatePausePending
	case svc.Paused:
		return StatePaused
	default:
		return StateUnknown
	}
}

func administrativeError(operation string, err error) error {
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return fmt.Errorf("%s: please run in an administrator PowerShell: %w", operation, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func closeError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
