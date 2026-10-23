// The agent in the Windows notification area: `gwatch-agent tray` (issue #53).
//
// A small icon by the clock that says whether the agent is reporting, opens
// the control panel, and nothing else. It is deliberately a thin shim over
// what already exists (entry 6 of docs/AGENT-DECISIONS.md): what it shows comes
// from the status file the service writes after every report (status.go), and
// everything it can do is done by starting `gwatch-agent ui`, the panel. It
// holds no settings, sends nothing anywhere and listens on nothing.
//
// It is pure Go, with no cgo and no library: a dozen calls into user32 and
// shell32 through golang.org/x/sys/windows, which is why it exists on Windows
// and not yet on macOS or Linux (tray_other.go says why). The structures those
// calls take are declared below with their layouts written out, because a
// field in the wrong place is not a compile error but a crash, or worse, an
// icon that silently never appears. Every one of them is laid out by Go exactly
// as by the C compiler, on 64-bit x86 and arm64 alike (Windows uses the same
// LLP64 model and natural alignment on both), so one declaration serves both.
//
// It runs as the person signed in, unelevated. It cannot read the agent's
// configuration and does not need to; the status file is given an ACL that lets
// signed-in users read it (status_share_windows.go), and the service's own
// state comes from the service manager, which any user may ask. It starts
// only if someone asks for it: the setup program offers a checkbox that runs
// `gwatch-agent tray start-at-login`, which adds a value under this user's
// HKCU\...\Run key, and `tray stop-at-login` (also run on uninstall) removes it.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW       = user32.NewProc("RegisterClassExW")
	procCreateWindowExW        = user32.NewProc("CreateWindowExW")
	procDefWindowProcW         = user32.NewProc("DefWindowProcW")
	procDestroyWindow          = user32.NewProc("DestroyWindow")
	procGetMessageW            = user32.NewProc("GetMessageW")
	procTranslateMessage       = user32.NewProc("TranslateMessage")
	procDispatchMessageW       = user32.NewProc("DispatchMessageW")
	procPostQuitMessage        = user32.NewProc("PostQuitMessage")
	procPostMessageW           = user32.NewProc("PostMessageW")
	procSetTimer               = user32.NewProc("SetTimer")
	procCreatePopupMenu        = user32.NewProc("CreatePopupMenu")
	procAppendMenuW            = user32.NewProc("AppendMenuW")
	procTrackPopupMenu         = user32.NewProc("TrackPopupMenu")
	procDestroyMenu            = user32.NewProc("DestroyMenu")
	procGetCursorPos           = user32.NewProc("GetCursorPos")
	procSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	procLoadImageW             = user32.NewProc("LoadImageW")
	procLoadIconW              = user32.NewProc("LoadIconW")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procRegisterWindowMessageW = user32.NewProc("RegisterWindowMessageW")
	procShowWindow             = user32.NewProc("ShowWindow")
	procShellNotifyIconW       = shell32.NewProc("Shell_NotifyIconW")
	procGetModuleHandleW       = kernel32.NewProc("GetModuleHandleW")
	procGetConsoleWindow       = kernel32.NewProc("GetConsoleWindow")
	procGetConsoleProcessList  = kernel32.NewProc("GetConsoleProcessList")
	procFreeConsole            = kernel32.NewProc("FreeConsole")
)

// Constants from winuser.h and shellapi.h.
const (
	wmDestroy      = 0x0002
	wmClose        = 0x0010
	wmNull         = 0x0000
	wmTimer        = 0x0113
	wmLButtonUp    = 0x0202
	wmRButtonUp    = 0x0205
	wmApp          = 0x8000
	wmTrayCallback = wmApp + 1

	nimAdd     = 0
	nimModify  = 1
	nimDelete  = 2
	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4

	mfString       = 0x0
	mfSeparator    = 0x800
	tpmRightButton = 0x0002
	tpmNoNotify    = 0x0080
	tpmReturnCmd   = 0x0100
	tpmBottomAlign = 0x0020

	imageIcon      = 1
	lrDefaultColor = 0
	smCxSmIcon     = 49
	smCySmIcon     = 50
	idiWarning     = 32515
	idiError       = 32513
	idiApplication = 32512

	swHideWindow = 0

	// hwndMessage is HWND_MESSAGE, (HWND)-3: the parent that makes a window
	// message-only, never shown and never enumerated.
	hwndMessage = ^uintptr(2)

	// The agent's own icon is the executable's only RT_GROUP_ICON, number 1
	// (cmd/gwatch-rsrc).
	appIconID = 1

	trayIconID    = 1
	refreshTimer  = 1
	trayRefresh   = 30 * time.Second
	menuOpenPanel = 1
	menuStatus    = 2
	menuQuit      = 3

	runKeyPath  = `Software\Microsoft\Windows\CurrentVersion\Run`
	runKeyValue = "GWatch Agent tray"
)

// wndClassEx is WNDCLASSEXW.
//
//	field          offset on amd64/arm64
//	cbSize         0   UINT
//	style          4   UINT
//	lpfnWndProc    8   WNDPROC (a pointer)
//	cbClsExtra     16  int
//	cbWndExtra     20  int
//	hInstance      24
//	hIcon          32
//	hCursor        40
//	hbrBackground  48
//	lpszMenuName   56
//	lpszClassName  64
//	hIconSm        72
//
// 80 bytes in all, which is what cbSize must say.
type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     windows.Handle
	hIcon         windows.Handle
	hCursor       windows.Handle
	hbrBackground windows.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       windows.Handle
}

// point is POINT: two LONGs, which are 32 bits on Windows whatever the
// architecture.
type point struct{ x, y int32 }

// msg is MSG.
//
//	hwnd      0
//	message   8   UINT, then 4 bytes of padding so that
//	wParam    16  lands on an 8-byte boundary
//	lParam    24
//	time      32  DWORD
//	pt        36  POINT (two int32s; needs only 4-byte alignment)
//	lPrivate  44  DWORD
//
// 48 bytes. Go inserts the same padding after message, for the same reason.
type msg struct {
	hwnd     windows.HWND
	message  uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	pt       point
	lPrivate uint32
}

// notifyIconData is NOTIFYICONDATAW in its full, Vista-and-later form; cbSize
// set to its size tells the shell which form it has been given.
//
//	cbSize            0    DWORD, then 4 bytes of padding
//	hWnd              8
//	uID               16   UINT
//	uFlags            20   UINT
//	uCallbackMessage  24   UINT, then 4 bytes of padding
//	hIcon             32
//	szTip             40   WCHAR[128] (256 bytes)
//	dwState           296  DWORD
//	dwStateMask       300  DWORD
//	szInfo            304  WCHAR[256] (512 bytes)
//	uTimeout/uVersion 816  UINT (a union of two UINTs)
//	szInfoTitle       820  WCHAR[64] (128 bytes)
//	dwInfoFlags       948  DWORD
//	guidItem          952  GUID (16 bytes, 4-byte alignment)
//	hBalloonIcon      968
//
// 976 bytes in all. The padding Go inserts is exactly the padding above:
// before each handle, to bring it to an 8-byte boundary, and none anywhere
// else, since every other field is 2- or 4-byte aligned already.
type notifyIconData struct {
	cbSize           uint32
	hWnd             windows.HWND
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            windows.Handle
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         windows.GUID
	hBalloonIcon     windows.Handle
}

// tray is the running icon. Everything here is touched only on the thread
// that runs the message loop.
type tray struct {
	hwnd           windows.HWND
	taskbarCreated uint32
	iconOK         windows.Handle
	iconWarn       windows.Handle
	iconBad        windows.Handle
	last           trayState
}

// theTray is the one tray of this process, for the window procedure, which
// Windows calls with nothing but the window.
var theTray *tray

// wndProcCallback is kept in a variable for the life of the process: a
// callback the garbage collector could free would be called after it had
// been.
var wndProcCallback = windows.NewCallback(trayWndProc)

func runTray(cfg config, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "start-at-login":
			if err := setTrayAtLogin(true); err != nil {
				return fmt.Errorf("could not set the icon to start when you sign in: %w", err)
			}
		case "stop-at-login":
			// Also run by the uninstaller, and safe to run twice.
			if err := setTrayAtLogin(false); err != nil {
				return fmt.Errorf("could not stop the icon starting when you sign in: %w", err)
			}
			return nil
		default:
			return fmt.Errorf("unknown tray option %q (start-at-login or stop-at-login)", args[0])
		}
	}

	// One icon per person: a second copy (started by hand while the login
	// one runs) just leaves.
	name, _ := windows.UTF16PtrFromString(`Local\GWatchAgentTray`)
	mutex, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(mutex)

	detachConsole()

	// A window belongs to the thread that made it, and its messages are
	// delivered there; the message loop has to stay on that one thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	t := &tray{}
	theTray = t
	if err := t.create(); err != nil {
		return err
	}
	t.refresh()
	procSetTimer.Call(uintptr(t.hwnd), refreshTimer, uintptr(trayRefresh/time.Millisecond), 0)

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 { // 0 is WM_QUIT, -1 an error; both end the loop
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// detachConsole lets go of the console window Windows gives a console program
// started from the Run key, so that signing in does not leave a black window
// on the desktop. A console someone started the tray from by hand is left
// alone, only detached from.
func detachConsole() {
	var pids [4]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n == 1 {
		if w, _, _ := procGetConsoleWindow.Call(); w != 0 {
			procShowWindow.Call(w, swHideWindow)
		}
	}
	procFreeConsole.Call()
}

func (t *tray) create() error {
	inst, _, _ := procGetModuleHandleW.Call(0)
	className, _ := windows.UTF16PtrFromString("GWatchAgentTray")
	wc := wndClassEx{lpfnWndProc: wndProcCallback, hInstance: windows.Handle(inst), lpszClassName: className}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("register the tray's window class: %w", err)
	}
	title, _ := windows.UTF16PtrFromString("GWatch Agent")
	hwnd, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		0, 0, 0, 0, 0, hwndMessage, 0, inst, 0)
	if hwnd == 0 {
		return fmt.Errorf("create the tray's window: %w", err)
	}
	t.hwnd = windows.HWND(hwnd)

	// Explorer sends this to every top-level window when it restarts (after
	// a crash, or an update); the icon has to be added again then. A
	// message-only window does not get broadcasts, so the icon's own
	// callback also re-adds it if a modify ever fails.
	tb, _ := windows.UTF16PtrFromString("TaskbarCreated")
	r, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(tb)))
	t.taskbarCreated = uint32(r)

	cx, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
	cy, _, _ := procGetSystemMetrics.Call(smCySmIcon)
	ic, _, _ := procLoadImageW.Call(inst, appIconID, imageIcon, cx, cy, lrDefaultColor)
	if ic == 0 {
		ic, _, _ = procLoadIconW.Call(0, idiApplication)
	}
	t.iconOK = windows.Handle(ic)
	w, _, _ := procLoadIconW.Call(0, idiWarning)
	t.iconWarn = windows.Handle(w)
	b, _, _ := procLoadIconW.Call(0, idiError)
	t.iconBad = windows.Handle(b)

	t.last = readTrayState()
	return t.notify(nimAdd)
}

// notify adds, updates or removes the icon.
func (t *tray) notify(op uintptr) error {
	nid := notifyIconData{hWnd: t.hwnd, uID: trayIconID, uFlags: nifMessage | nifIcon | nifTip, uCallbackMessage: wmTrayCallback}
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	switch t.last.level {
	case levelOK:
		nid.hIcon = t.iconOK
	case levelWarn:
		nid.hIcon = t.iconWarn
	default:
		nid.hIcon = t.iconBad
	}
	tip := windows.StringToUTF16(t.last.tip)
	if len(tip) > len(nid.szTip) {
		tip = append(tip[:len(nid.szTip)-1], 0)
	}
	copy(nid.szTip[:], tip)
	if r, _, err := procShellNotifyIconW.Call(op, uintptr(unsafe.Pointer(&nid))); r == 0 {
		return fmt.Errorf("the notification area refused the icon: %w", err)
	}
	return nil
}

func (t *tray) refresh() {
	t.last = readTrayState()
	if t.notify(nimModify) != nil {
		_ = t.notify(nimAdd)
	}
}

func trayWndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	t := theTray
	switch {
	case t == nil:
	case message == wmTrayCallback:
		// With the icon in its original (version 0) mode, which is all this
		// needs, lParam is the mouse message itself.
		switch lParam & 0xFFFF {
		case wmLButtonUp:
			openPanelFromTray()
		case wmRButtonUp:
			t.menu()
		}
		return 0
	case message == wmTimer:
		t.refresh()
		return 0
	case message == uintptr(t.taskbarCreated) && t.taskbarCreated != 0:
		_ = t.notify(nimAdd)
		return 0
	case message == wmClose:
		procDestroyWindow.Call(hwnd)
		return 0
	case message == wmDestroy:
		_ = t.notify(nimDelete)
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return r
}

func (t *tray) menu() {
	m, _, _ := procCreatePopupMenu.Call()
	if m == 0 {
		return
	}
	defer procDestroyMenu.Call(m)
	add := func(id uintptr, text string) {
		p, _ := windows.UTF16PtrFromString(text)
		procAppendMenuW.Call(m, mfString, id, uintptr(unsafe.Pointer(p)))
	}
	add(menuOpenPanel, "Open control panel")
	add(menuStatus, "Agent status")
	procAppendMenuW.Call(m, mfSeparator, 0, 0)
	add(menuQuit, "Quit")

	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	// Without this the menu stays open after a click elsewhere; and the
	// WM_NULL afterwards is the documented companion fix for the menu
	// closing at once the second time it is opened (KB135788).
	procSetForegroundWindow.Call(uintptr(t.hwnd))
	cmd, _, _ := procTrackPopupMenu.Call(m, tpmRightButton|tpmBottomAlign|tpmReturnCmd|tpmNoNotify,
		uintptr(pt.x), uintptr(pt.y), 0, uintptr(t.hwnd), 0)
	procPostMessageW.Call(uintptr(t.hwnd), wmNull, 0, 0)

	switch cmd {
	case menuOpenPanel:
		openPanelFromTray()
	case menuStatus:
		t.refresh()
		text, _ := windows.UTF16PtrFromString(t.last.detail)
		caption, _ := windows.UTF16PtrFromString("GWatch Agent")
		windows.MessageBox(0, text, caption, windows.MB_OK|windows.MB_ICONINFORMATION)
	case menuQuit:
		procDestroyWindow.Call(uintptr(t.hwnd))
	}
}

// openPanelFromTray starts `gwatch-agent ui`. Its console is hidden: the
// panel is the browser page, and the copy started here only exists to ask for
// an administrator's rights (UAC) and open that page (elevate.go). If it
// fails, what it said is shown in a message box rather than lost.
func openPanelFromTray() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	go func() {
		cmd := exec.Command(exe, "ui")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := cmd.CombinedOutput()
		if err == nil {
			return
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		text, _ := windows.UTF16PtrFromString("The control panel did not open.\n\n" + strings.TrimSpace(lines[len(lines)-1]))
		caption, _ := windows.UTF16PtrFromString("GWatch Agent")
		windows.MessageBox(0, text, caption, windows.MB_OK|windows.MB_ICONWARNING)
	}()
}

// ---- what the icon says ----

type trayLevel int

const (
	levelOK trayLevel = iota
	levelWarn
	levelBad
)

type trayState struct {
	level  trayLevel
	tip    string // at most 127 characters are shown
	detail string // the "Agent status" box
}

// serviceStateForTray asks the service manager about the agent's service with
// the least access there is, which any signed-in user is allowed.
func serviceStateForTray() string {
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return "unknown"
	}
	defer windows.CloseServiceHandle(m)
	name, _ := windows.UTF16PtrFromString(serviceName)
	s, err := windows.OpenService(m, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
			return "not installed"
		}
		return "unknown"
	}
	defer windows.CloseServiceHandle(s)
	var st windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(s, &st); err != nil {
		return "unknown"
	}
	switch st.CurrentState {
	case windows.SERVICE_RUNNING:
		return "running"
	case windows.SERVICE_STOPPED:
		return "stopped"
	case windows.SERVICE_START_PENDING:
		return "starting"
	case windows.SERVICE_STOP_PENDING:
		return "stopping"
	}
	return "unknown"
}

func readTrayState() trayState {
	svc := serviceStateForTray()
	st, err := readAgentStatus()
	return describeTrayState(svc, st, err, time.Now())
}

// describeTrayState turns the service's state and the status file into what
// the icon shows. It is the same judgement the panel's status pill makes.
func describeTrayState(svc string, st agentStatus, statusErr error, now time.Time) trayState {
	detail := []string{"Service: " + svc}
	if statusErr == nil {
		if st.Server != "" {
			detail = append(detail, "Reports to: "+st.Server)
		}
		if st.LastReport != nil {
			r := "Last reading: " + trayAgo(now.Sub(*st.LastReport)) + " ago"
			if !st.ReportOK {
				r += " (failed)"
			}
			detail = append(detail, r)
		} else {
			detail = append(detail, "Last reading: none yet")
		}
		if st.ReportError != "" {
			detail = append(detail, "Last error: "+st.ReportError)
		}
		if st.Held > 0 {
			detail = append(detail, fmt.Sprintf("Readings waiting to be sent: %d", st.Held))
		}
		detail = append(detail, "Agent version: "+st.Version)
	} else {
		detail = append(detail, "(The agent's status file could not be read: "+filepath.Base(statusFile())+".)")
	}
	detail = append(detail, "", "Open the control panel for settings, the log and more.")

	out := trayState{detail: strings.Join(detail, "\n")}
	switch {
	case svc == "not installed":
		out.level, out.tip = levelWarn, "GWatch agent: not installed"
	case svc == "stopped":
		out.level, out.tip = levelBad, "GWatch agent: stopped"
	case statusErr != nil:
		out.level, out.tip = levelOK, "GWatch agent: "+svc
	default:
		interval, _ := time.ParseDuration(st.Interval)
		if interval <= 0 {
			interval = time.Minute
		}
		stale := 3 * interval
		if stale < 3*time.Minute {
			stale = 3 * time.Minute
		}
		switch {
		case now.Sub(st.UpdatedAt) > stale:
			out.level, out.tip = levelWarn, "GWatch agent: no news for "+trayAgo(now.Sub(st.UpdatedAt))
		case st.LastReport != nil && !st.ReportOK:
			out.level, out.tip = levelBad, "GWatch agent: cannot reach GWatch"
		case st.LastReport != nil:
			out.level, out.tip = levelOK, "GWatch agent: reporting, last reading "+trayAgo(now.Sub(*st.LastReport))+" ago"
		default:
			out.level, out.tip = levelOK, "GWatch agent: starting"
		}
	}
	return out
}

func trayAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < 90*time.Minute:
		return fmt.Sprintf("%d min", int(d.Minutes()+0.5))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", int(d.Hours()+0.5))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24+0.5))
}

// setTrayAtLogin adds or removes the value under this user's Run key that
// starts the icon when they sign in.
func setTrayAtLogin(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(runKeyValue); err != nil && !errors.Is(err, registry.ErrNotExist) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(runKeyValue, windows.EscapeArg(exe)+" tray")
}
