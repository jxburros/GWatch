//go:build !windows

package checks

// The unprivileged ICMP socket is a datagram socket opened on the ICMP
// protocol directly. There is no way to ask for it through net.ListenPacket —
// "udp4" there means UDP, not ICMP — so it is opened by hand and handed to the
// net package, which then gives it deadlines, context-friendly reads and the
// rest of the runtime's poller. This file exists because that socket call has
// no counterpart on Windows.

import (
	"net"
	"os"
	"syscall"
)

func listenUnprivilegedICMP(ipv6 bool) (net.PacketConn, error) {
	family, proto := syscall.AF_INET, protoICMPv4
	if ipv6 {
		family, proto = syscall.AF_INET6, protoICMPv6
	}
	fd, err := syscall.Socket(family, syscall.SOCK_DGRAM, proto)
	if err != nil {
		return nil, os.NewSyscallError("socket", err)
	}
	// The runtime's network poller insists on a non-blocking descriptor.
	if err := syscall.SetNonblock(fd, true); err != nil {
		_ = syscall.Close(fd)
		return nil, os.NewSyscallError("setnonblock", err)
	}
	// FilePacketConn duplicates the descriptor, so ours is closed either way.
	f := os.NewFile(uintptr(fd), "icmp")
	pc, err := net.FilePacketConn(f)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	return pc, nil
}
