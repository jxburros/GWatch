package discovery

import (
	"encoding/binary"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// A /proc/net/arp captured from a home network: two resolved neighbours, one
// address the kernel asked for and never heard back from (flags 0x0, all-zero
// MAC), and a resolved neighbour on another interface outside any range.
const procNetARP = `IP address       HW type     Flags       HW address            Mask     Device
192.168.1.1      0x1         0x2         a4:2b:b0:12:34:56     *        eth0
192.168.1.77     0x1         0x0         00:00:00:00:00:00     *        eth0
192.168.1.40     0x1         0x2         B8:27:EB:AA:BB:CC     *        eth0
172.17.0.2       0x1         0x2         02:42:ac:11:00:02     *        docker0
`

func TestParseProcNetARP(t *testing.T) {
	got := parseProcNetARP(procNetARP)
	if len(got) != 3 {
		t.Fatalf("got %+v; the incomplete entry should be dropped", got)
	}
	if got[1].addr != netip.MustParseAddr("192.168.1.40") || got[1].mac != "b8:27:eb:aa:bb:cc" {
		t.Errorf("second entry: %+v", got[1])
	}
}

func TestParseIPNetTable(t *testing.T) {
	row := func(ip [4]byte, mac []byte, typ uint32) []byte {
		b := make([]byte, ipNetRowSize)
		binary.LittleEndian.PutUint32(b[0:], 7) // interface index
		binary.LittleEndian.PutUint32(b[4:], uint32(len(mac)))
		copy(b[8:16], mac)
		copy(b[16:20], ip[:])
		binary.LittleEndian.PutUint32(b[20:], typ)
		return b
	}
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, 3)
	buf = append(buf, row([4]byte{192, 168, 1, 1}, []byte{0xa4, 0x2b, 0xb0, 0x12, 0x34, 0x56}, ipNetTypeDynamic)...)
	buf = append(buf, row([4]byte{192, 168, 1, 9}, []byte{0, 0, 0, 0, 0, 0}, 2)...) // invalid
	buf = append(buf, row([4]byte{192, 168, 1, 255}, []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, ipNetTypeStatic)...)
	got := parseIPNetTable(buf)
	if len(got) != 2 || got[0].addr.String() != "192.168.1.1" || got[0].mac != "a4:2b:b0:12:34:56" {
		t.Errorf("got %+v", got)
	}
	if parseIPNetTable(buf[:2]) != nil {
		t.Error("a truncated buffer produced entries")
	}
}

func TestVendor(t *testing.T) {
	tests := map[string]string{
		"b8:27:eb:aa:bb:cc": "Raspberry Pi Foundation",
		"DC-A6-32-01-02-03": "Raspberry Pi Trading",
		"00:11:32:00:00:01": "Synology",
		"3c:2a:f4:00:00:01": "Brother Industries",
		"02:42:ac:11:00:02": RandomisedMAC, // locally administered
		"not a mac":         "",
	}
	for mac, want := range tests {
		if got := Vendor(mac); got != want {
			t.Errorf("Vendor(%q) = %q, want %q", mac, got, want)
		}
	}
}

func TestNormalizeMAC(t *testing.T) {
	if got := normalizeMAC("B8-27-EB-AA-BB-CC"); got != "b8:27:eb:aa:bb:cc" {
		t.Errorf("got %q", got)
	}
	for _, bad := range []string{"00:00:00:00:00:00", "b8:27:eb", "zz:27:eb:aa:bb:cc"} {
		if got := normalizeMAC(bad); got != "" {
			t.Errorf("normalizeMAC(%q) = %q", bad, got)
		}
	}
}

// The neighbour table adds what the ping missed and labels what it found:
// a firewalled PC that answered ARP but not ICMP is a responder, with a note
// that a ping check would read it as down; every responder carries its MAC
// and maker; and a neighbour outside the swept range is not a result.
func TestSweepReadsTheNeighbourTable(t *testing.T) {
	lan := newFakeNetwork()
	lan.device("192.168.1.1", "gateway.lan", 2*time.Millisecond, 80, 443)
	lan.open["192.168.1.30"] = []int{3389} // up, but drops pings
	lan.arp = []neighbour{
		{netip.MustParseAddr("192.168.1.1"), "24:a4:3c:01:02:03"},
		{netip.MustParseAddr("192.168.1.30"), "3c:2a:f4:00:00:01"},
		{netip.MustParseAddr("10.0.0.5"), "b8:27:eb:aa:bb:cc"},
	}
	lan.install(t)

	got := sweep(t.Context(), mustRange(t, "192.168.1.1-40"), DefaultPorts, 8, nil)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	gw, pc := got[0], got[1]
	if gw.IP != "192.168.1.1" || gw.MAC != "24:a4:3c:01:02:03" || gw.Vendor != "Ubiquiti" || gw.NoPing {
		t.Errorf("gateway: %+v", gw)
	}
	if !strings.Contains(gw.Note, "Ubiquiti") || gw.Template != "router" {
		t.Errorf("a Ubiquiti box with only a web page is a network device: %+v", gw)
	}
	if pc.IP != "192.168.1.30" || !pc.NoPing || pc.RTTMS != 0 || pc.Vendor != "Brother Industries" {
		t.Errorf("neighbour-only device: %+v", pc)
	}
	if !strings.Contains(pc.Note, "did not answer a ping") || len(pc.OpenPorts) != 1 {
		t.Errorf("the note should warn that a ping check would fail: %+v", pc)
	}
}

func mustRange(t *testing.T, spec string) []netip.Addr {
	t.Helper()
	addrs, err := ParseRanges([]string{spec})
	if err != nil {
		t.Fatal(err)
	}
	return addrs
}

func TestSuggestForUsesTheMaker(t *testing.T) {
	// Ping only: the template is the safe one, the maker names the device.
	s := SuggestFor(nil, "Espressif")
	if s.Template != "ping" || !strings.Contains(s.Note, "smart-home") {
		t.Errorf("Espressif, ping only: %+v", s)
	}
	// A web page on a printer is not a router.
	s = SuggestFor([]int{80}, "Brother Industries")
	if s.Template != "ping" || !strings.Contains(s.Note, "printer") {
		t.Errorf("Brother with a web page: %+v", s)
	}
	// A port that answered still wins: 9100 is a printer whoever made it.
	if s := SuggestFor([]int{9100}, "Raspberry Pi Trading"); s.Template != "tcp-service" {
		t.Errorf("JetDirect: %+v", s)
	}
	// An unknown or uninformative maker changes nothing.
	if s := SuggestFor([]int{22, 80}, "Intel"); s != Suggest([]int{22, 80}) {
		t.Errorf("Intel: %+v", s)
	}
	// Whole words only.
	if vendorKind("ADI Engineering") != "" || vendorKind("SonoSite") != "" || vendorKind("Ring") != kindIoT {
		t.Error("vendor fragments must match whole words")
	}
}

func TestContainerNotes(t *testing.T) {
	if containerKind() == "" {
		if n := containerNotes(nil); n != nil {
			t.Errorf("no container, but notes %v", n)
		}
		return
	}
	if n := containerNotes([]Responder{{IP: "192.168.1.1"}}); len(n) != 1 || !strings.Contains(n[0], "host or macvlan") {
		t.Errorf("in a container with no MACs: %v", n)
	}
	if n := containerNotes([]Responder{{IP: "192.168.1.1", MAC: "24:a4:3c:01:02:03"}}); n != nil {
		t.Errorf("a container that sees MACs is on the LAN: %v", n)
	}
}
