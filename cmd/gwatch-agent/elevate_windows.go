package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// isElevated reports whether this process runs with an administrator's full
// token (elevated, in UAC's terms).
func isElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// canElevate: every interactive Windows session can show a UAC prompt. A
// service, or a session over SSH, cannot; neither runs the panel.
func canElevate() bool { return !isElevated() && os.Getenv("SSH_CONNECTION") == "" }

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// shellExecuteInfo is SHELLEXECUTEINFOW (shellapi.h). The fields are in the
// header's order with Go types of the same size, so Go lays the struct out as
// the C compiler does on every Windows target the agent is built for:
//
//	field          amd64/arm64 offset   386 offset
//	cbSize         0   (DWORD)          0
//	fMask          4   (ULONG)          4
//	hwnd           8   (HWND)           8
//	lpVerb         16                   12
//	lpFile         24                   16
//	lpParameters   32                   20
//	lpDirectory    40                   24
//	nShow          48  (int, then 4 bytes of padding on 64-bit)   28
//	hInstApp       56                   32
//	lpIDList       64                   36
//	lpClass        72                   40
//	hkeyClass      80                   44
//	dwHotKey       88  (DWORD, then 4 bytes of padding on 64-bit) 48
//	hIcon/hMonitor 96  (a union of two handles)                   52
//	hProcess       104                  56
//
// for a size of 112 bytes on 64-bit and 60 on 32-bit, which is what cbSize
// must say and what unsafe.Sizeof gives.
type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         windows.HWND
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     windows.Handle
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    windows.Handle
	dwHotKey     uint32
	hIcon        windows.Handle
	hProcess     windows.Handle
}

const (
	seeMaskNoCloseProcess = 0x00000040 // fill in hProcess
	seeMaskNoAsync        = 0x00000100 // finish before returning
	seeMaskFlagNoUI       = 0x00000400 // no error dialogs of the shell's own
	swHide                = 0
	swShowMinNoActive     = 7
)

type windowsElevated struct{ process windows.Handle }

func (p windowsElevated) Wait() error {
	defer windows.CloseHandle(p.process)
	if _, err := windows.WaitForSingleObject(p.process, windows.INFINITE); err != nil {
		return err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(p.process, &code); err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("the administrator's copy of gwatch-agent stopped with status %d", code)
	}
	return nil
}

// startElevated runs exe with args through UAC: Windows shows its own consent
// (or credentials) prompt, and the process starts with an administrator's
// token only if the person agrees. background is for the panel, which keeps
// running: its console is shown minimised, so it can be found on the task bar
// and closed, rather than hidden where only the idle timer would end it.
func startElevated(exe string, args []string, background bool) (elevatedProc, error) {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = windows.EscapeArg(a)
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		return nil, err
	}
	params, err := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	if err != nil {
		return nil, err
	}
	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess | seeMaskNoAsync | seeMaskFlagNoUI,
		lpVerb:       verb,
		lpFile:       file,
		lpParameters: params,
		nShow:        swHide,
	}
	if background {
		info.nShow = swShowMinNoActive
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	if r, _, err := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info))); r == 0 {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return nil, errElevationDeclined
		}
		return nil, fmt.Errorf("could not ask for administrator rights: %w", err)
	}
	if info.hProcess == 0 {
		return nil, errors.New("could not ask for administrator rights: no process was started")
	}
	return windowsElevated{process: info.hProcess}, nil
}

// giveToDirOwner has nothing to do on Windows: the page inherits the access
// list of the folder it is written in, which is the person's own temporary
// folder, so they can read it whoever wrote it.
func giveToDirOwner(string, os.FileInfo) error { return nil }
