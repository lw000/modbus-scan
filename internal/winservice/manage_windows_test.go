//go:build windows

package winservice

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
)

func TestMapState(t *testing.T) {
	tests := []struct {
		input svc.State
		want  State
	}{
		{svc.Stopped, StateStopped},
		{svc.StartPending, StateStartPending},
		{svc.StopPending, StateStopPending},
		{svc.Running, StateRunning},
		{svc.ContinuePending, StateContinuePending},
		{svc.PausePending, StatePausePending},
		{svc.Paused, StatePaused},
		{svc.State(999), StateUnknown},
	}
	for _, test := range tests {
		if got := mapState(test.input); got != test.want {
			t.Errorf("mapState(%d) = %q, want %q", test.input, got, test.want)
		}
	}
}

func TestWaitForStatePollsUntilWanted(t *testing.T) {
	states := []svc.State{svc.StartPending, svc.StartPending, svc.Running}
	queries := 0
	err := waitForState(context.Background(), func() (svc.Status, error) {
		state := states[queries]
		queries++
		return svc.Status{State: state}, nil
	}, svc.Running, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if queries != 3 {
		t.Fatalf("queries = %d, want 3", queries)
	}
}

func TestWaitForStateReturnsQueryError(t *testing.T) {
	sentinel := errors.New("query failed")
	err := waitForState(context.Background(), func() (svc.Status, error) {
		return svc.Status{}, sentinel
	}, svc.Running, time.Millisecond)
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want query failure", err)
	}
}

func TestWaitForStateStopsOnContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitForState(ctx, func() (svc.Status, error) {
		return svc.Status{State: svc.StopPending}, nil
	}, svc.Stopped, time.Millisecond)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}
