package api

import (
	"fmt"
	"net/http"
	"net/netip"
	"strings"
)

// ParseTrustedProxies reads the --trusted-proxies setting: a list of IP
// addresses and CIDR ranges, separated by commas or spaces, naming the reverse
// proxies whose forwarding headers GWatch may believe. A bare address stands
// for itself alone. An empty list trusts no proxy, which is the default.
func ParseTrustedProxies(spec string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.FieldsFunc(spec, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if strings.Contains(f, "/") {
			p, err := netip.ParsePrefix(f)
			if err != nil {
				return nil, fmt.Errorf("trusted proxy %q is not an address or CIDR range", f)
			}
			if p.Addr().Is4In6() {
				// ::ffff:10.0.0.0/104 and 10.0.0.0/8 mean the same machines.
				if p.Bits() < 96 {
					return nil, fmt.Errorf("trusted proxy %q is not an address or CIDR range", f)
				}
				p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil || a.Zone() != "" {
			return nil, fmt.Errorf("trusted proxy %q is not an address or CIDR range", f)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// trustedProxy reports whether host — a socket peer or a forwarding hop — is
// one of the configured TrustedProxies.
func (s *Server) trustedProxy(host string) bool {
	if len(s.TrustedProxies) == 0 {
		return false
	}
	a, ok := parseHop(host)
	if !ok {
		return false
	}
	for _, p := range s.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// forwardedClient works out, for a request whose socket peer is a trusted
// proxy, the client that proxy is passing it on for. X-Forwarded-For is read
// from the right: each proxy appends the address it was connected from, so
// the rightmost hop that is not itself a trusted proxy is the first address
// no trusted party vouches past — anything to its left was written by the
// client and may be invented. If X-Forwarded-For is absent, X-Real-IP (one
// address, set by the proxy) is used. A chain that cannot be read gives no
// answer and the caller falls back to the socket peer.
func (s *Server) forwardedClient(r *http.Request) (string, bool) {
	if values := r.Header.Values("X-Forwarded-For"); len(values) > 0 {
		hops := strings.Split(strings.Join(values, ","), ",")
		var leftmost netip.Addr
		for i := len(hops) - 1; i >= 0; i-- {
			a, ok := parseHop(hops[i])
			if !ok {
				return "", false
			}
			leftmost = a
			if !s.trustedProxy(a.String()) {
				return a.String(), true
			}
		}
		// Every hop is a trusted proxy: the request started at one of them.
		return leftmost.String(), leftmost.IsValid()
	}
	if v := r.Header.Get("X-Real-Ip"); v != "" {
		if a, ok := parseHop(v); ok {
			return a.String(), true
		}
	}
	return "", false
}

// parseHop reads one address from a forwarding header or a socket peer:
// a bare IPv4 or IPv6 address, optionally with a port ("1.2.3.4:80",
// "[2001:db8::1]:80") or in brackets. IPv4-mapped IPv6 is unmapped so that a
// client is one identity whichever way the proxy wrote it.
func parseHop(v string) (netip.Addr, bool) {
	v = strings.TrimSpace(v)
	if a, err := netip.ParseAddr(strings.Trim(v, "[]")); err == nil {
		return a.WithZone("").Unmap(), true
	}
	if ap, err := netip.ParseAddrPort(v); err == nil {
		return ap.Addr().WithZone("").Unmap(), true
	}
	return netip.Addr{}, false
}
