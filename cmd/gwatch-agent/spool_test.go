package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// spoolBase is an hour before the tests started.
var spoolBase = time.Now().Add(-time.Hour).Truncate(time.Millisecond)

// spoolAt is a reading taken i minutes after spoolBase, so it is inside any
// buffer of an hour or more.
func spoolAt(i int) model.HostMetrics {
	return model.HostMetrics{Hostname: "h", Timestamp: spoolBase.Add(time.Duration(i) * time.Minute)}
}

func spoolConfig() config { return config{buffer: 6 * time.Hour, interval: time.Minute} }

func down(model.HostMetrics) error {
	return &sendFailure{err: errors.New("down"), retry: true}
}

func heldTimes(q *readingQueue) []int64 {
	var out []int64
	for _, h := range q.held {
		out = append(out, h.m.Timestamp.UnixMilli())
	}
	return out
}

// The point of the file: readings held through an outage survive the agent
// stopping (a restart, an update, a power cut) and come back in order.
func TestHeldReadingsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-queue.jsonl")
	q := openReadingQueue(spoolConfig(), path)
	for i := 0; i < 3; i++ {
		q.add(spoolAt(i))
		q.flush(context.Background(), down)
		q.sync()
	}
	again := openReadingQueue(spoolConfig(), path)
	if again.len() != 3 {
		t.Fatalf("held %d readings after a restart, want 3", again.len())
	}
	for i, h := range again.held {
		if !h.m.Timestamp.Equal(spoolAt(i).Timestamp) {
			t.Errorf("reading %d came back as %s", i, h.m.Timestamp)
		}
	}
	if fi, err := os.Stat(path); err != nil || (!isWindows() && fi.Mode().Perm() != 0o600) {
		t.Errorf("the queue file should be private: %v %v", fi.Mode(), err)
	}
}

// While GWatch answers, a reading goes straight out and the disk is never
// touched; once a backlog is delivered, the file goes.
func TestNothingIsWrittenWhileGWatchAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-queue.jsonl")
	q := openReadingQueue(spoolConfig(), path)
	ok := func(model.HostMetrics) error { return nil }
	for i := 0; i < 5; i++ {
		q.add(spoolAt(i))
		q.flush(context.Background(), ok)
		q.sync()
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a delivered reading was written to disk: %v", err)
		}
	}
	q.add(spoolAt(10))
	q.flush(context.Background(), down)
	q.sync()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("a held reading was not kept: %v", err)
	}
	q.flush(context.Background(), ok)
	q.sync()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file should be gone once everything is delivered: %v", err)
	}
}

// A reading that has been delivered must not be sent again after a restart,
// even when the restart comes in the middle of catching up.
func TestDeliveredReadingsLeaveTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-queue.jsonl")
	q := openReadingQueue(spoolConfig(), path)
	for i := 0; i < 5; i++ {
		q.add(spoolAt(i))
	}
	q.sync()
	sent := 0
	q.flush(context.Background(), func(model.HostMetrics) error {
		if sent == 2 {
			return &sendFailure{err: errors.New("down again"), retry: true}
		}
		sent++
		return nil
	})
	q.sync()
	again := openReadingQueue(spoolConfig(), path)
	if got := heldTimes(again); len(got) != 3 || got[0] != spoolAt(2).Timestamp.UnixMilli() {
		t.Errorf("after delivering two of five the file should hold the last three, got %v", got)
	}
}

// A power cut can leave half a line at the end of the file. That line is
// skipped, the rest is kept, and the file is rewritten whole so that the next
// append starts on a line of its own.
func TestATornLineIsSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-queue.jsonl")
	q := openReadingQueue(spoolConfig(), path)
	q.add(spoolAt(0))
	q.add(spoolAt(1))
	q.sync()
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	f.WriteString(`{"hostname":"h","ts":"20`)
	f.Close()

	again := openReadingQueue(spoolConfig(), path)
	if again.len() != 2 || !again.stale {
		t.Fatalf("held %d, stale %v; want the two good readings and a rewrite pending", again.len(), again.stale)
	}
	again.add(spoolAt(2))
	again.sync()
	third := openReadingQueue(spoolConfig(), path)
	if third.len() != 3 || third.stale {
		t.Errorf("after the rewrite the file should be clean with three readings: %d %v", third.len(), third.stale)
	}
}

// Readings older than the buffer, from the future, or with no time, are not
// worth sending after a long time switched off.
func TestLoadingKeepsOnlyWhatTheBufferAllows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-queue.jsonl")
	q := openReadingQueue(config{buffer: 48 * time.Hour, interval: time.Minute}, path)
	old := model.HostMetrics{Hostname: "h", Timestamp: time.Now().Add(-10 * time.Hour)}
	future := model.HostMetrics{Hostname: "h", Timestamp: time.Now().Add(time.Hour)}
	q.add(old)
	q.add(model.HostMetrics{Hostname: "h"})
	q.add(future)
	q.add(spoolAt(0))
	q.sync()

	// The machine was off for longer than a six-hour buffer.
	again := openReadingQueue(spoolConfig(), path)
	if got := heldTimes(again); len(got) != 1 || got[0] != spoolAt(0).Timestamp.UnixMilli() {
		t.Errorf("only the reading inside the buffer should survive, got %v", got)
	}
}

// The file is bounded by count and by bytes, oldest first, so the machine being
// watched cannot be filled up by its own agent however long GWatch is away.
func TestTheFileIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-queue.jsonl")
	q := openReadingQueue(config{buffer: time.Hour, interval: 6 * time.Minute}, path)
	for i := 0; i < 50; i++ {
		q.add(spoolAt(i))
		q.flush(context.Background(), down)
		q.sync()
	}
	if q.len() != 10 {
		t.Fatalf("held %d, want the 10 the buffer allows", q.len())
	}
	b, _ := os.ReadFile(path)
	if lines := strings.Count(string(b), "\n"); lines > 10+max(10/10, 1)+1 {
		t.Errorf("the file kept %d lines for a queue of 10", lines)
	}
	again := openReadingQueue(config{buffer: time.Hour, interval: 6 * time.Minute}, path)
	if got := heldTimes(again); len(got) != 10 || got[9] != spoolAt(49).Timestamp.UnixMilli() {
		t.Errorf("a restart should find the newest ten: %v", got)
	}

	// The byte cap, with a reading padded out to about a kilobyte.
	path2 := filepath.Join(t.TempDir(), "agent-queue.jsonl")
	big := openReadingQueue(spoolConfig(), path2)
	big.maxBytes = 20 << 10
	for i := 0; i < 200; i++ {
		m := spoolAt(i % 300)
		m.Warnings = []string{strings.Repeat("x", 1000)}
		big.add(m)
		big.flush(context.Background(), down)
		big.sync()
	}
	if big.bytes > big.maxBytes {
		t.Errorf("held %d bytes, cap %d", big.bytes, big.maxBytes)
	}
	if fi, _ := os.Stat(path2); fi.Size() > int64(big.maxBytes+big.maxBytes/10+2048) {
		t.Errorf("the file grew to %d bytes, cap %d", fi.Size(), big.maxBytes)
	}
}

// Turning the buffer off means nothing is kept, including what an earlier run
// left behind.
func TestNoBufferKeepsNoFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-queue.jsonl")
	q := openReadingQueue(spoolConfig(), path)
	q.add(spoolAt(0))
	q.sync()
	off := openReadingQueue(config{buffer: 0, interval: time.Minute}, path)
	if off.len() != 0 {
		t.Errorf("held %d with no buffer", off.len())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the old file should be removed: %v", err)
	}
	off.add(spoolAt(1))
	off.flush(context.Background(), down)
	off.sync()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("with no buffer nothing is written: %v", err)
	}
}

// A reading is marked as held — delivered late — by this machine's own clock,
// so that GWatch can refuse one whose time it cannot believe rather than file
// it as current.
func TestHeldReadingsAreMarked(t *testing.T) {
	now := time.Now()
	cfg := config{interval: time.Minute}
	if isHeld(cfg, model.HostMetrics{Timestamp: now.Add(-time.Second)}, now) {
		t.Error("a reading just taken is live")
	}
	if !isHeld(cfg, model.HostMetrics{Timestamp: now.Add(-5 * time.Minute)}, now) {
		t.Error("a reading five intervals old is held")
	}
	if isHeld(cfg, model.HostMetrics{}, now) {
		t.Error("a reading with no time is not marked")
	}
}
