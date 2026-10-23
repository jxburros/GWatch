package main

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// A remote collector, all the way round (with e2e_test.go's real GWatch):
// GWatch makes the agent a collector, a node is checked from it, the agent's
// check-in brings the check, the agent runs it with the real runner against a
// listener here, and the result it posts becomes the check's state in GWatch.
func TestRemoteCollectorEndToEnd(t *testing.T) {
	ts := gwatchServer(t)
	cfg, agent, _ := pairedAgent(t, ts)
	cfg.collector = true
	oldSpread, oldEvery := firstRunSpread, collectorSendEvery
	firstRunSpread, collectorSendEvery = 5*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { firstRunSpread, collectorSendEvery = oldSpread, oldEvery })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	id := agent.ID
	var node model.Node
	if code := adminCall(t, ts, "POST", "/api/nodes", model.Node{Name: "Branch NAS", Host: "127.0.0.1", Enabled: true, CollectorID: &id,
		Checks: []model.Check{{Type: model.CheckTCP, Name: "SSH", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 2,
			Config: model.CheckConfig{Port: ln.Addr().(*net.TCPAddr).Port}}}}, &node); code != 201 && code != 200 {
		t.Fatalf("create node: %d", code)
	}
	if code := adminCall(t, ts, "PUT", fmt.Sprintf("/api/agents/%d/collector", id), map[string]any{"enabled": true}, nil); code != 200 {
		t.Fatalf("turn collecting on: %d", code)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := newClient(cfg)
	rt := newRuntime(cfg)
	rt.collector = newCollectorRunner(ctx, cfg, rt, client)
	go rt.collector.sendLoop(ctx)
	p := &program{cfg: cfg, mode: "run"}
	if _, err := p.checkInOnce(ctx, rt, client); err != nil {
		t.Fatal(err)
	}
	if st := rt.collector.status(); st.Checks != 1 {
		t.Fatalf("the agent should be running the node's one check: %+v", st)
	}
	check := node.Checks[0].ID
	deadline := time.Now().Add(15 * time.Second)
	for {
		var state model.CheckState
		adminCall(t, ts, "GET", fmt.Sprintf("/api/checks/%d/state", check), nil, &state)
		if state.Status == model.StatusUp {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the collector's result never reached GWatch: %+v", state)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if a := agentNamed(t, ts, id); a.CollectorStatus != model.CollectorRunning || a.CollectorChecks != 1 {
		t.Fatalf("the agent card should show a running collector with one check: %+v", a)
	}
}
