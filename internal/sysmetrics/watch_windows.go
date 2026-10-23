//go:build windows

package sysmetrics

import (
	"context"
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/jxburros/GWatch/internal/model"
)

// Services on Windows are read from the Service Control Manager, and
// processes from a Toolhelp snapshot, both straight through
// golang.org/x/sys/windows: no PowerShell, no WMI, nothing to start.
//
// The Service Control Manager is opened asking only to connect and to list
// services. golang.org/x/sys/windows/svc/mgr's Connect asks for full control,
// which only an administrator gets; listing and reading state is something any
// account may do, so the agent can watch services whatever account it runs
// under.

// enumServices lists every Win32 service with its state and display name.
func enumServices() ([]model.HostService, error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT|windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, err
	}
	defer windows.CloseServiceHandle(h)

	var needed, returned, resume uint32
	var buf []byte
	for {
		var p *byte
		if len(buf) > 0 {
			p = &buf[0]
		}
		err = windows.EnumServicesStatusEx(h, windows.SC_ENUM_PROCESS_INFO, windows.SERVICE_WIN32, windows.SERVICE_STATE_ALL,
			p, uint32(len(buf)), &needed, &returned, &resume, nil)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.ERROR_MORE_DATA) || needed <= uint32(len(buf)) {
			return nil, err
		}
		// The whole list in one go: start again from the beginning with a
		// buffer big enough for all of it.
		buf, resume = make([]byte, needed), 0
	}
	if returned == 0 {
		return nil, nil
	}
	entries := unsafe.Slice((*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[0])), int(returned))
	out := make([]model.HostService, 0, len(entries))
	for _, e := range entries {
		state := e.ServiceStatusProcess.CurrentState
		out = append(out, model.HostService{
			Name:        windows.UTF16PtrToString(e.ServiceName),
			Description: windows.UTF16PtrToString(e.DisplayName),
			Running:     state == windows.SERVICE_RUNNING,
			State:       windowsServiceState(state),
		})
	}
	return out, nil
}

func listServices(context.Context) ([]model.HostService, error) { return enumServices() }

// serviceStates reads the whole list and picks the watched ones out of it:
// one call however many are watched, and a name can be the service name or
// the display name people see in the Services console.
func serviceStates(_ context.Context, names []string) ([]model.HostService, error) {
	all, err := enumServices()
	if err != nil {
		return nil, err
	}
	return pickServices(all, names, windowsServiceMatch), nil
}

// processNames walks a Toolhelp snapshot of every process. The names carry
// their ".exe", which countProcesses ignores when matching.
func processNames(context.Context) ([]string, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	var out []string
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID == 0 {
			continue // the System Idle Process, which is not a program
		}
		out = append(out, windows.UTF16ToString(e.ExeFile[:]))
	}
	if !errors.Is(err, syscall.ERROR_NO_MORE_FILES) {
		return nil, err
	}
	return out, nil
}
