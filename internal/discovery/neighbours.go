package discovery

import (
	"encoding/binary"
	"net/netip"
	"strconv"
	"strings"
)

// The neighbour table is the operating system's own record of which
// addresses on the local network answered an ARP request, and with which
// hardware address. A sweep fills it as a side effect: pinging an address on
// the same network means asking for its MAC address first, and a device
// answers ARP whether or not its firewall lets it answer the ping that
// follows. Reading the table after the sweep therefore finds the firewalled
// PC and the phone that ignores ICMP, and gives every responder a MAC
// address — and through it the maker of its network card — at no cost.
//
// Only IPv4 (ARP) is read. Discovery sweeps IPv4 ranges only, and the IPv6
// neighbour table (NDP) is a later step for when it sweeps IPv6.

// neighbour is one resolved entry of the table.
type neighbour struct {
	addr netip.Addr
	mac  string // normalised, lower-case, colon-separated
}

// readNeighbours returns the resolved IPv4 neighbours this machine knows.
// It is a variable so the tests can stand in a table of their own; the
// platform's own reader is neighbourTable (neigh_*.go).
var readNeighbours = neighbourTable

// arpComplete is the ATF_COM flag in /proc/net/arp: the entry was resolved.
// An entry without it is an address the kernel asked for and got no answer
// from, and its hardware address is all zeros.
const arpComplete = 0x2

// parseProcNetARP reads Linux's /proc/net/arp:
//
//	IP address       HW type     Flags       HW address            Mask     Device
//	192.168.1.1      0x1         0x2         a4:2b:b0:12:34:56     *        eth0
//	192.168.1.77     0x1         0x0         00:00:00:00:00:00     *        eth0
func parseProcNetARP(text string) []neighbour {
	var out []neighbour
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		addr, err := netip.ParseAddr(fields[0])
		if err != nil || !addr.Is4() {
			continue // the header line, or something that is not an address
		}
		flags, err := strconv.ParseUint(strings.TrimPrefix(fields[2], "0x"), 16, 32)
		if err != nil || flags&arpComplete == 0 {
			continue
		}
		mac := normalizeMAC(fields[3])
		if mac == "" {
			continue
		}
		out = append(out, neighbour{addr: addr, mac: mac})
	}
	return out
}

// Windows' MIB_IPNETROW, as GetIpNetTable lays it out: index, length of the
// hardware address, eight bytes of hardware address, the IPv4 address in
// network byte order, and the entry's type. The table starts with a count.
const (
	ipNetRowSize     = 24
	ipNetTypeDynamic = 3
	ipNetTypeStatic  = 4
)

// parseIPNetTable reads the MIB_IPNETTABLE buffer GetIpNetTable fills. It is
// a pure function over the bytes, so it is tested on every platform even
// though only Windows produces them.
func parseIPNetTable(buf []byte) []neighbour {
	if len(buf) < 4 {
		return nil
	}
	n := int(binary.LittleEndian.Uint32(buf[:4]))
	var out []neighbour
	for i := 0; i < n; i++ {
		off := 4 + i*ipNetRowSize
		if off+ipNetRowSize > len(buf) {
			break
		}
		row := buf[off : off+ipNetRowSize]
		physLen := int(binary.LittleEndian.Uint32(row[4:8]))
		typ := binary.LittleEndian.Uint32(row[20:24])
		if physLen != 6 || (typ != ipNetTypeDynamic && typ != ipNetTypeStatic) {
			continue
		}
		addr := netip.AddrFrom4([4]byte{row[16], row[17], row[18], row[19]})
		var hw strings.Builder
		for j := 0; j < 6; j++ {
			if j > 0 {
				hw.WriteByte(':')
			}
			hw.WriteString(strconv.FormatUint(uint64(row[8+j])|0x100, 16)[1:])
		}
		mac := normalizeMAC(hw.String())
		if mac == "" {
			continue
		}
		out = append(out, neighbour{addr: addr, mac: mac})
	}
	return out
}
