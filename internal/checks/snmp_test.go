package checks

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// ---- a fake SNMP v2c agent -----------------------------------------------
//
// gosnmp ships no test server, and a real one cannot be assumed on a build
// machine, so the tests answer the runner themselves. The encoder below writes
// just enough BER to build a GetResponse: the types a device answers a
// monitoring GET with, and nothing else.

const (
	berInteger     byte = 0x02
	berOctetString byte = 0x04
	berNull        byte = 0x05
	berSequence    byte = 0x30
	berCounter32   byte = 0x41
	berGauge32     byte = 0x42
	berTimeTicks   byte = 0x43
	berCounter64   byte = 0x46
	berGetResponse byte = 0xa2
)

// berLength writes a DER length: the short form below 128, the long form
// above it.
func berLength(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	var size []byte
	for v := n; v > 0; v >>= 8 {
		size = append([]byte{byte(v)}, size...)
	}
	return append([]byte{0x80 | byte(len(size))}, size...)
}

func berTLV(tag byte, body []byte) []byte {
	out := []byte{tag}
	out = append(out, berLength(len(body))...)
	return append(out, body...)
}

// berSigned encodes a signed integer the way SNMP's INTEGER wants it: the
// shortest two's-complement form.
func berSigned(tag byte, v int64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(v))
	i := 0
	for i < 7 && ((buf[i] == 0x00 && buf[i+1]&0x80 == 0) || (buf[i] == 0xff && buf[i+1]&0x80 != 0)) {
		i++
	}
	return berTLV(tag, buf[i:])
}

// berUnsigned encodes Counter32/Gauge32/TimeTicks/Counter64, which are
// unsigned: a leading zero byte is added when the top bit would otherwise
// make the value look negative.
func berUnsigned(tag byte, v uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, v)
	i := 0
	for i < 7 && buf[i] == 0x00 {
		i++
	}
	body := buf[i:]
	if body[0]&0x80 != 0 {
		body = append([]byte{0x00}, body...)
	}
	return berTLV(tag, body)
}

// readTLV splits one tag/length/value off the front of b.
func readTLV(b []byte) (tag byte, body, rest []byte, err error) {
	if len(b) < 2 {
		return 0, nil, nil, errors.New("truncated TLV")
	}
	tag = b[0]
	n := int(b[1])
	cursor := 2
	if n&0x80 != 0 {
		count := n & 0x7f
		if count == 0 || len(b) < 2+count {
			return 0, nil, nil, errors.New("bad long-form length")
		}
		n = 0
		for _, c := range b[2 : 2+count] {
			n = n<<8 | int(c)
		}
		cursor = 2 + count
	}
	if len(b) < cursor+n {
		return 0, nil, nil, errors.New("truncated value")
	}
	return tag, b[cursor : cursor+n], b[cursor+n:], nil
}

// decodeOID turns an encoded object identifier back into dotted form, which is
// how the fake agent looks a value up.
func decodeOID(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	parts := []string{fmt.Sprintf("%d", body[0]/40), fmt.Sprintf("%d", body[0]%40)}
	var acc uint64
	for _, c := range body[1:] {
		acc = acc<<7 | uint64(c&0x7f)
		if c&0x80 == 0 {
			parts = append(parts, fmt.Sprintf("%d", acc))
			acc = 0
		}
	}
	return "." + strings.Join(parts, ".")
}

// fakeAgent answers SNMP v2c GETs from a table of values. Values are stored as
// already-encoded TLVs, so a test can hand it a counter, a gauge or a string
// without the agent needing to know the difference.
type fakeAgent struct {
	conn   *net.UDPConn
	mu     sync.Mutex
	values map[string][]byte // dotted OID (with leading dot) -> encoded value
	silent bool              // when true, requests are read and never answered
	done   chan struct{}
}

func newFakeAgent(t *testing.T) *fakeAgent {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	a := &fakeAgent{conn: conn, values: map[string][]byte{}, done: make(chan struct{})}
	go a.serve()
	t.Cleanup(func() {
		conn.Close()
		<-a.done
	})
	return a
}

func (a *fakeAgent) hostPort() (string, int) {
	addr := a.conn.LocalAddr().(*net.UDPAddr)
	return addr.IP.String(), addr.Port
}

func (a *fakeAgent) set(oid string, value []byte) {
	a.mu.Lock()
	a.values[normalizeOID(oid)] = value
	a.mu.Unlock()
}

func (a *fakeAgent) setSilent(silent bool) {
	a.mu.Lock()
	a.silent = silent
	a.mu.Unlock()
}

func (a *fakeAgent) serve() {
	defer close(a.done)
	buf := make([]byte, 8192)
	for {
		n, from, err := a.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		a.mu.Lock()
		silent := a.silent
		a.mu.Unlock()
		if silent {
			continue
		}
		reply, err := a.respond(buf[:n])
		if err != nil {
			continue
		}
		_, _ = a.conn.WriteToUDP(reply, from)
	}
}

// respond parses a GetRequest and answers it with the table's values, echoing
// each requested OID back verbatim so the agent never has to encode one.
func (a *fakeAgent) respond(pkt []byte) ([]byte, error) {
	_, message, _, err := readTLV(pkt)
	if err != nil {
		return nil, err
	}
	_, versionBody, rest, err := readTLV(message) // version
	if err != nil {
		return nil, err
	}
	_ = versionBody
	_, community, rest, err := readTLV(rest) // community
	if err != nil {
		return nil, err
	}
	_ = community
	_, pdu, _, err := readTLV(rest) // the request PDU
	if err != nil {
		return nil, err
	}
	_, reqID, rest, err := readTLV(pdu)
	if err != nil {
		return nil, err
	}
	_, _, rest, err = readTLV(rest) // error-status
	if err != nil {
		return nil, err
	}
	_, _, rest, err = readTLV(rest) // error-index
	if err != nil {
		return nil, err
	}
	_, varbinds, _, err := readTLV(rest)
	if err != nil {
		return nil, err
	}

	var out []byte
	for len(varbinds) > 0 {
		_, vb, next, err := readTLV(varbinds)
		if err != nil {
			return nil, err
		}
		varbinds = next
		tag, oidBody, _, err := readTLV(vb)
		if err != nil {
			return nil, err
		}
		encodedOID := berTLV(tag, oidBody)
		a.mu.Lock()
		value, ok := a.values[decodeOID(oidBody)]
		a.mu.Unlock()
		if !ok {
			// 0x80 is noSuchObject: the answer a device gives for an OID it
			// does not implement.
			value = []byte{0x80, 0x00}
		}
		out = append(out, berTLV(berSequence, append(append([]byte{}, encodedOID...), value...))...)
	}

	body := berTLV(berInteger, reqID)
	body = append(body, berSigned(berInteger, 0)...) // error-status
	body = append(body, berSigned(berInteger, 0)...) // error-index
	body = append(body, berTLV(berSequence, out)...)
	packet := berSigned(berInteger, 1) // version 1 == SNMP v2c on the wire
	packet = append(packet, berTLV(berOctetString, []byte("public"))...)
	packet = append(packet, berTLV(berGetResponse, body)...)
	return berTLV(berSequence, packet), nil
}

// ---- helpers --------------------------------------------------------------

func f(v float64) *float64 { return &v }

// snmpCheck builds a check pointed at the fake agent and returns it with the
// node host to run it against.
func snmpCheck(t *testing.T, a *fakeAgent, id int64, oids ...model.SNMPOID) (model.Check, string) {
	t.Helper()
	host, port := a.hostPort()
	return model.Check{
		ID:             id,
		Type:           model.CheckSNMP,
		Name:           "SNMP",
		TimeoutSeconds: 2,
		Config: model.CheckConfig{
			SNMPVersion:   "2c",
			SNMPPort:      port,
			SNMPCommunity: "public",
			SNMPOIDs:      oids,
		},
	}, host
}

// seedSNMPSample plants the previous counter reading a check would have taken
// `ago` ago. Waiting for two real runs would make the rate depend on how long
// the test machine took between them; planting the first one makes the
// arithmetic exact.
func seedSNMPSample(t *testing.T, check model.Check, host string, readings map[string]snmpCounter, ago time.Duration) {
	t.Helper()
	snmpStateMu.Lock()
	snmpState[check.ID] = snmpSample{
		at:      time.Now().Add(-ago),
		byOID:   readings,
		version: snmpStateKey(check.Config, host, check.Config.SNMPPort),
	}
	snmpStateMu.Unlock()
	t.Cleanup(func() { ForgetCheck(check.ID) })
}

// ---- tests ----------------------------------------------------------------

func TestSNMPGaugeThresholds(t *testing.T) {
	agent := newFakeAgent(t)
	const oid = "1.3.6.1.2.1.25.3.3.1.2.1"
	agent.set(oid, berUnsigned(berGauge32, 85))

	t.Run("warning makes the check degraded", func(t *testing.T) {
		check, host := snmpCheck(t, agent, 0, model.SNMPOID{OID: oid, Name: "Processor", Kind: "gauge", Unit: "%", WarnAbove: f(80), CritAbove: f(95)})
		res := Run(context.Background(), check, Options{NodeHost: host})
		if res.Status != model.StatusDegraded {
			t.Fatalf("status = %s (%s), want degraded", res.Status, res.Message)
		}
		if len(res.Details.SNMP) != 1 || res.Details.SNMP[0].Value == nil || *res.Details.SNMP[0].Value != 85 {
			t.Fatalf("details = %+v, want one reading of 85", res.Details.SNMP)
		}
		if res.Metrics["Processor"] != 85 {
			t.Errorf("metrics = %v, want Processor 85", res.Metrics)
		}
		if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "Processor") {
			t.Errorf("warnings = %v, want one naming the reading", res.Warnings)
		}
	})

	t.Run("critical makes the check down", func(t *testing.T) {
		check, host := snmpCheck(t, agent, 0, model.SNMPOID{OID: oid, Name: "Processor", Kind: "gauge", Unit: "%", WarnAbove: f(50), CritAbove: f(80)})
		res := Run(context.Background(), check, Options{NodeHost: host})
		if res.Status != model.StatusDown {
			t.Fatalf("status = %s, want down", res.Status)
		}
		for _, want := range []string{"Processor", oid, "85"} {
			if !strings.Contains(res.Message, want) {
				t.Errorf("message %q does not mention %q", res.Message, want)
			}
		}
	})

	t.Run("a scaled reading is compared after scaling", func(t *testing.T) {
		agent.set("1.3.6.1.2.1.1.3.0", berUnsigned(berTimeTicks, 360000)) // hundredths: one hour
		check, host := snmpCheck(t, agent, 0, model.SNMPOID{OID: "1.3.6.1.2.1.1.3.0", Name: "Uptime", Kind: "gauge", Scale: 0.01, Unit: "s", CritBelow: f(600)})
		res := Run(context.Background(), check, Options{NodeHost: host})
		if res.Status != model.StatusUp {
			t.Fatalf("status = %s (%s), want up", res.Status, res.Message)
		}
		if got := res.Metrics["Uptime"]; got != 3600 {
			t.Errorf("uptime = %v, want 3600 seconds", got)
		}
	})
}

func TestSNMPCounterRateAcrossAWrap(t *testing.T) {
	agent := newFakeAgent(t)
	const oid = "1.3.6.1.2.1.2.2.1.10.1"
	// The device has just wrapped its 32-bit octet counter: it read 2^32-100
	// ten seconds ago and reads 100 now, which is 200 octets, not a leap
	// backwards of four billion.
	agent.set(oid, berUnsigned(berCounter32, 100))

	check, host := snmpCheck(t, agent, 4242, model.SNMPOID{OID: oid, Name: "WAN in", Kind: "counter", Scale: 8, Unit: "bit/s"})
	seedSNMPSample(t, check, host, map[string]snmpCounter{normalizeOID(oid): {raw: 1<<32 - 100, bits: 32}}, 10*time.Second)

	res := Run(context.Background(), check, Options{NodeHost: host})
	if res.Status != model.StatusUp {
		t.Fatalf("status = %s (%s), want up", res.Status, res.Message)
	}
	rate := res.Details.SNMP[0].Rate
	if rate == nil {
		t.Fatal("no rate reported for a counter with a previous sample")
	}
	// 200 octets over ten seconds, times eight bits: 160 bit/s, give or take
	// the moment the second sample was actually taken.
	if *rate < 159 || *rate > 161 {
		t.Fatalf("rate = %v bit/s, want about 160", *rate)
	}
	if res.Details.SNMP[0].Value != nil {
		t.Errorf("a counter should report a rate, not a value: %+v", res.Details.SNMP[0])
	}
}

func TestSNMPCounterHasNoRateOnTheFirstSample(t *testing.T) {
	agent := newFakeAgent(t)
	const oid = "1.3.6.1.2.1.2.2.1.14.1"
	agent.set(oid, berUnsigned(berCounter32, 5000))

	check, host := snmpCheck(t, agent, 4243, model.SNMPOID{OID: oid, Name: "WAN errors", Kind: "counter", CritAbove: f(0)})
	ForgetCheck(check.ID)
	t.Cleanup(func() { ForgetCheck(check.ID) })

	res := Run(context.Background(), check, Options{NodeHost: host})
	if res.Status != model.StatusUp {
		t.Fatalf("status = %s (%s), want up: the first sample yields no rate and so no verdict", res.Status, res.Message)
	}
	if res.Details.SNMP[0].Rate != nil {
		t.Errorf("rate = %v, want none on the first sample", *res.Details.SNMP[0].Rate)
	}
	if _, ok := res.Metrics["WAN errors"]; ok {
		t.Errorf("metrics = %v, want nothing recorded without a rate", res.Metrics)
	}
}

func TestSNMPNonNumericValueIsReportedRaw(t *testing.T) {
	agent := newFakeAgent(t)
	const oid = "1.3.6.1.2.1.1.1.0"
	agent.set(oid, berTLV(berOctetString, []byte("RouterOS 7.14 on hEX S")))

	check, host := snmpCheck(t, agent, 0, model.SNMPOID{OID: oid, Name: "Description", CritAbove: f(1)})
	res := Run(context.Background(), check, Options{NodeHost: host})
	if res.Status != model.StatusUp {
		t.Fatalf("status = %s (%s), want up: text cannot cross a numeric threshold", res.Status, res.Message)
	}
	got := res.Details.SNMP[0]
	if got.Raw != "RouterOS 7.14 on hEX S" {
		t.Errorf("raw = %q, want the device's text", got.Raw)
	}
	if got.Value != nil || got.Rate != nil {
		t.Errorf("text was turned into a number: %+v", got)
	}
	if len(res.Metrics) != 0 {
		t.Errorf("metrics = %v, want nothing chartable for a text reading", res.Metrics)
	}
}

func TestSNMPMissingOIDIsDown(t *testing.T) {
	agent := newFakeAgent(t)
	agent.set("1.3.6.1.2.1.1.1.0", berTLV(berOctetString, []byte("a device")))

	check, host := snmpCheck(t, agent, 0, model.SNMPOID{OID: "1.3.6.1.2.1.2.2.1.8.99", Name: "Port 99"})
	res := Run(context.Background(), check, Options{NodeHost: host})
	if res.Status != model.StatusDown {
		t.Fatalf("status = %s, want down for an OID the device does not implement", res.Status)
	}
	if !strings.Contains(res.Message, "Port 99") {
		t.Errorf("message %q does not name the reading", res.Message)
	}
}

func TestSNMPTimeoutIsDown(t *testing.T) {
	agent := newFakeAgent(t)
	agent.setSilent(true)

	check, host := snmpCheck(t, agent, 0, model.SNMPOID{OID: "1.3.6.1.2.1.1.3.0", Name: "Uptime"})
	check.TimeoutSeconds = 1
	res := Run(context.Background(), check, Options{NodeHost: host})
	if res.Status != model.StatusDown {
		t.Fatalf("status = %s, want down", res.Status)
	}
	if !strings.Contains(res.Message, "no response") {
		t.Errorf("message = %q, want it to say there was no response", res.Message)
	}
	if !strings.Contains(res.Message, "community") {
		t.Errorf("message = %q, want the v2c hint that a wrong community looks like silence", res.Message)
	}
}

func TestCounterDeltaUnwindsAWrap(t *testing.T) {
	cases := []struct {
		name       string
		prev, cur  snmpCounter
		wantChange uint64
	}{
		{"32-bit, no wrap", snmpCounter{raw: 1000, bits: 32}, snmpCounter{raw: 1500, bits: 32}, 500},
		{"32-bit wrap", snmpCounter{raw: 1<<32 - 100, bits: 32}, snmpCounter{raw: 100, bits: 32}, 200},
		{"64-bit, no wrap", snmpCounter{raw: 1 << 40, bits: 64}, snmpCounter{raw: 1<<40 + 7, bits: 64}, 7},
		{"64-bit wrap", snmpCounter{raw: 1<<64 - 3, bits: 64}, snmpCounter{raw: 4, bits: 64}, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := counterDelta(tc.prev, tc.cur); got != tc.wantChange {
				t.Errorf("counterDelta = %d, want %d", got, tc.wantChange)
			}
		})
	}
}

func TestValidateSNMP(t *testing.T) {
	ok := []model.SNMPOID{{OID: "1.3.6.1.2.1.1.3.0", Name: "Uptime"}}
	cases := []struct {
		name string
		cfg  model.CheckConfig
		want string // "" means the configuration is valid
	}{
		{"a plain v2c check", model.CheckConfig{SNMPVersion: "2c", SNMPCommunity: "public", SNMPOIDs: ok}, ""},
		{"no OIDs", model.CheckConfig{SNMPVersion: "2c"}, "at least one OID"},
		{"an unknown version", model.CheckConfig{SNMPVersion: "1", SNMPOIDs: ok}, "unsupported SNMP version"},
		{"a MIB name rather than an OID", model.CheckConfig{SNMPOIDs: []model.SNMPOID{{OID: "sysUpTime.0", Name: "Uptime"}}}, "not a number"},
		{"a leading dot is fine", model.CheckConfig{SNMPOIDs: []model.SNMPOID{{OID: ".1.3.6.1.2.1.1.3.0", Name: "Uptime"}}}, ""},
		{"an unnamed OID", model.CheckConfig{SNMPOIDs: []model.SNMPOID{{OID: "1.3.6.1.2.1.1.3.0"}}}, "needs a name"},
		{"two readings of the same name", model.CheckConfig{SNMPOIDs: []model.SNMPOID{
			{OID: "1.3.6.1.2.1.2.2.1.10.1", Name: "Traffic"}, {OID: "1.3.6.1.2.1.2.2.1.16.1", Name: "traffic"},
		}}, "must differ"},
		{"an unknown kind", model.CheckConfig{SNMPOIDs: []model.SNMPOID{{OID: "1.3.6.1.2.1.1.3.0", Name: "Uptime", Kind: "rate"}}}, "unsupported kind"},
		{"critical below the warning it escalates", model.CheckConfig{SNMPOIDs: []model.SNMPOID{
			{OID: "1.3.6.1.2.1.1.3.0", Name: "Uptime", WarnAbove: f(90), CritAbove: f(80)},
		}}, "must be above"},
		{"critical above the low warning it escalates", model.CheckConfig{SNMPOIDs: []model.SNMPOID{
			{OID: "1.3.6.1.2.1.1.3.0", Name: "Uptime", WarnBelow: f(10), CritBelow: f(20)},
		}}, "must be below"},
		{"v3 without a user", model.CheckConfig{SNMPVersion: "3", SNMPOIDs: ok}, "needs a user name"},
		{"v3 with authentication but no password", model.CheckConfig{SNMPVersion: "3", SNMPUser: "monitor", SNMPAuthProto: "SHA256", SNMPOIDs: ok}, "authentication password is required"},
		{"v3 with privacy but no authentication", model.CheckConfig{SNMPVersion: "3", SNMPUser: "monitor", SNMPPrivProto: "AES", SNMPPrivPass: "x", SNMPOIDs: ok}, "privacy needs authentication"},
		{"v3 with privacy but no password", model.CheckConfig{SNMPVersion: "3", SNMPUser: "monitor", SNMPAuthProto: "SHA", SNMPAuthPass: "x", SNMPPrivProto: "AES256", SNMPOIDs: ok}, "privacy password is required"},
		{"a complete v3 check", model.CheckConfig{SNMPVersion: "3", SNMPUser: "monitor", SNMPAuthProto: "SHA256", SNMPAuthPass: "authpass", SNMPPrivProto: "AES", SNMPPrivPass: "privpass", SNMPOIDs: ok}, ""},
		{"too many OIDs", model.CheckConfig{SNMPOIDs: manyOIDs(maxSNMPOIDs + 1)}, "at most"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(model.Check{Type: model.CheckSNMP, Name: "SNMP", Config: tc.cfg}, "192.168.1.1")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("err = %v, want none", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func manyOIDs(n int) []model.SNMPOID {
	out := make([]model.SNMPOID, n)
	for i := range out {
		out[i] = model.SNMPOID{OID: fmt.Sprintf("1.3.6.1.2.1.2.2.1.10.%d", i), Name: fmt.Sprintf("Port %d", i)}
	}
	return out
}
