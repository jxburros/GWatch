package checks

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// The internet checksum is the one part of this that has to be exactly right,
// so it is checked against the worked example in RFC 1071 and against a real
// echo request the Linux kernel accepted.
func TestICMPChecksum(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		want uint16
	}{
		{"rfc 1071 example", []byte{0x00, 0x01, 0xf2, 0x03, 0xf4, 0xf5, 0xf6, 0xf7}, 0x220d},
		{"echo request id 0x1234 seq 1", []byte{8, 0, 0, 0, 0x12, 0x34, 0, 1, 1, 2, 3, 4, 5, 6, 7, 8}, 0xd5b6},
		{"odd length pads with zero", []byte{0x00, 0x01, 0xf2}, 0x0dfe},
	}
	for _, c := range cases {
		if got := icmpChecksum(c.in); got != c.want {
			t.Errorf("%s: checksum = %#04x, want %#04x", c.name, got, c.want)
		}
	}
	// A message that already carries its checksum sums to zero, which is how a
	// receiver validates one.
	msg := icmpEcho{Type: icmpv4EchoRequest, ID: 0x1234, Seq: 1, Payload: []byte{1, 2, 3, 4, 5, 6, 7, 8}}.marshal(true)
	if got := icmpChecksum(msg); got != 0 {
		t.Errorf("checksum over a complete message = %#04x, want 0", got)
	}
}

func TestICMPEchoRoundTrip(t *testing.T) {
	payload := make([]byte, icmpPayloadSize)
	binary.BigEndian.PutUint64(payload, 0x0102030405060708)
	for _, c := range []struct {
		name     string
		typ      uint8
		checksum bool
	}{
		{"ipv4", icmpv4EchoRequest, true},
		{"ipv6 (kernel fills the checksum in)", icmpv6EchoRequest, false},
	} {
		wire := icmpEcho{Type: c.typ, ID: 0xbeef, Seq: 7, Payload: payload}.marshal(c.checksum)
		if len(wire) != icmpMinMessage+icmpPayloadSize {
			t.Fatalf("%s: wire length = %d", c.name, len(wire))
		}
		got, ok := parseICMPEcho(wire)
		if !ok || got.Type != c.typ || got.Code != 0 || got.ID != 0xbeef || got.Seq != 7 {
			t.Fatalf("%s: parsed = %+v ok=%v", c.name, got, ok)
		}
		if string(got.Payload) != string(payload) {
			t.Errorf("%s: payload = % x", c.name, got.Payload)
		}
		if c.checksum && wire[2] == 0 && wire[3] == 0 {
			t.Errorf("%s: checksum was not filled in", c.name)
		}
		if !c.checksum && (wire[2] != 0 || wire[3] != 0) {
			t.Errorf("%s: checksum should have been left to the kernel", c.name)
		}
	}
	if _, ok := parseICMPEcho([]byte{8, 0, 0, 0, 0, 0}); ok {
		t.Error("a truncated message parsed")
	}
}

func TestStripIPv4Header(t *testing.T) {
	body := icmpEcho{Type: icmpv4EchoReply, ID: 1, Seq: 2, Payload: make([]byte, icmpPayloadSize)}.marshal(true)

	// A 24-byte header (IHL 6) exercises the options case as well as the
	// common one, and the protocol byte marks it as ICMP.
	hdr := make([]byte, 24)
	hdr[0] = 0x46 // version 4, header length 6 words
	hdr[9] = protoICMPv4
	withHeader := append(append([]byte{}, hdr...), body...)
	if got := stripIPv4Header(withHeader); string(got) != string(body) {
		t.Errorf("header not stripped: % x", got)
	}
	// Already stripped (an unprivileged socket, or a kernel that did it for
	// us): the message must come through untouched.
	if got := stripIPv4Header(body); string(got) != string(body) {
		t.Errorf("a bare message was altered: % x", got)
	}
	// A header carrying something other than ICMP is not ours to strip, and
	// neither is a buffer too short to hold a header at all.
	other := make([]byte, 20)
	other[0] = 0x45
	other[9] = 17 // UDP
	other = append(other, body...)
	if got := stripIPv4Header(other); string(got) != string(other) {
		t.Errorf("a non-ICMP packet was stripped: % x", got)
	}
	short := []byte{0x45, 0, 0, 0}
	if got := stripIPv4Header(short); string(got) != string(short) {
		t.Errorf("a runt was stripped: % x", got)
	}
}

func TestNextICMPIDIsUnique(t *testing.T) {
	seen := map[uint16]bool{}
	for i := 0; i < 1000; i++ {
		id := nextICMPID()
		if seen[id] {
			t.Fatalf("identifier %d handed out twice after %d calls", id, i)
		}
		seen[id] = true
	}
}

// The built-in sender against the loopback address is the only end-to-end
// proof there is. Plenty of build machines (containers without CAP_NET_RAW and
// with an empty ping_group_range, for one) allow neither kind of ICMP socket,
// so this skips rather than fails when the socket cannot be opened at all.
func TestBuiltinPingLoopback(t *testing.T) {
	for _, c := range []struct{ name, host string }{{"ipv4", "127.0.0.1"}, {"ipv6", "::1"}} {
		t.Run(c.name, func(t *testing.T) {
			conn, err := listenICMP(c.name == "ipv6")
			if err != nil {
				t.Skipf("no ICMP socket available here: %v", err)
			}
			conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			pr, err := runBuiltinPing(ctx, c.host, 3, 3*time.Second)
			if err != nil {
				t.Fatalf("ping %s: %v", c.host, err)
			}
			if pr.Sent != 3 || pr.Received != 3 || len(pr.RTTs) != 3 {
				t.Fatalf("sent=%d received=%d rtts=%v", pr.Sent, pr.Received, pr.RTTs)
			}
			for _, d := range pr.RTTs {
				if d < 0 || d > 3*time.Second {
					t.Errorf("implausible round trip %v", d)
				}
			}
		})
	}
}

func TestBuiltinPingRespectsCancellation(t *testing.T) {
	if conn, err := listenICMP(false); err != nil {
		t.Skipf("no ICMP socket available here: %v", err)
	} else {
		conn.Close()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := runBuiltinPing(ctx, "127.0.0.1", 20, 10*time.Second); err == nil {
		t.Error("expected an error from a cancelled context")
	}
	if time.Since(start) > 2*time.Second {
		t.Error("a cancelled ping did not return promptly")
	}
}

// A machine that will not open an ICMP socket is the whole reason the method
// setting exists: "auto" quietly moves on to the ping command, "builtin" says
// what went wrong instead.
func TestPingMethodHandlesADeniedSocket(t *testing.T) {
	origListen, origSystem := icmpListener, systemPingFunc
	defer func() { icmpListener, systemPingFunc = origListen, origSystem }()
	icmpListener = func(ipv6 bool) (*icmpConn, error) {
		return nil, errors.New("no permission to open an ICMP socket (socket: operation not permitted)")
	}
	systemCalls := 0
	systemPingFunc = func(ctx context.Context, host string, count int, timeout time.Duration) (pingResult, error) {
		systemCalls++
		return pingResult{Sent: count, Received: count, RTTs: []time.Duration{time.Millisecond}}, nil
	}

	pr, err := runPing(context.Background(), "127.0.0.1", 1, time.Second, model.PingMethodAuto)
	if err != nil || pr.Received != 1 || systemCalls != 1 {
		t.Fatalf("auto did not fall back to the system ping: %+v err=%v calls=%d", pr, err, systemCalls)
	}

	_, err = runPing(context.Background(), "127.0.0.1", 1, time.Second, model.PingMethodBuiltin)
	if err == nil || !strings.Contains(err.Error(), "permission") {
		t.Fatalf("builtin error = %v, want one that names the permission problem", err)
	}
	if systemCalls != 1 {
		t.Errorf("builtin ran the system ping command (%d calls)", systemCalls)
	}

	systemCalls = 0
	if _, err := runPing(context.Background(), "127.0.0.1", 1, time.Second, model.PingMethodSystem); err != nil || systemCalls != 1 {
		t.Errorf("system method: err=%v calls=%d", err, systemCalls)
	}
}

// A name that does not resolve is not a reason to try the ping command: it
// will not resolve there either, and the DNS error is the useful one.
func TestPingAutoDoesNotFallBackOnDNSFailure(t *testing.T) {
	origSystem := systemPingFunc
	defer func() { systemPingFunc = origSystem }()
	called := false
	systemPingFunc = func(ctx context.Context, host string, count int, timeout time.Duration) (pingResult, error) {
		called = true
		return pingResult{}, nil
	}
	_, err := runPing(context.Background(), "no-such-host.invalid", 1, time.Second, model.PingMethodAuto)
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) {
		t.Fatalf("err = %v, want a *net.DNSError", err)
	}
	if called {
		t.Error("the system ping command was run for a name that does not resolve")
	}
}

func TestResolvePingMethod(t *testing.T) {
	cases := []struct{ override, global, want string }{
		{"", "", model.PingMethodAuto},
		{"", model.PingMethodSystem, model.PingMethodSystem},
		{model.PingMethodBuiltin, model.PingMethodSystem, model.PingMethodBuiltin},
		{model.PingMethodAuto, model.PingMethodSystem, model.PingMethodAuto},
		{"nonsense", model.PingMethodBuiltin, model.PingMethodBuiltin},
		{"nonsense", "rubbish", model.PingMethodAuto},
	}
	for _, c := range cases {
		if got := resolvePingMethod(c.override, c.global); got != c.want {
			t.Errorf("resolvePingMethod(%q, %q) = %q, want %q", c.override, c.global, got, c.want)
		}
	}
}

// The check itself has to hand the resolved method down, or the setting is
// decoration.
func TestPingCheckUsesTheResolvedMethod(t *testing.T) {
	orig := pingFunc
	defer func() { pingFunc = orig }()
	var got string
	pingFunc = func(ctx context.Context, host string, count int, timeout time.Duration, method string) (pingResult, error) {
		got = method
		return pingResult{Sent: 1, Received: 1, RTTs: []time.Duration{time.Millisecond}}, nil
	}
	check := model.Check{Type: model.CheckPing, Name: "Ping", IntervalSeconds: 60, TimeoutSeconds: 5}
	runPingCheck(context.Background(), check, "127.0.0.1", Options{PingMethod: model.PingMethodSystem})
	if got != model.PingMethodSystem {
		t.Errorf("global setting: method = %q", got)
	}
	check.Config.PingMethod = model.PingMethodBuiltin
	runPingCheck(context.Background(), check, "127.0.0.1", Options{PingMethod: model.PingMethodSystem})
	if got != model.PingMethodBuiltin {
		t.Errorf("per-check override: method = %q", got)
	}
}
