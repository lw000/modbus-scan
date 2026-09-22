//go:build !windows

package winservice

import "context"

// IsService always reports false outside Windows.
func IsService() (bool, error) {
	return false, nil
}

// Run reports that Windows service execution is unavailable.
func Run(func(context.Context, func()) error) error {
	return errUnsupported
}
