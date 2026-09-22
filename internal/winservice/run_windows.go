//go:build windows

package winservice

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sys/windows/svc"
)

const (
	serviceWaitHint        = 15 * time.Second
	serviceCheckpointDelay = time.Second
)

// IsService reports whether the process was started by the Windows SCM.
func IsService() (bool, error) {
	interactive, err := svc.IsAnInteractiveSession()
	if err != nil {
		return false, fmt.Errorf("detect Windows service session: %w", err)
	}
	return !interactive, nil
}

// Run enters the Windows SCM dispatcher and runs the application callback.
func Run(run func(context.Context, func()) error) error {
	if err := svc.Run(Name, serviceHandler{run: run}); err != nil {
		return fmt.Errorf("run Windows service: %w", err)
	}
	return nil
}

type serviceHandler struct {
	run func(context.Context, func()) error
}

func (h serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, statuses chan<- svc.Status) (bool, uint32) {
	statuses <- svc.Status{State: svc.StartPending, WaitHint: uint32(serviceWaitHint / time.Millisecond)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrors := make(chan error, 1)
	ready := make(chan struct{})
	var readyOnce sync.Once
	go func() {
		runErrors <- h.run(ctx, func() {
			readyOnce.Do(func() { close(ready) })
		})
	}()

	select {
	case err := <-runErrors:
		statuses <- svc.Status{State: svc.Stopped}
		if err != nil {
			return true, 1
		}
		return false, 0
	case <-ready:
	}

	accepted := svc.AcceptStop | svc.AcceptShutdown
	current := svc.Status{State: svc.Running, Accepts: accepted}
	statuses <- current

	var checkpoint uint32
	var ticker *time.Ticker
	var tick <-chan time.Time
	for {
		select {
		case err := <-runErrors:
			statuses <- svc.Status{State: svc.Stopped}
			if err != nil {
				return true, 1
			}
			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				statuses <- current
			case svc.Stop, svc.Shutdown:
				if current.State != svc.StopPending {
					checkpoint = 1
					current = svc.Status{
						State:      svc.StopPending,
						CheckPoint: checkpoint,
						WaitHint:   uint32(serviceWaitHint / time.Millisecond),
					}
					statuses <- current
					cancel()
					ticker = time.NewTicker(serviceCheckpointDelay)
					defer ticker.Stop()
					tick = ticker.C
				}
			default:
				statuses <- current
			}
		case <-tick:
			checkpoint++
			current.CheckPoint = checkpoint
			statuses <- current
		}
	}
}
