package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

func TestAgentLifecycle(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	node, err := s.CreateNode(ctx, model.Node{Name: "NAS", Host: "10.0.0.5", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.CreateAgent(ctx, "  nas  ", &node.ID, "gwa_abcd", "hash-1", "pat (admin)")
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "nas" {
		t.Errorf("name should be trimmed, got %q", a.Name)
	}
	if a.NodeID == nil || *a.NodeID != node.ID || !a.Enabled || a.Revoked() {
		t.Fatalf("unexpected agent: %+v", a)
	}
	if a.HostKey() != model.AgentHostKey(a.ID) {
		t.Errorf("host key: got %q", a.HostKey())
	}

	// Two machines cannot share a token.
	if _, err := s.CreateAgent(ctx, "other", nil, "gwa_abcd", "hash-1", ""); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("want ErrDuplicate for a repeated token, got %v", err)
	}
	if _, err := s.CreateAgent(ctx, "  ", nil, "p", "hash-2", ""); err == nil {
		t.Error("a nameless agent should be refused")
	}

	found, err := s.AgentByTokenHash(ctx, "hash-1")
	if err != nil || found.ID != a.ID {
		t.Fatalf("lookup by token: %+v %v", found, err)
	}

	if err := s.TouchAgent(ctx, a.ID, "10.0.0.5:51000", "1.4.0", "nas.local", "linux", "arm64"); err != nil {
		t.Fatal(err)
	}
	a, _ = s.GetAgent(ctx, a.ID)
	if a.LastSeenAt == nil || a.LastAddr != "10.0.0.5:51000" || a.Hostname != "nas.local" || a.Arch != "arm64" {
		t.Fatalf("touch did not record the report: %+v", a)
	}

	// A disabled agent's token stops resolving immediately.
	if _, err := s.UpdateAgent(ctx, a.ID, "nas", &node.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AgentByTokenHash(ctx, "hash-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a disabled agent's token must not resolve, got %v", err)
	}
	if _, err := s.UpdateAgent(ctx, a.ID, "nas", &node.ID, true); err != nil {
		t.Fatal(err)
	}

	// So does a revoked one, and the row stays for the audit trail.
	if err := s.RevokeAgent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AgentByTokenHash(ctx, "hash-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a revoked agent's token must not resolve, got %v", err)
	}
	a, err = s.GetAgent(ctx, a.ID)
	if err != nil || !a.Revoked() {
		t.Fatalf("the revoked agent should still be readable: %+v %v", a, err)
	}
	// Revoking twice is not an error, so a retry does not fail.
	if err := s.RevokeAgent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeAgent(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for an unknown agent, got %v", err)
	}
}

// A machine's readings are its hardware check's results, which belong to the
// node. Deleting the agent removes the registration (its token and how it was
// enrolled) and leaves the node and its history, which go with the node.
func TestDeleteAgentKeepsTheNodesHistory(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	node, check := hardwareNode(t, s, model.HostSourceAgent, 0)
	a, err := s.CreateAgent(ctx, "nas", &node.ID, "p", "hash", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertResult(ctx, hardwareResult(check.ID, time.Now(), 10)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAgent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAgent(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("agent should be gone, got %v", err)
	}
	if got, _ := s.RecentResults(ctx, check.ID, 10); len(got) != 1 {
		t.Errorf("the node's history should stay with the node, got %d results", len(got))
	}
	if err := s.DeleteAgent(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound deleting twice, got %v", err)
	}
}

// hardwareNode makes a node with one hardware check.
func hardwareNode(t *testing.T, s *Store, source model.HostSource, agentID int64) (model.Node, model.Check) {
	t.Helper()
	cfg := model.SystemDefaults()
	cfg.HostSource, cfg.AgentID = source, agentID
	node, err := s.CreateNode(context.Background(), model.Node{Name: "nas", Enabled: true, Checks: []model.Check{{
		Type: model.CheckSystem, Name: "Hardware health", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 10,
		Retries: 1, FailureThreshold: 2, Config: cfg,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return node, node.Checks[0]
}

// hardwareResult is what a hardware check records for one reading: the metrics
// by key, and the whole snapshot in its details.
func hardwareResult(checkID int64, ts time.Time, cpu float64) model.Result {
	m := model.HostMetrics{
		Key: "agent:1", Hostname: "nas.local", Timestamp: ts,
		CPU:    model.HostCPU{Cores: 4, UsagePct: &cpu},
		Memory: model.HostMemory{TotalBytes: 100, UsedBytes: 40, UsedPct: 40},
	}
	return model.Result{
		CheckID: checkID, Timestamp: ts, Success: true, Status: model.StatusUp, Attempts: 1,
		Metrics: map[string]float64{
			"cpu": cpu, "memory": 40, "swap": 5, "load": 0.5,
			"disk:/": 60, "disk:/data": 85,
			"net:eth0.rx": 100, "net:eth0.tx": 10, "net:wlan0.rx": 50, "net:wlan0.tx": 5,
			"diskio:sda.read": 7, "diskio:sda.write": 3, "diskio:sda.busy": 90,
		},
		Details: model.ResultDetails{Host: &m},
	}
}

func TestHostSeriesIsBuiltFromTheChecksResults(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, check := hardwareNode(t, s, model.HostSourceAgent, 1)
	base := time.Now().Add(-10 * time.Minute).Truncate(time.Second)
	for i, cpu := range []float64{10, 20, 30} {
		if _, err := s.InsertResult(ctx, hardwareResult(check.ID, base.Add(time.Duration(i)*time.Minute), cpu)); err != nil {
			t.Fatal(err)
		}
	}
	// A failed run has no metrics and no snapshot; it is a gap, not a zero.
	if _, err := s.InsertResult(ctx, model.Result{CheckID: check.ID, Timestamp: base.Add(5 * time.Minute), Status: model.StatusDown, Attempts: 1}); err != nil {
		t.Fatal(err)
	}

	series, err := s.HostSeries(ctx, []int64{check.ID}, base.Add(-time.Minute), base.Add(10*time.Minute), 600)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 3 {
		t.Fatalf("want one point per successful run, got %d: %+v", len(series), series)
	}
	if *series[0].CPUPct != 10 || *series[2].CPUPct != 30 || !series[0].Timestamp.Before(series[1].Timestamp) {
		t.Errorf("series out of order or wrong: %+v", series)
	}
	p := series[0]
	// The charts draw the fullest disk and the totals of throughput, derived
	// from the per-instance metrics the check keeps.
	if p.DiskPct == nil || *p.DiskPct != 85 {
		t.Errorf("disk should be the fullest filesystem: %v", p.DiskPct)
	}
	if *p.NetRxBytesSec != 150 || *p.NetTxBytesSec != 15 || *p.DiskReadBytes != 7 || *p.DiskWriteBytes != 3 {
		t.Errorf("throughput totals: rx=%v tx=%v read=%v write=%v", *p.NetRxBytesSec, *p.NetTxBytesSec, *p.DiskReadBytes, *p.DiskWriteBytes)
	}
	if *p.MemPct != 40 || *p.SwapPct != 5 || *p.LoadPerCore != 0.5 {
		t.Errorf("single-instance metrics: %+v", p)
	}
	// A chart wants numbers, not snapshots.
	if p.Metrics.Hostname != "" {
		t.Error("the series should not carry full snapshots")
	}
}

func TestHostSeriesAveragesIntoBucketsAndKeepsMissingMissing(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, check := hardwareNode(t, s, model.HostSourceAgent, 1)
	base := time.Now().Add(-time.Hour).Truncate(time.Second)
	for i := 0; i < 60; i++ {
		r := hardwareResult(check.ID, base.Add(time.Duration(i)*time.Minute), float64(i))
		if i%2 == 1 {
			delete(r.Metrics, "swap") // half the readings have no swap figure
		}
		if _, err := s.InsertResult(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	series, err := s.HostSeries(ctx, []int64{check.ID}, base.Add(-time.Second), base.Add(time.Hour), 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 6 {
		t.Fatalf("sixty readings in six buckets: got %d points", len(series))
	}
	// Averaging keeps a spike visible; a bucket's CPU is the mean of its ten.
	total := 0.0
	for _, p := range series {
		total += *p.CPUPct
	}
	if total/6 < 28 || total/6 > 31 {
		t.Errorf("mean of bucket means = %v, want about 29.5", total/6)
	}
	for _, p := range series {
		if p.SwapPct == nil || *p.SwapPct != 5 {
			t.Errorf("a metric missing from some readings is averaged over the ones that have it, not over zero: %v", p.SwapPct)
		}
	}
}

func TestLatestHostFromResultsSkipsRunsWithNoSnapshot(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, check := hardwareNode(t, s, model.HostSourceAgent, 1)
	_, other := hardwareNode(t, s, model.HostSourceAgent, 2)
	now := time.Now()
	for _, r := range []model.Result{
		hardwareResult(check.ID, now.Add(-30*time.Minute), 10),
		hardwareResult(check.ID, now.Add(-20*time.Minute), 77),
		// The machine went quiet: the check has been failing since, with nothing
		// to say about what the machine looked like.
		{CheckID: check.ID, Timestamp: now.Add(-10 * time.Minute), Status: model.StatusDown, Attempts: 1},
		{CheckID: check.ID, Timestamp: now.Add(-5 * time.Minute), Status: model.StatusDown, Attempts: 1},
	} {
		if _, err := s.InsertResult(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	m, ok, err := s.LatestHostFromResults(ctx, []int64{check.ID, other.ID})
	if err != nil || !ok {
		t.Fatalf("got %v %v", ok, err)
	}
	if m.CPU.UsagePct == nil || *m.CPU.UsagePct != 77 || m.Hostname != "nas.local" {
		t.Errorf("the newest reading that has a snapshot: %+v", m)
	}
	if _, ok, _ := s.LatestHostFromResults(ctx, []int64{other.ID}); ok {
		t.Error("a check that recorded nothing has no reading")
	}
}

// A hardware result is a whole snapshot, so hardware checks keep theirs for the
// "hardware readings" retention and every other check keeps its own for the raw
// one.
func TestHardwareResultsHaveARetentionOfTheirOwn(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, hw := hardwareNode(t, s, model.HostSourceAgent, 1)
	pingNode, err := s.CreateNode(ctx, model.Node{Name: "router", Host: "192.168.1.1", Enabled: true, Checks: []model.Check{{
		Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Retries: 1, FailureThreshold: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	ping := pingNode.Checks[0]
	old := time.Now().Add(-50 * 24 * time.Hour)
	for _, id := range []int64{hw.ID, ping.ID} {
		if _, err := s.InsertResult(ctx, model.Result{CheckID: id, Timestamp: old, Success: true, Status: model.StatusUp, Attempts: 1}); err != nil {
			t.Fatal(err)
		}
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	if n, err := s.DeleteResultsBeforeByKind(ctx, cutoff, false); err != nil || n != 1 {
		t.Fatalf("pruning ordinary checks at 30 days: %d %v", n, err)
	}
	if got, _ := s.RecentResults(ctx, hw.ID, 5); len(got) != 1 {
		t.Error("the hardware result is kept: its retention is not the ordinary one")
	}
	if got, _ := s.RecentResults(ctx, ping.ID, 5); len(got) != 0 {
		t.Error("the ordinary result should have been pruned")
	}
	if n, err := s.DeleteResultsBeforeByKind(ctx, cutoff, true); err != nil || n != 1 {
		t.Fatalf("pruning hardware at 30 days: %d %v", n, err)
	}
}

func TestCountResultsNear(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	_, check := hardwareNode(t, s, model.HostSourceAgent, 1)
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	if _, err := s.InsertResult(ctx, hardwareResult(check.ID, at, 10)); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountResultsNear(ctx, check.ID, at.Add(40*time.Second), 48*time.Second); n != 1 {
		t.Errorf("within the window: %d", n)
	}
	if n, _ := s.CountResultsNear(ctx, check.ID, at.Add(5*time.Minute), 48*time.Second); n != 0 {
		t.Errorf("outside the window: %d", n)
	}
}

// The table readings used to live in is gone, and the migration that drops it
// is safe on a database that never had one.
func TestHostSamplesTableIsRetired(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	var n int
	if err := s.queryRow(ctx, "SELECT COUNT(*) FROM host_samples").Scan(&n); err == nil {
		t.Fatal("host_samples should not exist: a machine's readings are its check's results")
	}
	if err := s.writeTx(ctx, func(tx *wtx) error { return migrateRetireHostSamples(ctx, tx) }); err != nil {
		t.Fatalf("dropping a table that is not there should be fine: %v", err)
	}
}

func TestPairingCodeLifecycle(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Now()

	node, err := s.CreateNode(ctx, model.Node{Name: "NAS", Host: "10.0.0.5", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreatePairingCode(ctx, "  nas  ", &node.ID, "hash-1", "pat (admin)", now.Add(15*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "nas" {
		t.Errorf("name should be trimmed, got %q", p.Name)
	}
	if !p.Pending(now) || p.NodeID == nil || *p.NodeID != node.ID {
		t.Fatalf("unexpected pairing: %+v", p)
	}
	if _, err := s.CreatePairingCode(ctx, "other", nil, "hash-1", "", now.Add(time.Minute)); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("two invitations cannot share a code, got %v", err)
	}
	if _, err := s.CreatePairingCode(ctx, " ", nil, "hash-2", "", now.Add(time.Minute)); err == nil {
		t.Error("a nameless invitation should be refused")
	}

	redeemed, stored, err := s.RedeemPairingCode(ctx, "hash-1", now, "192.168.1.9")
	if err != nil {
		t.Fatal(err)
	}
	if stored != "hash-1" {
		t.Errorf("the stored digest should come back for the constant-time check, got %q", stored)
	}
	if !redeemed.Redeemed() || redeemed.RedeemedAddr != "192.168.1.9" || redeemed.Name != "nas" {
		t.Fatalf("unexpected redemption: %+v", redeemed)
	}

	// Single use is the whole point: the second machine to try is turned away
	// exactly as if the code had never existed.
	if _, _, err := s.RedeemPairingCode(ctx, "hash-1", now, "192.168.1.10"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a spent code must not be redeemable again, got %v", err)
	}
	// And so is a code nobody ever minted.
	if _, _, err := s.RedeemPairingCode(ctx, "hash-nope", now, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown code should be ErrNotFound, got %v", err)
	}

	agent, err := s.CreateAgent(ctx, "nas", &node.ID, "gwa_abcd", "token-hash", "pairing code")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AttachPairingAgent(ctx, redeemed.ID, agent.ID); err != nil {
		t.Fatal(err)
	}
	again, err := s.GetPairingCode(ctx, redeemed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.AgentID == nil || *again.AgentID != agent.ID {
		t.Fatalf("the invitation should remember the machine it produced: %+v", again)
	}

	// Cancelling afterwards must not rewrite the record of an enrolment that
	// already happened.
	if err := s.RevokePairingCode(ctx, redeemed.ID); err != nil {
		t.Fatal(err)
	}
	if after, _ := s.GetPairingCode(ctx, redeemed.ID); after.Revoked() {
		t.Error("a redeemed invitation should not be marked cancelled")
	}
}

func TestPairingCodeExpires(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Now()

	p, err := s.CreatePairingCode(ctx, "nas", nil, "hash-exp", "", now.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if p.Pending(now) {
		t.Error("a code whose time has passed is not pending")
	}
	if _, _, err := s.RedeemPairingCode(ctx, "hash-exp", now, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an expired code must not be redeemable, got %v", err)
	}
	// Expiry is against the clock the caller passes, so the boundary is the
	// instant itself: a code is dead at its expiry time, not a moment after.
	live, err := s.CreatePairingCode(ctx, "nas", nil, "hash-live", "", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if live.Pending(live.ExpiresAt) {
		t.Error("a code should be expired at exactly its expiry time")
	}
	if _, _, err := s.RedeemPairingCode(ctx, "hash-live", now.Add(2*time.Minute), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("redemption should honour the clock it is given, got %v", err)
	}
}

func TestPairingCodeRevocation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Now()

	p, err := s.CreatePairingCode(ctx, "nas", nil, "hash-rev", "", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokePairingCode(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetPairingCode(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Revoked() || after.Pending(now) {
		t.Fatalf("a cancelled invitation should be cancelled: %+v", after)
	}
	if _, _, err := s.RedeemPairingCode(ctx, "hash-rev", now, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a cancelled code must not be redeemable, got %v", err)
	}
	// Cancelling twice is not an error; cancelling something that is not there
	// is.
	if err := s.RevokePairingCode(ctx, p.ID); err != nil {
		t.Errorf("cancelling twice should be harmless, got %v", err)
	}
	if err := s.RevokePairingCode(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("cancelling a code that does not exist should say so, got %v", err)
	}
}

// Two machines given the same code at the same moment is the race the single
// writer and the conditional update exist to settle. Exactly one may win.
func TestConcurrentRedemptionYieldsOneWinner(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Now()
	if _, err := s.CreatePairingCode(ctx, "nas", nil, "hash-race", "", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	const racers = 8
	start := make(chan struct{})
	results := make(chan error, racers)
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, err := s.RedeemPairingCode(ctx, "hash-race", time.Now(), fmt.Sprintf("10.0.0.%d", i))
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)

	won := 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, ErrNotFound):
		default:
			t.Fatalf("unexpected error from a racing redemption: %v", err)
		}
	}
	if won != 1 {
		t.Fatalf("%d of %d racers redeemed the same code; it must be exactly one", won, racers)
	}
}

func TestListPairingCodesNewestFirst(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	for _, n := range []string{"one", "two", "three"} {
		if _, err := s.CreatePairingCode(ctx, n, nil, "hash-"+n, "", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.ListPairingCodes(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Name != "three" {
		t.Fatalf("unexpected list: %+v", list)
	}
	// The code itself is never in the list; only its hash was ever stored.
	blob := fmt.Sprintf("%+v", list)
	if strings.Contains(blob, "hash-three") {
		t.Error("the list leaked a code digest")
	}
}
