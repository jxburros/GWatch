//go:build !linux && !darwin && !windows

package sysmetrics

import "context"

// collect has no implementation on this platform. Callers surface
// ErrUnsupported as a check failure that says so, rather than reporting a
// machine with no processor and no disks.
func collect(context.Context) (*raw, error) { return nil, ErrUnsupported }

// useHostRoot has nothing to move here: watching a host from inside a
// container is a Linux arrangement (see SetHostRoot).
func useHostRoot(dir string) error {
	if dir == "" {
		return nil
	}
	return ErrHostRootUnsupported
}
