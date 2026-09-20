package checks

// GWatch builds and reads its own ICMP echo packets rather than depending on a
// ping library. A ping is a small, stable protocol — RFC 792 for IPv4, RFC 4443
// for IPv6 — and owning it keeps a network monitor free of a dependency that
// pulls in half of golang.org/x/net for a few hundred lines of work we can
// explain ourselves.
//
// Two kinds of socket can carry it:
//
//   - an unprivileged ICMP datagram socket (Linux's ping_group_range, macOS by
//     default). The kernel owns the echo identifier: it overwrites the one we
//     send and rewrites the one in the reply, so replies can only be ours and
//     there is no identifier to match on. Replies arrive without an IP header.
//   - a raw socket, which needs administrator/root or CAP_NET_RAW. Here the
//     identifier is ours to choose and to match on, and on IPv4 the reply may
//     still carry its IP header (Go's net package usually strips it; we strip
//     whatever is left).
//
// Windows has no unprivileged ICMP socket, but the service account can open a
// raw one, so it goes straight there.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"time"
)

const (
	// Protocol numbers, from the IANA registry.
	protoICMPv4 = 1
	protoICMPv6 = 58

	icmpv4EchoRequest = 8
	icmpv4EchoReply   = 0
	icmpv6EchoRequest = 128
	icmpv6EchoReply   = 129

	// An echo body is 4 bytes of identifier and sequence followed by whatever
	// payload we choose. Ours is the send time, so a reply carries everything
	// needed to time it without keeping state per packet.
	icmpBodyOffset  = 4
	icmpPayloadSize = 8
	icmpHeaderSize  = 4 // type, code, checksum
	icmpMinMessage  = icmpHeaderSize + icmpBodyOffset
)

// icmpID hands out a 16-bit echo identifier that no other check in this
// process is using at the same time. The process id seeds it so two GWatch
// processes on one machine (a service and someone running it by hand) do not
// answer for each other's packets, and the counter separates checks that run
// in parallel. Only raw sockets use it; an unprivileged socket gets its
// identifier from the kernel.
var icmpIDCounter atomic.Uint32

func nextICMPID() uint16 {
	return uint16(uint32(os.Getpid())*31 + icmpIDCounter.Add(1))
}

// icmpEcho is an echo request or reply.
type icmpEcho struct {
	Type    uint8
	Code    uint8
	ID      uint16
	Seq     uint16
	Payload []byte
}

// marshal lays the message out on the wire. withChecksum is false for IPv6,
// where the kernel fills the field in: the ICMPv6 checksum covers a pseudo
// header built from the source address, which is not chosen until the packet
// is on its way out.
func (e icmpEcho) marshal(withChecksum bool) []byte {
	b := make([]byte, icmpMinMessage+len(e.Payload))
	b[0] = e.Type
	b[1] = e.Code
	binary.BigEndian.PutUint16(b[4:], e.ID)
	binary.BigEndian.PutUint16(b[6:], e.Seq)
	copy(b[8:], e.Payload)
	if withChecksum {
		binary.BigEndian.PutUint16(b[2:], icmpChecksum(b))
	}
	return b
}

// parseICMPEcho reads a message back. It reports false for anything too short
// to be one, which is all the validation a reply needs: the socket already
// filtered by protocol, and the caller checks the type, the identifier and the
// sequence number itself.
func parseICMPEcho(b []byte) (icmpEcho, bool) {
	if len(b) < icmpMinMessage {
		return icmpEcho{}, false
	}
	e := icmpEcho{
		Type:    b[0],
		Code:    b[1],
		ID:      binary.BigEndian.Uint16(b[4:]),
		Seq:     binary.BigEndian.Uint16(b[6:]),
		Payload: b[8:],
	}
	return e, true
}

// icmpChecksum is the internet checksum of RFC 1071: the one's complement of
// the one's complement sum of the buffer read as big-endian 16-bit words, with
// a final odd byte padded with zero. The checksum field must be zero going in.
func icmpChecksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		sum += uint32(b[len(b)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

// stripIPv4Header removes a leading IPv4 header if there is one. Go's net
// package already does this for ip4 raw sockets on the platforms we ship, but
// not every kernel agrees on whether the header comes along, and an
// unprivileged socket never sends one — so rather than trust the platform, we
// look: version 4, a header length that fits, and a protocol of ICMP.
func stripIPv4Header(b []byte) []byte {
	if len(b) < 20 || b[0]>>4 != 4 {
		return b
	}
	n := int(b[0]&0x0f) << 2
	if n < 20 || n > len(b) || b[9] != protoICMPv4 {
		return b
	}
	return b[n:]
}

// icmpConn is an open ICMP socket and what we know about how it behaves.
type icmpConn struct {
	pc net.PacketConn
	// unprivileged sockets have their echo identifier managed by the kernel,
	// so a reply's identifier is not the one we sent and must not be matched.
	unprivileged bool
	ipv6         bool
}

// Close releases the socket.
func (c *icmpConn) Close() error { return c.pc.Close() }

// addr converts a destination into the address type this socket expects: a
// UDP address for the unprivileged datagram socket (the port is unused — the
// kernel takes the echo identifier from the socket), an IP address for a raw
// one.
func (c *icmpConn) addr(dst *net.IPAddr) net.Addr {
	if c.unprivileged {
		return &net.UDPAddr{IP: dst.IP, Zone: dst.Zone}
	}
	return dst
}

// icmpListener opens the socket a ping run will use. It is a variable so that
// tests can stand in a socket that cannot be opened, which is the one case
// that matters here and the one no test can arrange for itself.
var icmpListener = listenICMP

// listenICMP opens the best socket available for this address family, trying
// the unprivileged one first so that GWatch running as an ordinary user still
// pings without help. A raw socket is the fallback, and on Windows the only
// option.
func listenICMP(ipv6 bool) (*icmpConn, error) {
	pc, err := listenUnprivilegedICMP(ipv6)
	if err == nil {
		return &icmpConn{pc: pc, unprivileged: true, ipv6: ipv6}, nil
	}
	// Anything that stops an unprivileged socket opening — no permission, no
	// such protocol, a kernel that does not offer it — is a reason to try the
	// raw socket rather than to give up.
	unprivErr := err
	network := "ip4:icmp"
	if ipv6 {
		network = "ip6:ipv6-icmp"
	}
	raw, err := net.ListenPacket(network, "")
	if err != nil {
		if isSocketPermissionError(unprivErr) || isSocketPermissionError(err) {
			return nil, fmt.Errorf("no permission to open an ICMP socket (%v; raw socket: %v)", unprivErr, err)
		}
		return nil, fmt.Errorf("could not open an ICMP socket (%v; raw socket: %v)", unprivErr, err)
	}
	return &icmpConn{pc: raw, ipv6: ipv6}, nil
}

// resolvePingHost turns a host into one address to ping. IPv4 wins when a name
// has both, which is what the ping library GWatch used to depend on did and
// what the machines on a home or office LAN almost always want. A DNS failure
// comes back as *net.DNSError so the caller can tell it apart from a network
// problem and stop rather than fall back.
func resolvePingHost(ctx context.Context, host string) (*net.IPAddr, error) {
	if ip := net.ParseIP(host); ip != nil {
		return &net.IPAddr{IP: ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for i := range addrs {
		if addrs[i].IP.To4() != nil {
			return &addrs[i], nil
		}
	}
	if len(addrs) > 0 {
		return &addrs[0], nil
	}
	return nil, &net.DNSError{Err: "no addresses returned", Name: host, IsNotFound: true}
}

// runBuiltinPing sends count echo requests from a socket of our own and
// collects the replies. It is the "builtin" ping method, and the first thing
// "auto" tries.
func runBuiltinPing(ctx context.Context, host string, count int, timeout time.Duration) (pingResult, error) {
	dst, err := resolvePingHost(ctx, host)
	if err != nil {
		return pingResult{}, err
	}
	conn, err := icmpListener(dst.IP.To4() == nil)
	if err != nil {
		return pingResult{}, err
	}
	defer conn.Close()
	return conn.exchange(ctx, dst, count, timeout)
}

// exchange sends the echo requests, spaced at pingInterval, and reads replies
// until every packet is accounted for or the timeout runs out. Sending and
// receiving share one loop: between packets it waits for replies, which keeps
// a lost reply from delaying the next request and needs no goroutine of its
// own to do it.
func (c *icmpConn) exchange(ctx context.Context, dst *net.IPAddr, count int, timeout time.Duration) (pingResult, error) {
	var pr pingResult
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	deadline := time.Now().Add(timeout)
	id := nextICMPID()
	to := c.addr(dst)
	reqType, replyType := uint8(icmpv4EchoRequest), uint8(icmpv4EchoReply)
	if c.ipv6 {
		reqType, replyType = icmpv6EchoRequest, icmpv6EchoReply
	}

	// A context cancelled mid-run unblocks the socket by expiring its
	// deadline; the loop below then sees ctx.Err() and stops.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = c.pc.SetDeadline(time.Now())
		case <-done:
		}
	}()

	seen := make(map[uint16]bool, count)
	buf := make([]byte, 1500)
	var sendErr error
	nextSend := time.Now()
	for pr.Sent < count || pr.Received < pr.Sent {
		now := time.Now()
		if !now.Before(deadline) || ctx.Err() != nil {
			break
		}
		if pr.Sent < count && !now.Before(nextSend) {
			payload := make([]byte, icmpPayloadSize)
			binary.BigEndian.PutUint64(payload, uint64(now.UnixNano()))
			msg := icmpEcho{Type: reqType, ID: id, Seq: uint16(pr.Sent), Payload: payload}
			if _, err := c.pc.WriteTo(msg.marshal(!c.ipv6), to); err != nil {
				sendErr = err
				break
			}
			pr.Sent++
			nextSend = now.Add(pingInterval)
			continue
		}
		wait := deadline
		if pr.Sent < count && nextSend.Before(wait) {
			wait = nextSend
		}
		if err := c.pc.SetReadDeadline(wait); err != nil {
			return pr, err
		}
		n, from, err := c.pc.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue // time to send the next packet, or the run is over
			}
			if ctx.Err() != nil {
				break
			}
			if pr.Received > 0 {
				break // keep what came back rather than losing the whole run
			}
			return pr, err
		}
		if !addrIP(from).Equal(dst.IP) {
			continue
		}
		msg, ok := parseICMPEcho(stripIPv4Header(buf[:n]))
		if !ok || msg.Type != replyType {
			continue // our own outgoing packet looped back, or somebody else's
		}
		if !c.unprivileged && msg.ID != id {
			continue
		}
		if int(msg.Seq) >= pr.Sent || seen[msg.Seq] || len(msg.Payload) < icmpPayloadSize {
			continue
		}
		sent := time.Unix(0, int64(binary.BigEndian.Uint64(msg.Payload[:icmpPayloadSize])))
		rtt := time.Since(sent)
		if rtt < 0 {
			rtt = 0
		}
		seen[msg.Seq] = true
		pr.Received++
		pr.RTTs = append(pr.RTTs, rtt)
	}
	if pr.Received == 0 {
		if sendErr != nil {
			return pr, sendErr
		}
		if ctx.Err() != nil {
			return pr, ctx.Err()
		}
		if pr.Sent == 0 {
			return pr, errors.New("no packets were sent")
		}
	}
	return pr, nil
}

// addrIP pulls the address out of whichever kind of packet address the socket
// reported: *net.IPAddr from a raw socket, *net.UDPAddr from an unprivileged
// one.
func addrIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.IPAddr:
		return v.IP
	case *net.UDPAddr:
		return v.IP
	}
	return nil
}
