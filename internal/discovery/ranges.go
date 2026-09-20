// Package discovery sweeps ranges of IPv4 addresses, works out which of them
// answer, and describes each responder well enough that it can be turned into
// a node without anyone typing an address twice.
//
// It exists because the first ten minutes with a network monitor are spent
// copying addresses off a router's client list. A sweep is the same work done
// once, by the machine that is going to be pinging them anyway: everything
// here goes through the same ping path a ping check uses, so a device that
// answers a sweep answers a check too.
//
// Nothing in here is a port scanner. The sweep touches a short, fixed list of
// ports on hosts that already answered a ping, purely to tell a printer from a
// router, and every one of those probes is a plain TCP connect closed the
// moment it succeeds.
package discovery

import (
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// MaxAddresses caps how many addresses one run may sweep. A /20 is 4094 hosts
// and takes a couple of minutes at 64 workers; anything past that is a scan of
// somebody else's network as often as it is a typo, and refusing it with a
// message is kinder than starting an hour of pinging.
const MaxAddresses = 4096

// ParseRanges turns the lines a person typed into the addresses to sweep.
//
// Each line is a CIDR block (192.168.1.0/24), a dashed range written out in
// full (192.168.1.10-192.168.1.50) or abbreviated to its last octet
// (192.168.1.10-50), or a single address. Blank lines and anything after a #
// are ignored, and one line may hold several ranges separated by commas or
// semicolons, because people paste lists.
//
// The result is sorted and free of duplicates: overlapping ranges are an
// ordinary thing to type, and pinging an address twice is only ever a waste.
func ParseRanges(lines []string) ([]netip.Addr, error) {
	seen := map[netip.Addr]bool{}
	var out []netip.Addr
	var anySpec bool

	add := func(a netip.Addr) error {
		if seen[a] {
			return nil
		}
		if len(out)+1 > MaxAddresses {
			return fmt.Errorf("that is more than %d addresses; sweep a smaller range (a /20 is the largest block that fits)", MaxAddresses)
		}
		seen[a] = true
		out = append(out, a)
		return nil
	}

	for _, line := range lines {
		for _, spec := range splitSpecs(line) {
			anySpec = true
			addrs, err := parseSpec(spec)
			if err != nil {
				return nil, err
			}
			for _, a := range addrs {
				if err := add(a); err != nil {
					return nil, err
				}
			}
		}
	}
	if !anySpec {
		return nil, fmt.Errorf("give at least one range, such as 192.168.1.0/24 or 192.168.1.10-50")
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("those ranges hold no addresses to scan")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out, nil
}

// splitSpecs breaks one typed line into the individual ranges on it.
func splitSpecs(line string) []string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	var out []string
	for _, part := range strings.FieldsFunc(line, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parseSpec expands one range into its addresses.
func parseSpec(spec string) ([]netip.Addr, error) {
	switch {
	case strings.Contains(spec, "/"):
		return parseCIDR(spec)
	case strings.Contains(spec, "-"):
		return parseDashed(spec)
	default:
		a, err := parseIPv4(spec)
		if err != nil {
			return nil, err
		}
		return []netip.Addr{a}, nil
	}
}

func parseCIDR(spec string) ([]netip.Addr, error) {
	p, err := netip.ParsePrefix(spec)
	if err != nil {
		return nil, badSpecError(spec)
	}
	if !p.Addr().Is4() {
		return nil, ipv6Error(spec)
	}
	p = p.Masked()
	first := beU32(p.Addr())
	last := first | (^uint32(0) >> uint(p.Bits()))
	// The first and last address of a block are the network and the broadcast
	// address. Neither belongs to a device, and pinging a broadcast address is
	// how one host answers for several. They are skipped for every block big
	// enough to have them — which covers the /24 a home network actually is,
	// and the smaller blocks too, since .0 and .127 of a /25 are no more a
	// device than .0 and .255 of a /24. A /31 and a /32 have no such pair.
	if p.Bits() <= 30 {
		first++
		last--
	}
	if last < first {
		return nil, nil
	}
	if n := uint64(last) - uint64(first) + 1; n > MaxAddresses {
		return nil, tooManyError(spec, n)
	}
	return expand(first, last), nil
}

func parseDashed(spec string) ([]netip.Addr, error) {
	lo, hi, _ := strings.Cut(spec, "-")
	lo, hi = strings.TrimSpace(lo), strings.TrimSpace(hi)
	start, err := parseIPv4(lo)
	if err != nil {
		return nil, err
	}
	var end netip.Addr
	if strings.Contains(hi, ".") || strings.Contains(hi, ":") {
		if end, err = parseIPv4(hi); err != nil {
			return nil, err
		}
	} else {
		// The shorthand: only the last octet is given, so the rest of the
		// address comes from the start of the range.
		n, err := strconv.Atoi(hi)
		if err != nil || n < 0 || n > 255 {
			return nil, fmt.Errorf("%s does not end in an address or a last octet; try 192.168.1.10-50", spec)
		}
		b := start.As4()
		b[3] = byte(n)
		end = netip.AddrFrom4(b)
	}
	first, last := beU32(start), beU32(end)
	if last < first {
		return nil, fmt.Errorf("%s runs backwards: %s comes after %s", spec, start, end)
	}
	if n := uint64(last) - uint64(first) + 1; n > MaxAddresses {
		return nil, tooManyError(spec, n)
	}
	return expand(first, last), nil
}

func parseIPv4(s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, badSpecError(s)
	}
	if !a.Is4() {
		return netip.Addr{}, ipv6Error(s)
	}
	return a, nil
}

func badSpecError(spec string) error {
	return fmt.Errorf("%q is not a range GWatch understands; try 192.168.1.0/24, 192.168.1.10-50 or a single address", spec)
}

func tooManyError(spec string, n uint64) error {
	return fmt.Errorf("%s covers %d addresses; the limit for one run is %d", spec, n, MaxAddresses)
}

// ipv6Error explains the one limitation worth spelling out. Sweeping IPv6 is
// not a matter of widening a loop: a /64 holds more addresses than a lifetime
// of pinging would get through, so it needs neighbour discovery rather than a
// sweep, and that is a separate piece of work.
func ipv6Error(spec string) error {
	return fmt.Errorf("%s is IPv6; discovery sweeps IPv4 only for now (an IPv6 subnet is far too large to ping through)", spec)
}

func expand(first, last uint32) []netip.Addr {
	out := make([]netip.Addr, 0, last-first+1)
	for v := first; ; v++ {
		out = append(out, netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}))
		if v == last {
			break
		}
	}
	return out
}

func beU32(a netip.Addr) uint32 {
	b := a.As4()
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}
