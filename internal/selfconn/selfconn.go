// Package selfconn remembers the loopback connections GWatch itself has open,
// so the API can tell a request GWatch made from one a person at this computer
// made.
//
// A client on this computer is, by default, the administrator with no sign-in.
// GWatch also makes requests of its own on other people's instructions: an HTTP
// check, a webhook action, a Matter server URL. Pointed at GWatch's own port,
// such a request arrives over loopback exactly as a browser on this computer
// would, and would be handed the administrator's rights. That let a read-write
// API key, which may create and test HTTP checks, do anything an administrator
// can. Every outbound connection that could carry such a request is dialled
// through this package, and the API refuses the local-administrator shortcut
// to any connection found here.
package selfconn

import (
	"context"
	"net"
	"net/netip"
	"sync"
)

var (
	mu   sync.Mutex
	open = map[netip.AddrPort]int{}
)

// DialContext wraps a dialer's DialContext so that every connection it opens
// from a loopback address is remembered until it is closed.
func DialContext(dial func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return Track(conn), nil
	}
}

// Track remembers conn if its local end is a loopback address, and returns a
// connection that forgets it again on Close. Any other connection is returned
// unchanged: only a loopback peer can earn the local-administrator shortcut.
func Track(conn net.Conn) net.Conn {
	key, ok := parse(conn.LocalAddr().String())
	if !ok || !key.Addr().IsLoopback() {
		return conn
	}
	mu.Lock()
	open[key]++
	mu.Unlock()
	return &tracked{Conn: conn, key: key}
}

// Is reports whether remoteAddr, the peer address of a request the server
// received, is the local end of a connection GWatch opened itself.
func Is(remoteAddr string) bool {
	key, ok := parse(remoteAddr)
	if !ok {
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	return open[key] > 0
}

func parse(s string) (netip.AddrPort, bool) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return netip.AddrPort{}, false
	}
	return netip.AddrPortFrom(ap.Addr().Unmap().WithZone(""), ap.Port()), true
}

type tracked struct {
	net.Conn
	key  netip.AddrPort
	once sync.Once
}

func (t *tracked) Close() error {
	t.once.Do(func() {
		mu.Lock()
		if open[t.key] <= 1 {
			delete(open, t.key)
		} else {
			open[t.key]--
		}
		mu.Unlock()
	})
	return t.Conn.Close()
}
