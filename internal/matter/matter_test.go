package matter_test

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/matter"
	"github.com/jxburros/GWatch/internal/matter/mattertest"
)

func sensor() mattertest.Node {
	return mattertest.Node{ID: 7, Available: true, Attributes: map[string]any{
		"0/40/1": "Eve Systems", "0/40/3": "Eve Weather", "0/40/5": "Balcony",
		"0/29/0":   []any{map[string]any{"0": 22, "1": 1}},
		"1/29/0":   []any{map[string]any{"0": 770, "1": 1}},
		"1/1026/0": 2150, "2/1029/0": 4875, "0/47/12": 180,
	}}
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return c
}

// The client greets, lists, reads and survives the noise a real server can
// make in between: an unrelated event, a ping and a fragmented answer.
func TestClientConversation(t *testing.T) {
	for _, chatty := range []bool{false, true} {
		srv := mattertest.New(sensor())
		srv.Chatty = chatty
		c, err := matter.Dial(ctx(t), srv.WSURL())
		if err != nil {
			t.Fatalf("chatty=%v dial: %v", chatty, err)
		}
		if c.Info.SchemaVersion != 11 || c.Info.SDKVersion == "" {
			t.Fatalf("server info not read: %+v", c.Info)
		}
		nodes, err := c.GetNodes(ctx(t))
		if err != nil || len(nodes) != 1 || nodes[0].NodeID != 7 || !nodes[0].Available {
			t.Fatalf("get_nodes: %v %+v", err, nodes)
		}
		vals, err := c.ReadAttributes(ctx(t), 7, []string{"1/1026/0", "2/1029/0", "9/9/9"})
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(vals["1/1026/0"]) != "2150" || string(vals["2/1029/0"]) != "4875" {
			t.Fatalf("values: %v", vals)
		}
		if _, ok := vals["9/9/9"]; ok {
			t.Fatal("a path the node does not have should be absent")
		}
		c.Close()
		srv.Close()
	}
}

// A large answer needs the 64-bit length form on the way in, and a large
// request the 16-bit form on the way out.
func TestClientLargeMessages(t *testing.T) {
	n := sensor()
	for i := 0; i < 3000; i++ {
		n.Attributes["5/"+itoa(i)+"/0"] = strings.Repeat("x", 30)
	}
	srv := mattertest.New(n)
	defer srv.Close()
	c, err := matter.Dial(ctx(t), srv.WSURL())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	nodes, err := c.GetNodes(ctx(t))
	if err != nil || len(nodes[0].Attributes) < 3000 {
		t.Fatalf("large answer: %v", err)
	}
	paths := make([]string, 0, 3000)
	for i := 0; i < 3000; i++ {
		paths = append(paths, "5/"+itoa(i)+"/0")
	}
	vals, err := c.ReadAttributes(ctx(t), 7, paths)
	if err != nil || len(vals) != 3000 {
		t.Fatalf("large request: %v %d", err, len(vals))
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func TestClientFallsBackToSingleReads(t *testing.T) {
	srv := mattertest.New(sensor())
	defer srv.Close()
	srv.SingleReadsOnly = true
	c, err := matter.Dial(ctx(t), srv.WSURL())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	vals, err := c.ReadAttributes(ctx(t), 7, []string{"1/1026/0", "0/47/12"})
	if err != nil || string(vals["0/47/12"]) != "180" || string(vals["1/1026/0"]) != "2150" {
		t.Fatalf("fallback read: %v %v", err, vals)
	}
	if got := len(srv.CommandLog()); got != 3 {
		t.Fatalf("expected one refused list read and two single reads, got %d commands", got)
	}
}

func TestClientErrorCodes(t *testing.T) {
	n := sensor()
	n.Available = false
	srv := mattertest.New(n)
	defer srv.Close()
	c, err := matter.Dial(ctx(t), srv.WSURL())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.ReadAttributes(ctx(t), 7, []string{"1/1026/0"})
	if !matter.IsCode(err, matter.ErrCodeNodeNotReady) {
		t.Fatalf("unavailable node: %v", err)
	}
	_, err = c.GetNode(ctx(t), 99)
	if !matter.IsCode(err, matter.ErrCodeNodeNotExists) || !strings.Contains(err.Error(), "NodeNotExists") {
		t.Fatalf("missing node: %v", err)
	}
	if _, err := c.Call(ctx(t), "frobnicate", nil); !matter.IsCode(err, matter.ErrCodeInvalidCommand) {
		t.Fatalf("unknown command: %v", err)
	}
}

func TestDialRefusesWhatIsNotAMatterServer(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer plain.Close()
	if _, err := matter.Dial(ctx(t), "ws"+strings.TrimPrefix(plain.URL, "http")+"/ws"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a plain web server should be refused with its status: %v", err)
	}
	silent := mattertest.New()
	defer silent.Close()
	silent.NoGreeting = true
	short, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := matter.Dial(short, silent.WSURL()); err == nil || !strings.Contains(err.Error(), "greeting") {
		t.Fatalf("a server that never greets should time out: %v", err)
	}
	if _, err := matter.Dial(ctx(t), "http://example.invalid/ws"); err == nil || !strings.Contains(err.Error(), "ws://") {
		t.Fatalf("an http URL should be refused: %v", err)
	}
}

func TestCommission(t *testing.T) {
	srv := mattertest.New()
	defer srv.Close()
	c, err := matter.Dial(ctx(t), srv.WSURL())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Commission(ctx(t), "34970112332", false); !matter.IsCode(err, matter.ErrCodeCommissionFailed) {
		t.Fatalf("bluetooth commissioning without bluetooth should fail: %v", err)
	}
	n, err := c.Commission(ctx(t), "34970112332", true)
	if err != nil || n.NodeID != 100 {
		t.Fatalf("network commissioning: %v %+v", err, n)
	}
	if srv.LastArgs["code"] != "34970112332" || srv.LastArgs["network_only"] != true {
		t.Fatalf("args sent: %v", srv.LastArgs)
	}
}

func TestInterpret(t *testing.T) {
	raw := func(s string) json.RawMessage { return json.RawMessage(s) }
	cases := []struct {
		kind  matter.Kind
		raw   string
		value float64
		text  string
	}{
		{matter.KindOnOff, "true", 1, "on"},
		{matter.KindOnOff, "false", 0, "off"},
		{matter.KindTemperature, "2150", 21.5, "21.5"},
		{matter.KindTemperature, "-325", -3.25, "-3.25"},
		{matter.KindHumidity, "4875", 48.75, "48.75"},
		{matter.KindContact, "true", 0, "shut"},
		{matter.KindContact, "false", 1, "open"},
		{matter.KindOccupancy, "1", 1, "occupied"},
		{matter.KindOccupancy, "6", 0, "clear"},
		{matter.KindIlluminance, "0", 0, "0"},
		{matter.KindIlluminance, "1", 1, "1"},
		{matter.KindIlluminance, "40001", 10000, "10000"},
		{matter.KindBattery, "180", 90, "90"},
		{matter.KindAttribute, "42", 42, "42"},
	}
	for _, c := range cases {
		got := matter.Interpret(c.kind, raw(c.raw), 1)
		if got.Missing || got.Value == nil || math.Abs(*got.Value-c.value) > 1e-6 || got.Text != c.text {
			t.Errorf("%s %s: got %+v (value %v), want %v %q", c.kind, c.raw, got, deref(got.Value), c.value, c.text)
		}
	}
	if !matter.Interpret(matter.KindTemperature, raw("null"), 1).Missing || !matter.Interpret(matter.KindBattery, nil, 1).Missing {
		t.Error("null and absent values should be missing")
	}
	if got := matter.Interpret(matter.KindAttribute, raw("7"), 0.5); *got.Value != 3.5 {
		t.Errorf("scale not applied: %v", *got.Value)
	}
	if got := matter.Interpret(matter.KindAttribute, raw(`"v1.2"`), 1); got.Value != nil || got.Text != "v1.2" {
		t.Errorf("text attribute: %+v", got)
	}
}

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestSummarize(t *testing.T) {
	n := sensor()
	b, _ := json.Marshal(map[string]any{"node_id": 7, "available": true, "attributes": n.Attributes})
	var data matter.NodeData
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	s := matter.Summarize(data)
	if s.Vendor != "Eve Systems" || s.Product != "Eve Weather" || s.Label != "Balcony" {
		t.Fatalf("names: %+v", s)
	}
	if len(s.Endpoints) != 3 {
		t.Fatalf("endpoints: %+v", s.Endpoints)
	}
	ep1 := s.Endpoints[1]
	if ep1.ID != 1 || len(ep1.DeviceTypes) != 1 || ep1.DeviceTypes[0] != "Temperature sensor" {
		t.Fatalf("endpoint 1: %+v", ep1)
	}
	if len(ep1.Readings) != 1 || ep1.Readings[0].Kind != matter.KindTemperature || ep1.Readings[0].Current != "21.5" {
		t.Fatalf("endpoint 1 readings: %+v", ep1.Readings)
	}
	if r := s.Endpoints[0].Readings; len(r) != 1 || r[0].Kind != matter.KindBattery || r[0].Current != "90" {
		t.Fatalf("battery on endpoint 0: %+v", r)
	}
}

func TestServerURL(t *testing.T) {
	cases := map[[2]string]string{
		{"", "192.168.1.20"}:                       "ws://192.168.1.20:5580/ws",
		{"", "http://hub.local:8123/"}:             "ws://hub.local:5580/ws",
		{"", "fd00::1"}:                            "ws://[fd00::1]:5580/ws",
		{"matter.lan", ""}:                         "ws://matter.lan:5580/ws",
		{"matter.lan:6000", ""}:                    "ws://matter.lan:6000/ws",
		{"wss://matter.example/api/ws", "ignored"}: "wss://matter.example/api/ws",
		{"", ""}: "",
	}
	for in, want := range cases {
		if got := matter.ServerURL(in[0], in[1]); got != want {
			t.Errorf("ServerURL(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
	for _, bad := range []string{"http://x/ws", "ws://", "ws://x:99999/ws"} {
		if matter.ValidateServerURL(bad) == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if err := matter.ValidateServerURL("ws://192.168.1.20:5580/ws"); err != nil {
		t.Error(err)
	}
}
