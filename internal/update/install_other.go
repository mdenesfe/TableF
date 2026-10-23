//go:build !darwin && !windows

package update

import (
	"context"
	"errors"
)

// Install isn't supported here: Linux builds come from source or packages.
func Install(context.Context, string) (string, error) {
	return "", errors.New("updating in place isn't supported on this platform; download the update from the release page")
}

// Relaunch is a no-op where Install isn't supported.
func Relaunch(string) error { return nil }

// Cleanup is a no-op where Install isn't supported.
func Cleanup() {}
