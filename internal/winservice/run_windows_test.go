//go:build windows

package winservice

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/sys/windows/svc"
)

func TestExecuteCancelsApplicationOnStop(t *testing.T) {
	testExecuteCancelsApplication(t, svc.Stop)
}

func TestExecuteCancelsApplicationOnShutdown(t *testing.T) {
	testExecuteCancelsApplication(t, svc.Shutdown)
}

func testExecuteCancelsApplication(t *testing.T, command svc.Cmd) {
	t.Helper()
	requests := make(chan svc.ChangeRequest, 1)
	statuses := make(chan svc.Status, 8)
	started := make(chan struct{})
	handler := serviceHandler{run: func(ctx context.Context, ready func()) error {
		ready()
		close(started)
		<-ctx.Done()
		return nil
	}}
	done := make(chan struct {
		specific bool
		code     uint32
	}, 1)
	go func() {
		specific, code := handler.Execute(nil, requests, statuses)
		done <- struct {
			specific bool
			code     uint32
		}{specific: specific, code: code}
	}()
	<-started
	requests <- svc.ChangeRequest{Cmd: command}
	result := <-done
	if result.specific || result.code != 0 {
		t.Fatalf("specific = %v, code = %d", result.specific, result.code)
	}
	assertStateSequence(t, statuses, svc.StartPending, svc.Running, svc.StopPending, svc.Stopped)
}

func TestExecuteReportsApplicationFailure(t *testing.T) {
	sentinel := errors.New("startup failed")
	statuses := make(chan svc.Status, 8)
	handler := serviceHandler{run: func(context.Context, func()) error { return sentinel }}
	specific, code := handler.Execute(nil, make(chan svc.ChangeRequest), statuses)
	if !specific || code == 0 {
		t.Fatalf("specific = %v, code = %d", specific, code)
	}
	assertStateSequence(t, statuses, svc.StartPending, svc.Stopped)
}

func assertStateSequence(t *testing.T, statuses <-chan svc.Status, wanted ...svc.State) {
	t.Helper()
	for index, want := range wanted {
		select {
		case status := <-statuses:
			if status.State != want {
				t.Fatalf("status %d = %d, want %d", index, status.State, want)
			}
		default:
			t.Fatalf("missing status %d (%d)", index, want)
		}
	}
}
