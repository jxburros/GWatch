//go:build !linux && !darwin && !windows

package sysmetrics

import (
	"context"
	"errors"

	"github.com/jxburros/GWatch/internal/model"
)

// Nothing reads services or processes on a platform with no collector; a
// reading is refused there before this is reached (ErrUnsupported), and the
// lists say why.

var errWatchUnsupported = errors.New("services and processes cannot be read on this platform")

func serviceStates(context.Context, []string) ([]model.HostService, error) {
	return nil, errWatchUnsupported
}
func listServices(context.Context) ([]model.HostService, error) { return nil, errWatchUnsupported }
func processNames(context.Context) ([]string, error)            { return nil, errWatchUnsupported }
