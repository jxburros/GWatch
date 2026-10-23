package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/matter/mattertest"
	"github.com/jxburros/GWatch/internal/store"
)

func matterFakeNode() mattertest.Node {
	return mattertest.Node{ID: 3, Available: true, IsBridge: true, Attributes: map[string]any{
		"0/40/1": "Acme", "0/40/3": "Bridge 2", "0/40/5": "Hall bridge",
		"0/29/0":   []any{map[string]any{"0": 22, "1": 1}},
		"1/29/0":   []any{map[string]any{"0": 14, "1": 1}},
		"2/29/0":   []any{map[string]any{"0": 770, "1": 1}, map[string]any{"0": 19, "1": 1}},
		"2/57/5":   "Hallway sensor",
		"2/1026/0": 1985,
		"2/47/12":  150,
	}}
}

func TestMatterNodesListsAndDescribes(t *testing.T) {
	fake := mattertest.New(matterFakeNode())
	defer fake.Close()
	ts, _ := newTestServer(t)
	admin := bootstrapAdmin(t, ts, "admin", "correct horse battery")

	var answer struct {
		Server     string `json:"server"`
		ServerInfo struct {
			SchemaVersion int `json:"schema_version"`
		} `json:"serverInfo"`
		Nodes []struct {
			NodeID    uint64 `json:"nodeId"`
			Available bool   `json:"available"`
			IsBridge  bool   `json:"isBridge"`
			Product   string `json:"product"`
			Endpoints []struct {
				ID          uint32   `json:"id"`
				Label       string   `json:"label"`
				DeviceTypes []string `json:"deviceTypes"`
				Readings    []struct {
					Kind    string `json:"kind"`
					Current string `json:"current"`
				} `json:"readings"`
			} `json:"endpoints"`
		} `json:"nodes"`
		Standards []json.RawMessage `json:"standards"`
	}
	code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/matter/nodes", map[string]string{"server": fake.WSURL()}, &answer)
	if code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	if answer.Server != fake.WSURL() || answer.ServerInfo.SchemaVersion != 11 || len(answer.Standards) != 7 {
		t.Fatalf("header: %+v", answer)
	}
	if len(answer.Nodes) != 1 || !answer.Nodes[0].IsBridge || answer.Nodes[0].Product != "Bridge 2" {
		t.Fatalf("nodes: %s", body)
	}
	ep := answer.Nodes[0].Endpoints[2]
	if ep.ID != 2 || ep.Label != "Hallway sensor" || len(ep.Readings) != 2 || ep.Readings[0].Kind != "temperature" || ep.Readings[0].Current != "19.85" {
		t.Fatalf("endpoint 2: %+v", ep)
	}
	if strings.Join(ep.DeviceTypes, ",") != "Temperature sensor,Bridged node" {
		t.Fatalf("device types: %v", ep.DeviceTypes)
	}

	// No address at all, and an address that is not a WebSocket.
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/matter/nodes", map[string]string{}, nil); code != 400 || !strings.Contains(body, "address") {
		t.Fatalf("no server: %d %s", code, body)
	}
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/matter/nodes", map[string]string{"server": "http://x/ws"}, nil); code != 400 || !strings.Contains(body, "ws://") {
		t.Fatalf("http server: %d %s", code, body)
	}
	// A sidecar that is not running.
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/matter/nodes", map[string]string{"server": "ws://127.0.0.1:1/ws"}, nil); code != 502 || !strings.Contains(body, "could not reach") {
		t.Fatalf("down server: %d %s", code, body)
	}
}

func TestMatterCommission(t *testing.T) {
	fake := mattertest.New()
	defer fake.Close()
	ts, srv := newTestServer(t)
	admin := bootstrapAdmin(t, ts, "admin", "correct horse battery")

	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/matter/commission", map[string]any{"server": fake.WSURL(), "code": "hello"}, nil); code != 400 || !strings.Contains(body, "pairing code") {
		t.Fatalf("bad code: %d %s", code, body)
	}
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/matter/commission", map[string]any{"server": fake.WSURL(), "code": "3497-011-2332", "networkOnly": false}, nil); code != 400 || !strings.Contains(body, "no Bluetooth") {
		t.Fatalf("bluetooth without an adapter: %d %s", code, body)
	}
	var answer struct {
		Node struct {
			NodeID  uint64 `json:"nodeId"`
			Product string `json:"product"`
		} `json:"node"`
	}
	code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/matter/commission", map[string]any{"server": fake.WSURL(), "code": "3497-011-2332"}, &answer)
	if code != 200 || answer.Node.NodeID != 100 || answer.Node.Product != "Smart Plug" {
		t.Fatalf("commission: %d %s", code, body)
	}
	if fake.LastArgs["code"] != "34970112332" || fake.LastArgs["network_only"] != true {
		t.Fatalf("the code should reach the sidecar without dashes, network-only: %v", fake.LastArgs)
	}
	events, err := srv.Store.ListEvents(context.Background(), store.EventFilter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Title == "Matter device commissioned" && strings.Contains(ev.Detail, "node 100") {
			found = true
		}
	}
	if !found {
		t.Fatalf("commissioning should be in the event log: %+v", events)
	}
}

func TestNormalizePairingCode(t *testing.T) {
	good := map[string]string{
		"34970112332":            "34970112332",
		"3497-011-2332":          "34970112332",
		" 3497 011 2332 ":        "34970112332",
		"749701123365521327694":  "749701123365521327694",
		"MT:Y.K9042C00KA0648G00": "MT:Y.K9042C00KA0648G00",
		"mt:Y.K9042C00KA0648G00": "MT:Y.K9042C00KA0648G00",
	}
	for in, want := range good {
		if got, err := normalizePairingCode(in); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "1234", "MT:", "34970112332; rm -rf /", "MT:abc"} {
		if _, err := normalizePairingCode(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

// Saving a Matter check through the API fills in a standard reading's name
// and unit, and the check runs against the sidecar like any other.
func TestMatterCheckSavedThroughAPI(t *testing.T) {
	fake := mattertest.New(matterFakeNode())
	defer fake.Close()
	ts, srv := newTestServer(t)
	admin := bootstrapAdmin(t, ts, "admin", "correct horse battery")
	node := map[string]any{"name": "Hall", "host": "", "enabled": true, "checks": []map[string]any{{
		"type": "matter", "name": "Matter", "enabled": true, "intervalSeconds": 60, "timeoutSeconds": 5,
		"config": map[string]any{"matterServer": " " + fake.WSURL() + " ", "matterNodeId": 3, "matterReadings": []map[string]any{
			{"endpoint": 2, "kind": "Temperature", "cluster": 99, "warnAbove": 25},
			{"endpoint": 2, "kind": "battery"},
		}},
	}}}
	var created nodeDoc
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/nodes", node, &created); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	stored, err := srv.Store.GetCheck(context.Background(), created.Checks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	r := stored.Config.MatterReadings
	if stored.Config.MatterServer != fake.WSURL() || r[0].Name != "Temperature" || r[0].Unit != "°C" || r[0].Kind != "temperature" || r[0].Cluster != 0 || r[1].Name != "Battery" {
		t.Fatalf("normalised: %+v", stored.Config)
	}
	if units := stored.MetricUnits(); units["Temperature"] != "°C" || units["Battery"] != "%" {
		t.Fatalf("units: %v", units)
	}

	// A node without readings, or without a node id, is refused with the
	// validator's words.
	bad := map[string]any{"name": "Bad", "enabled": true, "checks": []map[string]any{{
		"type": "matter", "name": "Matter", "intervalSeconds": 60, "config": map[string]any{"matterServer": fake.WSURL(), "matterReadings": []map[string]any{{"endpoint": 1, "kind": "onoff"}}},
	}}}
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/nodes", bad, nil); code != 400 || !strings.Contains(body, "Matter node") {
		t.Fatalf("no node id: %d %s", code, body)
	}
}
