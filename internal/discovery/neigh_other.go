//go:build !linux && !windows

package discovery

// neighbourTable has no implementation here yet. macOS and the BSDs keep the
// table behind a routing socket (or `arp -an`), which is a later step; until
// then a sweep on them finds what answers a ping, without MAC addresses.
func neighbourTable() []neighbour { return nil }

// containerKind is "" off Linux: GWatch's container image is Linux.
func containerKind() string { return "" }
