//go:build !windows

package winservice

import (
	"context"
	"errors"
)

var errUnsupported = errors.New("Windows service management is only supported on Windows")

type unsupportedManager struct{}

// NewManager creates a manager that reports Windows services as unsupported.
func NewManager() Manager {
	return unsupportedManager{}
}

func (unsupportedManager) Install(string, string) error { return errUnsupported }
func (unsupportedManager) Uninstall() error             { return errUnsupported }
func (unsupportedManager) Start() error                 { return errUnsupported }
func (unsupportedManager) Stop(context.Context) error   { return errUnsupported }
func (unsupportedManager) Restart(context.Context) error {
	return errUnsupported
}
func (unsupportedManager) Status() (State, error) { return StateUnknown, errUnsupported }
