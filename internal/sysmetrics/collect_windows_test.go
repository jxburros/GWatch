//go:build windows

package sysmetrics

import (
	"encoding/binary"
	"golang.org/x/sys/windows"
	"testing"
	"unsafe"
)

func TestWindowsFiletimeAndStringTable(t *testing.T) {
	for _, tc := range []struct {
		value uint64
		want  float64
	}{{0, 0}, {10000000, 1}, {uint64(1) << 32, 429.4967296}} {
		got := filetimeSeconds(windows.Filetime{LowDateTime: uint32(tc.value), HighDateTime: uint32(tc.value >> 32)})
		if got != tc.want {
			t.Fatalf("%d: %g", tc.value, got)
		}
	}
	got := splitNullTerminated([]uint16{'C', ':', '\\', 0, 'D', ':', '\\', 0, 0})
	if len(got) != 2 || got[0] != `C:\` || got[1] != `D:\` {
		t.Fatalf("%q", got)
	}
	if len(splitNullTerminated(nil)) != 0 {
		t.Fatal("empty string table")
	}
}
func TestWindowsMemoryBounds(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		raw                       memoryStatusEx
		used, swapTotal, swapUsed uint64
	}{
		{"normal", memoryStatusEx{TotalPhys: 100, AvailPhys: 30, TotalPageFile: 160, AvailPageFile: 70}, 70, 60, 20},
		{"no swap", memoryStatusEx{TotalPhys: 100, AvailPhys: 30, TotalPageFile: 100}, 70, 0, 0},
		{"available exceeds total", memoryStatusEx{TotalPhys: 100, AvailPhys: 200}, 0, 0, 0},
		{"page availability exceeds limit", memoryStatusEx{TotalPhys: 100, AvailPhys: 30, TotalPageFile: 160, AvailPageFile: 999}, 70, 60, 0},
		{"swap clamped", memoryStatusEx{TotalPhys: 100, AvailPhys: 100, TotalPageFile: 160}, 0, 60, 60},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := memoryFromWindows(tc.raw)
			if got.UsedBytes != tc.used || got.SwapTotalBytes != tc.swapTotal || got.SwapUsedBytes != tc.swapUsed {
				t.Fatalf("%+v", got)
			}
		})
	}
}
func TestWindowsInterfaceTableParsing(t *testing.T) {
	rows := []mibIfRow{{Index: 7, Speed: 1000000000, OperStatus: ifOperStatusUp, InOctets: 120, OutOctets: 240, InErrors: 2, OutDiscards: 3}, {Index: 8, Type: windows.IF_TYPE_SOFTWARE_LOOPBACK}, {Index: 9, DescrLen: 8}}
	copy(rows[2].Descr[:], "Fallback")
	size := int(unsafe.Sizeof(mibIfRow{}))
	offset := int(unsafe.Offsetof(struct {
		N   uint32
		Row mibIfRow
	}{}.Row))
	buf := make([]byte, offset+len(rows)*size)
	binary.LittleEndian.PutUint32(buf, uint32(len(rows)))
	for i := range rows {
		*(*mibIfRow)(unsafe.Pointer(&buf[offset+i*size])) = rows[i]
	}
	got, err := parseWindowsInterfaces(buf, map[uint32]string{7: "Ethernet"}, map[string][]string{"Ethernet": {"192.0.2.1"}})
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].name != "Ethernet" || !got[0].up || got[0].rxBytes != 120 || got[0].txBytes != 240 || got[0].speedMbit != 1000 || got[0].rxErrors != 2 || got[0].txDropped != 3 || got[1].name != "Fallback" {
		t.Fatalf("%+v", got)
	}
	for _, bad := range [][]byte{nil, buf[:3], buf[:len(buf)-1]} {
		if _, err := parseWindowsInterfaces(bad, nil, nil); err == nil {
			t.Fatal("truncated table accepted")
		}
	}
}
