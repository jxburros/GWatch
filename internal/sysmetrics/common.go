package sysmetrics

import "net"

// netInterfaces is net.Interfaces behind a variable so a test can substitute a
// fixed list instead of whatever the build machine happens to have.
var netInterfaces = net.Interfaces

// upInterfaces returns which interfaces are up and which are loopback, keyed
// by name. Counter files name interfaces but do not say either.
func upInterfaces() (up map[string]bool, loopback map[string]bool) {
	up, loopback = map[string]bool{}, map[string]bool{}
	ifaces, err := netInterfaces()
	if err != nil {
		return up, loopback
	}
	for _, n := range ifaces {
		up[n.Name] = n.Flags&net.FlagUp != 0
		loopback[n.Name] = n.Flags&net.FlagLoopback != 0
	}
	return up, loopback
}
