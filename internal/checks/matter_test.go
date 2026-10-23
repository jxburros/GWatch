package checks

import (
	"context"
	"math"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/matter/mattertest"
	"github.com/jxburros/GWatch/internal/model"
)

// A bridge exposing a temperature/humidity sensor, a smart plug, a door
// contact, a motion sensor with a light sensor and a battery, laid out the
// way python-matter-server caches it.
func fakeBridge() mattertest.Node {
	return mattertest.Node{ID: 12, Available: true, IsBridge: true, Attributes: map[string]any{
		"0/40/1":   "Acme",
		"0/40/3":   "Hub",
		"0/29/0":   []any{map[string]any{"0": 22, "1": 1}},
		"1/29/0":   []any{map[string]any{"0": 14, "1": 1}},
		"2/57/5":   "Living room",
		"2/1026/0": 2137,
		"2/1029/0": 4410,
		"3/6/0":    true,
		"4/69/0":   false,
		"5/1030/0": 1,
		"5/1024/0": 30001,
		"5/47/12":  37,
		"6/1026/0": nil,
		"7/8/0":    127,
	}}
}

func matterCheck(server string, readings ...model.MatterReading) model.Check {
	return model.Check{ID: 0, Type: model.CheckMatter, Name: "Matter", Enabled: true, TimeoutSeconds: 5,
		Config: model.CheckConfig{MatterServer: server, MatterNodeID: 12, MatterReadings: readings}}
}

func TestMatterCheckReadsStandardClusters(t *testing.T) {
	srv := mattertest.New(fakeBridge())
	defer srv.Close()
	check := matterCheck(srv.WSURL(),
		model.MatterReading{Endpoint: 2, Kind: "temperature", Name: "Living room temperature", Unit: "°C"},
		model.MatterReading{Endpoint: 2, Kind: "humidity", Name: "Living room humidity", Unit: "%"},
		model.MatterReading{Endpoint: 3, Kind: "onoff", Name: "Heater plug"},
		model.MatterReading{Endpoint: 4, Kind: "contact", Name: "Front door open"},
		model.MatterReading{Endpoint: 5, Kind: "occupancy", Name: "Hall motion"},
		model.MatterReading{Endpoint: 5, Kind: "illuminance", Name: "Hall light", Unit: "lx"},
		model.MatterReading{Endpoint: 5, Kind: "battery", Name: "Hall battery", Unit: "%"},
		model.MatterReading{Endpoint: 7, Kind: "attribute", Cluster: 8, Attribute: 0, Scale: 100.0 / 254, Name: "Lamp level", Unit: "%"},
	)
	if err := Validate(check, ""); err != nil {
		t.Fatalf("validate: %v", err)
	}
	res := Run(context.Background(), check, Options{})
	if res.Status != model.StatusUp {
		t.Fatalf("status %s: %s", res.Status, res.Message)
	}
	want := map[string]float64{
		"Living room temperature": 21.37, "Living room humidity": 44.1, "Heater plug": 1,
		"Front door open": 1, "Hall motion": 1, "Hall light": 1000, "Hall battery": 18.5, "Lamp level": 50,
	}
	for name, v := range want {
		got, ok := res.Metrics[name]
		if !ok || math.Abs(got-v) > 0.01 {
			t.Errorf("%s = %v (present %v), want %v", name, got, ok, v)
		}
	}
	if len(res.Details.Matter) != 8 || res.Details.Matter[3].Raw != "open" || res.Details.Matter[2].Raw != "on" {
		t.Fatalf("details: %+v", res.Details.Matter)
	}
	if res.Details.MatterServer != srv.WSURL() || res.LatencyMS == nil {
		t.Fatalf("server or latency missing: %+v", res.Details)
	}
	if !strings.Contains(res.Message, "8 readings read from node 12") || !strings.Contains(res.Message, "Living room temperature 21.37 °C") {
		t.Fatalf("message: %q", res.Message)
	}
	// One read for every attribute, not one per reading.
	if log := srv.CommandLog(); len(log) != 1 || log[0] != "read_attribute" {
		t.Fatalf("commands: %v", log)
	}
}

func TestMatterCheckThresholds(t *testing.T) {
	srv := mattertest.New(fakeBridge())
	defer srv.Close()
	f := model.Float
	warm := matterCheck(srv.WSURL(), model.MatterReading{Endpoint: 2, Kind: "temperature", Name: "Temp", Unit: "°C", WarnAbove: f(21), CritAbove: f(30)})
	res := Run(context.Background(), warm, Options{})
	if res.Status != model.StatusDegraded || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "above the warning threshold of 21 °C") {
		t.Fatalf("warning: %s %v", res.Status, res.Warnings)
	}
	door := matterCheck(srv.WSURL(), model.MatterReading{Endpoint: 4, Kind: "contact", Name: "Door open", CritAbove: f(0)})
	res = Run(context.Background(), door, Options{})
	if res.Status != model.StatusDown || !strings.Contains(res.Message, "Door open (4/69/0) is 1, above the critical threshold of 0") {
		t.Fatalf("door: %s %q", res.Status, res.Message)
	}
	srv.SetAttribute(12, "4/69/0", true)
	if res = Run(context.Background(), door, Options{}); res.Status != model.StatusUp {
		t.Fatalf("a shut door should be up: %s %q", res.Status, res.Message)
	}
	battery := matterCheck(srv.WSURL(), model.MatterReading{Endpoint: 5, Kind: "battery", Name: "Battery", Unit: "%", WarnBelow: f(25), CritBelow: f(10)})
	if res = Run(context.Background(), battery, Options{}); res.Status != model.StatusDegraded {
		t.Fatalf("battery at 18.5%%: %s %q", res.Status, res.Message)
	}
}

func TestMatterCheckUnavailableNodeIsDown(t *testing.T) {
	srv := mattertest.New(fakeBridge())
	defer srv.Close()
	check := matterCheck(srv.WSURL(), model.MatterReading{Endpoint: 2, Kind: "temperature", Name: "Temp"})
	srv.SetAvailable(12, false)
	res := Run(context.Background(), check, Options{})
	if res.Status != model.StatusDown || !strings.Contains(res.Message, "node 12 is unavailable") {
		t.Fatalf("unavailable: %s %q", res.Status, res.Message)
	}
	check.Config.MatterNodeID = 99
	res = Run(context.Background(), check, Options{})
	if res.Status != model.StatusDown || !strings.Contains(res.Message, "no node 99") {
		t.Fatalf("missing node: %s %q", res.Status, res.Message)
	}
}

func TestMatterCheckMissingAndNullValues(t *testing.T) {
	srv := mattertest.New(fakeBridge())
	defer srv.Close()
	check := matterCheck(srv.WSURL(), model.MatterReading{Endpoint: 6, Kind: "temperature", Name: "Loose probe"})
	res := Run(context.Background(), check, Options{})
	if res.Status != model.StatusDegraded || !strings.Contains(strings.Join(res.Warnings, ";"), "no value right now") {
		t.Fatalf("null value should warn: %s %v", res.Status, res.Warnings)
	}
	check = matterCheck(srv.WSURL(), model.MatterReading{Endpoint: 9, Kind: "humidity", Name: "Nowhere"})
	res = Run(context.Background(), check, Options{})
	if res.Status != model.StatusDown || !strings.Contains(res.Message, "not available on node 12") {
		t.Fatalf("absent attribute should be down: %s %q", res.Status, res.Message)
	}
}

func TestMatterCheckServerDown(t *testing.T) {
	// A port nothing listens on: take one and give it back.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	check := matterCheck("ws://"+addr+"/ws", model.MatterReading{Endpoint: 1, Kind: "onoff", Name: "Plug"})
	check.TimeoutSeconds = 2
	res := Run(context.Background(), check, Options{})
	if res.Status != model.StatusDown || !strings.Contains(res.Message, "Matter server at ws://"+addr) {
		t.Fatalf("server down: %s %q", res.Status, res.Message)
	}

	silent := mattertest.New()
	defer silent.Close()
	silent.NoGreeting = true
	check = matterCheck(silent.WSURL(), model.MatterReading{Endpoint: 1, Kind: "onoff", Name: "Plug"})
	check.TimeoutSeconds = 1
	start := time.Now()
	res = Run(context.Background(), check, Options{})
	// Which deadline fires first — the wait for the greeting or the run's own
	// timeout — depends on how busy the machine is; either way it is down,
	// and quickly.
	if res.Status != model.StatusDown || !(strings.Contains(res.Message, "did not answer within") || strings.Contains(res.Message, "timed out after")) || time.Since(start) > 4*time.Second {
		t.Fatalf("silent server: %s %q after %s", res.Status, res.Message, time.Since(start))
	}
}

// A blank server address means the node's host on the sidecar's default
// port and path.
func TestMatterCheckDefaultsToNodeHost(t *testing.T) {
	check := matterCheck("", model.MatterReading{Endpoint: 1, Kind: "onoff", Name: "Plug"})
	if err := Validate(check, ""); err == nil || !strings.Contains(err.Error(), "Matter server address") {
		t.Fatalf("no address anywhere should be refused: %v", err)
	}
	if err := Validate(check, "192.168.1.20"); err != nil {
		t.Fatalf("the node host should stand in: %v", err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	res := Run(context.Background(), check, Options{NodeHost: "127.0.0.1"})
	if !strings.Contains(res.Message, "ws://127.0.0.1:5580/ws") {
		t.Fatalf("expected the default address in the message: %q", res.Message)
	}
}

func TestValidateMatterCheck(t *testing.T) {
	f := model.Float
	ok := model.MatterReading{Endpoint: 1, Kind: "onoff", Name: "Plug"}
	cases := map[string]model.CheckConfig{
		"http scheme":      {MatterServer: "http://hub:5580/ws", MatterNodeID: 1, MatterReadings: []model.MatterReading{ok}},
		"no node":          {MatterServer: "ws://hub:5580/ws", MatterReadings: []model.MatterReading{ok}},
		"no readings":      {MatterServer: "ws://hub:5580/ws", MatterNodeID: 1},
		"unknown kind":     {MatterServer: "ws://hub:5580/ws", MatterNodeID: 1, MatterReadings: []model.MatterReading{{Endpoint: 1, Kind: "colour", Name: "x"}}},
		"no cluster":       {MatterServer: "ws://hub:5580/ws", MatterNodeID: 1, MatterReadings: []model.MatterReading{{Endpoint: 1, Kind: "attribute", Name: "x"}}},
		"no name":          {MatterServer: "ws://hub:5580/ws", MatterNodeID: 1, MatterReadings: []model.MatterReading{{Endpoint: 1, Kind: "onoff"}}},
		"duplicate names":  {MatterServer: "ws://hub:5580/ws", MatterNodeID: 1, MatterReadings: []model.MatterReading{ok, {Endpoint: 2, Kind: "onoff", Name: "plug"}}},
		"inverted above":   {MatterServer: "ws://hub:5580/ws", MatterNodeID: 1, MatterReadings: []model.MatterReading{{Endpoint: 1, Kind: "temperature", Name: "t", WarnAbove: f(30), CritAbove: f(20)}}},
		"inverted below":   {MatterServer: "ws://hub:5580/ws", MatterNodeID: 1, MatterReadings: []model.MatterReading{{Endpoint: 1, Kind: "battery", Name: "b", WarnBelow: f(10), CritBelow: f(20)}}},
		"negative scale":   {MatterServer: "ws://hub:5580/ws", MatterNodeID: 1, MatterReadings: []model.MatterReading{{Endpoint: 1, Kind: "attribute", Cluster: 8, Scale: -1, Name: "l"}}},
		"too many reading": {MatterServer: "ws://hub:5580/ws", MatterNodeID: 1, MatterReadings: make([]model.MatterReading, maxMatterReadings+1)},
	}
	for name, cfg := range cases {
		c := model.Check{Type: model.CheckMatter, Name: "m", Config: cfg}
		if err := Validate(c, ""); err == nil {
			t.Errorf("%s: should be refused", name)
		}
	}
	good := model.Check{Type: model.CheckMatter, Name: "m", Config: model.CheckConfig{MatterServer: "ws://hub:5580/ws", MatterNodeID: 1, MatterReadings: []model.MatterReading{ok}}}
	if err := Validate(good, ""); err != nil {
		t.Fatal(err)
	}
}

func TestMatterMetricUnits(t *testing.T) {
	c := matterCheck("ws://x/ws", model.MatterReading{Endpoint: 1, Kind: "temperature", Name: "Temp", Unit: "°C"}, model.MatterReading{Endpoint: 1, Kind: "onoff", Name: "Plug"})
	units := c.MetricUnits()
	if units["Temp"] != "°C" || len(units) != 2 {
		t.Fatalf("units: %v", units)
	}
}
