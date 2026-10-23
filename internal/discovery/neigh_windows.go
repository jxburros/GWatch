//go:build windows

package discovery

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// GetIpNetTable is the IPv4 neighbour (ARP) table. Its successor,
// GetIpNetTable2, adds IPv6 through a larger row of unions; discovery sweeps
// IPv4 only, so the older call with its fixed 24-byte row is the simpler and
// sufficient one. Neither is wrapped by golang.org/x/sys/windows.
var (
	iphlpapi          = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetIpNetTable = iphlpapi.NewProc("GetIpNetTable")
)

func neighbourTable() []neighbour {
	if procGetIpNetTable.Find() != nil {
		return nil
	}
	var size uint32
	// The first call asks how large the table is. The table can grow between
	// the two calls, so a too-small answer is retried a couple of times.
	for attempt := 0; attempt < 3; attempt++ {
		var buf []byte
		var ptr uintptr
		if size > 0 {
			buf = make([]byte, size)
			ptr = uintptr(unsafe.Pointer(&buf[0]))
		}
		r, _, _ := procGetIpNetTable.Call(ptr, uintptr(unsafe.Pointer(&size)), 0)
		switch windows.Errno(r) {
		case 0:
			if buf == nil {
				return nil
			}
			return parseIPNetTable(buf)
		case windows.ERROR_INSUFFICIENT_BUFFER:
			continue
		default:
			// ERROR_NO_DATA is an empty table; anything else is a table that
			// cannot be read, which costs the sweep only its MAC addresses.
			return nil
		}
	}
	return nil
}

// containerKind is always "" on Windows: GWatch's container image is Linux.
func containerKind() string { return "" }
