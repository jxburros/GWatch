//go:build windows

package sysmetrics

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/jxburros/GWatch/internal/model"
)

// Hyper-V guests are read from WMI, through COM, straight from
// golang.org/x/sys/windows: a WbemLocator, a connection to
// root\virtualization\v2, and two queries. That is what the Hyper-V Manager
// itself reads, it needs nothing started, and it keeps the agent's Windows
// readings free of PowerShell, as the rest of them are. Get-VM is used only if
// WMI fails in some other way than "Hyper-V is not installed" or "this
// account may not" — both of which Get-VM would say too, seconds later.
//
// Reading Hyper-V needs an account in the local "Hyper-V Administrators"
// group, or an administrator. The agent's service runs as LocalSystem, which
// may; a panel or a one-off reading run as an ordinary user may not, and
// says so.

var (
	errHyperVMissing = errors.New("Hyper-V is not installed on this machine (its WMI namespace, root\\virtualization\\v2, is missing)")
	errHyperVDenied  = errors.New("this account may not read Hyper-V: it needs to be in the local \"Hyper-V Administrators\" group, or be an administrator")
)

// wmiTimeout bounds each step of a WMI query, and the whole of one.
const wmiTimeout = 10 * time.Second

// listGuests reads every guest on this machine. The processor and memory
// figures come from a second query, and a guest is still worth reporting
// without them, so that one failing is a warning.
func listGuests(ctx context.Context) ([]model.HostVM, []string, error) {
	systems, err := wmiQueryContext(ctx, hyperVNamespace, hyperVSystemsQuery, hyperVSystemsProps)
	if err != nil {
		if errors.Is(err, errHyperVMissing) || errors.Is(err, errHyperVDenied) || ctx.Err() != nil {
			return nil, nil, err
		}
		vms, perr := getVMs(ctx)
		if perr != nil {
			return nil, nil, fmt.Errorf("%v (and Get-VM: %v)", err, perr)
		}
		return vms, nil, nil
	}
	var warnings []string
	summaries, err := wmiQueryContext(ctx, hyperVNamespace, hyperVSummaryQuery, hyperVSummaryProps)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("virtual machines: processor and memory use: %v", err))
	}
	return hyperVGuests(systems, summaries), warnings, nil
}

// getVMs asks PowerShell's Hyper-V module, without a profile or a window.
func getVMs(ctx context.Context) ([]model.HostVM, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", hyperVGetVMCommand)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("%s", firstLine(string(ee.Stderr)))
		}
		return nil, err
	}
	return parseGetVM(string(out))
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\r' || r == '\n' {
			return s[:i]
		}
	}
	return s
}

// wmiQueryContext runs wmiQuery on a thread of its own, so a WMI service that
// does not answer cannot hold the reading past ctx. The query keeps its
// thread until WMI lets go, which wmiTimeout bounds.
func wmiQueryContext(ctx context.Context, namespace, query string, props []string) ([]wmiRow, error) {
	type answer struct {
		rows []wmiRow
		err  error
	}
	done := make(chan answer, 1)
	go func() {
		rows, err := wmiQuery(namespace, query, props)
		done <- answer{rows, err}
	}()
	select {
	case a := <-done:
		return a.rows, a.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

/* ---------------- WMI through COM ---------------- */

var (
	modole32    = windows.NewLazySystemDLL("ole32.dll")
	modoleaut32 = windows.NewLazySystemDLL("oleaut32.dll")

	procCoInitializeEx    = modole32.NewProc("CoInitializeEx")
	procCoUninitialize    = modole32.NewProc("CoUninitialize")
	procCoCreateInstance  = modole32.NewProc("CoCreateInstance")
	procCoSetProxyBlanket = modole32.NewProc("CoSetProxyBlanket")
	procSysAllocString    = modoleaut32.NewProc("SysAllocString")
	procSysFreeString     = modoleaut32.NewProc("SysFreeString")
	procVariantClear      = modoleaut32.NewProc("VariantClear")

	clsidWbemLocator = windows.GUID{Data1: 0x4590F811, Data2: 0x1D3A, Data3: 0x11D0, Data4: [8]byte{0x89, 0x1F, 0x00, 0xAA, 0x00, 0x4B, 0x2E, 0x24}}
	iidIWbemLocator  = windows.GUID{Data1: 0xDC12A687, Data2: 0x737F, Data3: 0x11CF, Data4: [8]byte{0x88, 0x4D, 0x00, 0xAA, 0x00, 0x4B, 0x2E, 0x24}}
)

const (
	coinitMultithreaded = 0x0
	clsctxInprocServer  = 0x1

	rpcCAuthnWinNT          = 10
	rpcCAuthzNone           = 0
	rpcCAuthnLevelCall      = 3
	rpcCImpLevelImpersonate = 3
	eoacNone                = 0

	wbemFlagReturnImmediately = 0x10
	wbemFlagForwardOnly       = 0x20

	sFalse            = 0x00000001
	wbemSTimedOut     = 0x00040004
	rpcEChangedMode   = 0x80010106
	eAccessDenied     = 0x80070005
	wbemEAccessDenied = 0x80041003
	wbemEInvalidNS    = 0x8004100E

	// The vtable slots used, counted from IUnknown's three.
	slotRelease       = 2
	slotConnectServer = 3  // IWbemLocator
	slotExecQuery     = 20 // IWbemServices
	slotNext          = 4  // IEnumWbemClassObject
	slotGet           = 4  // IWbemClassObject
)

// comObject is a COM interface pointer: an object whose first word is its
// vtable. Only COM allocates these; Go only ever holds pointers to them.
type comObject struct {
	vtbl *[32]uintptr
}

func (o *comObject) release() {
	if o != nil {
		syscall.SyscallN(o.vtbl[slotRelease], uintptr(unsafe.Pointer(o)))
	}
}

// variant is a VARIANT: a type tag, three reserved words, and a union two
// pointers wide (24 bytes on 64-bit Windows, 16 on 32-bit).
type variant struct {
	vt  uint16
	_   [3]uint16
	val [2]uintptr
}

const (
	vtEmpty = 0
	vtNull  = 1
	vtI2    = 2
	vtI4    = 3
	vtR4    = 4
	vtR8    = 5
	vtBSTR  = 8
	vtBool  = 11
	vtI1    = 16
	vtUI1   = 17
	vtUI2   = 18
	vtUI4   = 19
	vtI8    = 20
	vtUI8   = 21
)

// value reads the variant as one of the types a wmiRow holds.
func (v *variant) value() any {
	p := unsafe.Pointer(&v.val)
	switch v.vt {
	case vtBSTR:
		s := *(**uint16)(p)
		if s == nil {
			return ""
		}
		return windows.UTF16PtrToString(s)
	case vtI1:
		return int64(*(*int8)(p))
	case vtUI1:
		return int64(*(*uint8)(p))
	case vtI2:
		return int64(*(*int16)(p))
	case vtUI2:
		return int64(*(*uint16)(p))
	case vtI4:
		return int64(*(*int32)(p))
	case vtUI4:
		return int64(*(*uint32)(p))
	case vtI8, vtUI8:
		return *(*int64)(p)
	case vtR4:
		return float64(*(*float32)(p))
	case vtR8:
		return *(*float64)(p)
	case vtBool:
		return *(*int16)(p) != 0
	}
	return nil // VT_EMPTY, VT_NULL and anything a query here never returns
}

type hresult uintptr

func (h hresult) failed() bool { return int32(uint32(h)) < 0 }

func (h hresult) err(step string) error {
	switch uint32(h) {
	case wbemEInvalidNS:
		return errHyperVMissing
	case eAccessDenied, wbemEAccessDenied:
		return errHyperVDenied
	}
	return fmt.Errorf("WMI %s failed (0x%08X)", step, uint32(h))
}

// bstr makes a BSTR, which the caller frees with freeBSTR.
func bstr(s string) (uintptr, error) {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return 0, err
	}
	b, _, _ := procSysAllocString.Call(uintptr(unsafe.Pointer(p)))
	if b == 0 {
		return 0, errors.New("out of memory for a BSTR")
	}
	return b, nil
}

func freeBSTR(b uintptr) { procSysFreeString.Call(b) }

// wmiQuery runs one WQL query in a namespace and reads props from every row.
// COM is used from a thread locked to this goroutine for the length of the
// call, joined to the multithreaded apartment and left again afterwards.
func wmiQuery(namespace, query string, props []string) ([]wmiRow, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded)
	switch uint32(hr) {
	case 0, sFalse:
		defer procCoUninitialize.Call()
	case rpcEChangedMode:
		// This thread is already in an apartment of another kind, which
		// serves as well; it is not ours to leave.
	default:
		return nil, hresult(hr).err("CoInitializeEx")
	}

	var locator *comObject
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidWbemLocator)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIWbemLocator)), uintptr(unsafe.Pointer(&locator)))
	if hresult(hr).failed() {
		return nil, hresult(hr).err("CoCreateInstance(WbemLocator)")
	}
	defer locator.release()

	ns, err := bstr(namespace)
	if err != nil {
		return nil, err
	}
	defer freeBSTR(ns)
	var services *comObject
	// ConnectServer(namespace, user, password, locale, flags, authority,
	// context, &services): the local machine, as the caller.
	hr, _, _ = syscall.SyscallN(locator.vtbl[slotConnectServer], uintptr(unsafe.Pointer(locator)),
		ns, 0, 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&services)))
	if hresult(hr).failed() {
		return nil, hresult(hr).err("ConnectServer")
	}
	defer services.release()

	hr, _, _ = procCoSetProxyBlanket.Call(uintptr(unsafe.Pointer(services)),
		rpcCAuthnWinNT, rpcCAuthzNone, 0, rpcCAuthnLevelCall, rpcCImpLevelImpersonate, 0, eoacNone)
	if hresult(hr).failed() {
		return nil, hresult(hr).err("CoSetProxyBlanket")
	}

	lang, err := bstr("WQL")
	if err != nil {
		return nil, err
	}
	defer freeBSTR(lang)
	q, err := bstr(query)
	if err != nil {
		return nil, err
	}
	defer freeBSTR(q)
	var enum *comObject
	hr, _, _ = syscall.SyscallN(services.vtbl[slotExecQuery], uintptr(unsafe.Pointer(services)),
		lang, q, wbemFlagForwardOnly|wbemFlagReturnImmediately, 0, uintptr(unsafe.Pointer(&enum)))
	if hresult(hr).failed() {
		return nil, hresult(hr).err("ExecQuery")
	}
	defer enum.release()

	names := make([]*uint16, len(props))
	for i, p := range props {
		if names[i], err = windows.UTF16PtrFromString(p); err != nil {
			return nil, err
		}
	}

	var rows []wmiRow
	deadline := time.Now().Add(wmiTimeout)
	for {
		if time.Now().After(deadline) {
			return nil, errors.New("WMI took too long to answer")
		}
		var obj *comObject
		var got uint32
		hr, _, _ = syscall.SyscallN(enum.vtbl[slotNext], uintptr(unsafe.Pointer(enum)),
			uintptr(wmiTimeout/time.Millisecond), 1, uintptr(unsafe.Pointer(&obj)), uintptr(unsafe.Pointer(&got)))
		if hresult(hr).failed() {
			return nil, hresult(hr).err("Next")
		}
		if got == 0 || obj == nil {
			if uint32(hr) == wbemSTimedOut {
				return nil, errors.New("WMI took too long to answer")
			}
			break // WBEM_S_FALSE: no more rows
		}
		row := make(wmiRow, len(props))
		for i, p := range props {
			var v variant
			// Get(name, flags, &value, &type, &flavor)
			hr, _, _ = syscall.SyscallN(obj.vtbl[slotGet], uintptr(unsafe.Pointer(obj)),
				uintptr(unsafe.Pointer(names[i])), 0, uintptr(unsafe.Pointer(&v)), 0, 0)
			if !hresult(hr).failed() {
				row[p] = v.value()
			}
			procVariantClear.Call(uintptr(unsafe.Pointer(&v)))
		}
		obj.release()
		rows = append(rows, row)
	}
	return rows, nil
}
