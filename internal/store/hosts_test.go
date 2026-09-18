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

// Deleting a machine must take its readings with it: they are the record of
// what that machine was doing and have no meaning without it.
func TestDeleteAgentRemovesItsReadings(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	a, err := s.CreateAgent(ctx, "nas", nil, "p", "hash", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveHostSample(ctx, sampleAt(a.HostKey(), time.Now(), 10)); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAgent(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAgent(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("agent should be gone, got %v", err)
	}
	if _, err := s.LatestHostSample(ctx, a.HostKey()); !errors.Is(err, ErrNotFound) {
		t.Errorf("readings should be gone, got %v", err)
	}
	if err := s.DeleteAgent(ctx, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound deleting twice, got %v", err)
	}
}

func sampleAt(key string, ts time.Time, cpu float64) model.HostSample {
	return model.HostSample{
		Key:       key,
		Timestamp: ts,
		CPUPct:    &cpu,
		Metrics: model.HostMetrics{
			Key: key, Hostname: "nas.local", Timestamp: ts,
			CPU:    model.HostCPU{Cores: 4, UsagePct: &cpu},
			Memory: model.HostMemory{TotalBytes: 100, UsedBytes: 40, UsedPct: 40},
		},
	}
}

func TestHostSamplesRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Second)

	for i, cpu := range []float64{10, 20, 30} {
		if err := s.SaveHostSample(ctx, sampleAt("local", base.Add(time.Duration(i)*time.Minute), cpu)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveHostSample(ctx, sampleAt("agent:1", base, 99)); err != nil {
		t.Fatal(err)
	}

	latest, err := s.LatestHostSample(ctx, "local")
	if err != nil {
		t.Fatal(err)
	}
	if latest.CPUPct == nil || *latest.CPUPct != 30 {
		t.Fatalf("latest reading: %+v", latest)
	}
	// The full snapshot comes back with the newest reading, because that is
	// what the inspector shows.
	if latest.Metrics.Hostname != "nas.local" || latest.Metrics.CPU.Cores != 4 {
		t.Errorf("snapshot not preserved: %+v", latest.Metrics)
	}

	all, err := s.LatestHostSamples(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 || *all["local"].CPUPct != 30 || *all["agent:1"].CPUPct != 99 {
		t.Fatalf("latest per machine: %+v", all)
	}

	series, err := s.HostSamples(ctx, "local", base, base.Add(90*time.Second), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 2 {
		t.Fatalf("want the two readings inside the window, got %d", len(series))
	}
	if *series[0].CPUPct != 10 || *series[1].CPUPct != 20 {
		t.Errorf("series is not in time order: %+v", series)
	}
	// A chart wants the numbers, not several thousand decoded snapshots.
	if series[0].Metrics.Hostname != "" {
		t.Error("the series should not carry full snapshots")
	}

	keys, err := s.HostKeys(ctx)
	if err != nil || len(keys) != 2 || keys[0] != "agent:1" || keys[1] != "local" {
		t.Fatalf("host keys: %v %v", keys, err)
	}
}

// An agent that retries a push must not leave two points where there was one.
func TestSaveHostSampleReplacesSameTimestamp(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	ts := time.Now().Truncate(time.Millisecond)

	if err := s.SaveHostSample(ctx, sampleAt("local", ts, 10)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveHostSample(ctx, sampleAt("local", ts, 55)); err != nil {
		t.Fatal(err)
	}
	series, err := s.HostSamples(ctx, "local", ts.Add(-time.Minute), ts.Add(time.Minute), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(series) != 1 {
		t.Fatalf("want one reading, got %d", len(series))
	}
	if *series[0].CPUPct != 55 {
		t.Errorf("the retry should have replaced the reading, got %v", *series[0].CPUPct)
	}
}

func TestPruneAndDeleteHostSamples(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	now := time.Now()

	for _, age := range []time.Duration{0, 24 * time.Hour, 72 * time.Hour} {
		if err := s.SaveHostSample(ctx, sampleAt("local", now.Add(-age), 10)); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.PruneHostSamples(ctx, now.Add(-48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 reading pruned, got %d", n)
	}
	if err := s.DeleteHostSamples(ctx, "local"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := s.HostKeys(ctx); len(keys) != 0 {
		t.Fatalf("want no readings left, got %v", keys)
	}
}

func TestSaveHostSampleRequiresAKey(t *testing.T) {
	s := openTest(t)
	if err := s.SaveHostSample(context.Background(), model.HostSample{}); err == nil {
		t.Fatal("a reading with no host key should be refused")
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
