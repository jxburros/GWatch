//go:build windows

package checks

// Windows has no unprivileged ICMP socket: the choice there is a raw socket or
// the system ping command, and the service account can open a raw one. Saying
// so here rather than in listenICMP keeps the socket order the same on every
// platform — try the unprivileged socket, then the raw one — with only this
// one answer differing.

import (
	"errors"
	"net"
)

func listenUnprivilegedICMP(ipv6 bool) (net.PacketConn, error) {
	return nil, errors.New("Windows has no unprivileged ICMP socket")
}
