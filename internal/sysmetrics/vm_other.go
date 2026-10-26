//go:build !windows

package sysmetrics

import (
	"context"

	"github.com/jxburros/GWatch/internal/model"
)

// Only Hyper-V is read so far (vm.go), so a guest watched anywhere else is a
// warning on the reading and an empty list to choose from.

func listGuests(context.Context) ([]model.HostVM, []string, error) {
	return nil, nil, errVMUnsupported
}
