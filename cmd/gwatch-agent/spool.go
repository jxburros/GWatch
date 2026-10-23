// The queue that keeps readings through an outage, and the small file that
// keeps the queue through a restart.
//
// When GWatch cannot be reached the agent goes on taking a reading every
// interval and holds the ones it could not deliver, oldest first, so that once
// GWatch answers again the history has a gap no longer than the outage, and
// usually none. GWatch files a reading at the time it was taken, not the time it
// arrived (internal/hostmon settles the timestamp), and a held reading that
// arrives after a newer one is added to the history at its own time without
// being judged as current (internal/engine), so catching up never raises an
// alert for something that has already passed.
//
// The held readings are also written to agent-queue.jsonl beside agent.json, so
// a restart, an update or a power cut in the middle of an outage does not lose
// them. That is the one thing besides its configuration and its own bookkeeping
// that the agent writes on the machine it watches, so the file is bounded three
// ways — by the `buffer` setting (no reading older than that is kept, and the
// count follows from it and the interval), by a hard count, and by a byte cap —
// and the oldest reading goes first whichever limit is reached. Nothing is
// written at all while GWatch is answering: a reading that goes straight out
// never touches the disk, and the file is removed once the queue is empty.
//
// The file is one reading per line. A held reading is appended as it is taken,
// which is one small write per interval through an outage instead of a rewrite
// of everything held; the whole file is rewritten (atomically: a temporary file
// renamed over it, see writeQueueFile) only when readings have left the front
// of the queue — delivered, or dropped to stay within a limit. A line torn by a
// power cut is skipped when the file is read back, and the file is then
// rewritten whole.
//
// The same queue, with the same limits and the same file discipline, holds a
// remote collector's check results through an outage (collector.go), in
// agent-results.jsonl beside the readings; it is generic over what it holds
// so the two cannot drift apart.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/jxburros/GWatch/internal/model"
	"github.com/jxburros/GWatch/internal/permissions"
)

// maxFlushPerCycle bounds how many held readings are sent in one go, so an
// agent that has been offline for hours catches up over a few cycles rather
// than in one burst.
const maxFlushPerCycle = 120

// maxQueued bounds the held readings by count whatever the buffer setting
// says, so a very short interval with a long buffer cannot grow without limit.
const maxQueued = 4000

// maxQueueBytes bounds the held readings by size, in memory and on disk. A
// reading is a kilobyte or two, more on a machine with many interfaces and
// drives; eight megabytes is six hours of minute readings several times over,
// and is nothing to any disk the agent could be running from. The file can run
// past it by at most a tenth before it is rewritten (see sync).
const maxQueueBytes = 8 << 20

// queueFile is where held readings are kept between runs: beside agent.json,
// like the agent's other bookkeeping.
func queueFile() string { return filepath.Join(filepath.Dir(configFile()), "agent-queue.jsonl") }

// sendFailure is a report that did not go through, with whether trying the same
// reading again later can help.
type sendFailure struct {
	err   error
	retry bool
}

func (f *sendFailure) Error() string { return f.err.Error() }
func (f *sendFailure) Unwrap() error { return f.err }

// heldItem is a reading (or a check result) and its encoding, which is kept so
// that rewriting the file, and counting bytes against the cap, never
// re-encodes anything.
type heldItem[T any] struct {
	m    T
	line []byte
}

func (h heldItem[T]) size() int { return len(h.line) + 1 }

// spool holds items that could not be delivered, oldest first. stamp says when
// an item was taken, which is what the age limit and the file's sanity check
// on reading back go by; noun is what the log calls one.
//
// It is not safe for concurrent use; each queue has one loop that owns it.
type spool[T any] struct {
	max      int
	maxAge   time.Duration
	maxBytes int
	held     []heldItem[T]
	bytes    int
	stamp    func(T) time.Time
	noun     string

	// The file, when there is one. disk is how many of the held readings, from
	// the front, the file already has; dropped is how many lines at the front of
	// the file are readings no longer held because a limit pushed them out
	// (harmless to leave for a while: they were never delivered, and the limits
	// are applied again when the file is read back); stale is that the file
	// still has readings that were delivered, which must not be sent twice after
	// a restart, so the next sync rewrites it.
	path         string
	disk         int
	dropped      int
	droppedBytes int
	stale        bool
	warned       bool
}

// readingQueue is the spool as the report loop uses it.
type readingQueue = spool[model.HostMetrics]

// newReadingQueue is a queue held in memory only.
func newReadingQueue(cfg config) *readingQueue {
	n := 0
	if cfg.buffer > 0 && cfg.interval > 0 {
		n = int(cfg.buffer / cfg.interval)
	}
	return &readingQueue{max: max(0, min(n, maxQueued)), maxAge: cfg.buffer, maxBytes: maxQueueBytes,
		stamp: func(m model.HostMetrics) time.Time { return m.Timestamp }, noun: "reading"}
}

// openReadingQueue is the queue the reporting agent uses: kept in path, and
// starting with whatever an earlier run left there that is still within the
// limits. With no buffer configured nothing is kept, and a file left from when
// there was one is removed.
func openReadingQueue(cfg config, path string) *readingQueue {
	return openSpool(newReadingQueue(cfg), cfg.buffer, path)
}

// openSpool gives a queue its file and reads back what an earlier run left in
// it.
func openSpool[T any](q *spool[T], buffer time.Duration, path string) *spool[T] {
	if buffer <= 0 {
		if err := os.Remove(path); err == nil {
			log.Printf("buffer is 0s, so the %ss an earlier run held in %s were discarded", q.noun, path)
		}
		return q
	}
	q.path = path
	n, skipped := q.load(time.Now())
	if n > 0 {
		log.Printf("%d %s(s) held from before this agent started will be sent, oldest first", n, q.noun)
	}
	if skipped > 0 {
		log.Printf("%d held %s(s) in %s were unreadable or outside the buffer and were discarded", skipped, q.noun, path)
	}
	return q
}

// load reads the file back. A line that does not parse (the tail of a write a
// power cut interrupted), a reading with no time, one older than the buffer or
// one from the future is skipped; then the count and size limits keep the
// newest. Anything skipped or cut means the file no longer matches the queue,
// so it is rewritten at the next sync.
func (q *spool[T]) load(now time.Time) (kept, skipped int) {
	f, err := os.Open(q.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("could not read the held readings in %s: %v", q.path, err)
		}
		return 0, 0
	}
	defer f.Close()
	// Never read more than the file could legitimately hold, whatever is on
	// disk: a file that has grown past that was not written by this agent.
	sc := bufio.NewScanner(io.LimitReader(f, int64(q.maxBytes)*2))
	sc.Buffer(make([]byte, 64<<10), q.maxBytes)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var m T
		if err := json.Unmarshal(line, &m); err != nil {
			skipped++
			continue
		}
		if ts := q.stamp(m); ts.IsZero() || ts.Before(now.Add(-q.maxAge)) || ts.After(now.Add(time.Minute)) {
			skipped++
			continue
		}
		q.held = append(q.held, heldItem[T]{m: m, line: append([]byte(nil), line...)})
		q.bytes += len(line) + 1
	}
	if sc.Err() != nil {
		skipped++
	}
	before := len(q.held)
	q.trim()
	skipped += before - len(q.held)
	q.disk, q.dropped, q.droppedBytes = len(q.held), 0, 0
	q.stale = skipped > 0
	return len(q.held), skipped
}

func (q *spool[T]) len() int { return len(q.held) }

// add puts a new reading at the back, dropping the oldest if the queue is full.
// With no buffer at all the queue holds only what is about to be sent.
func (q *spool[T]) add(m T) {
	line, err := json.Marshal(m)
	if err != nil {
		// A reading is plain data and always encodes; this is not reachable in
		// practice, and dropping the reading is the honest answer if it were.
		log.Printf("could not hold a %s: %v", q.noun, err)
		return
	}
	q.held = append(q.held, heldItem[T]{m: m, line: line})
	q.bytes += len(line) + 1
	q.trim()
}

// trim drops the oldest readings until the queue is within its count and size
// limits. It always keeps the newest reading, whatever its size, because that
// one is about to be sent.
func (q *spool[T]) trim() {
	limit := max(q.max, 1)
	n := 0
	size := q.bytes
	for len(q.held)-n > 1 && (len(q.held)-n > limit || size > q.maxBytes) {
		size -= q.held[n].size()
		n++
	}
	if n == 0 {
		return
	}
	// Readings pushed out by a limit were never delivered, so leaving them in
	// the file a little longer is harmless; the file is rewritten once enough
	// of them have built up (see sync).
	for i := 0; i < n && i < q.disk; i++ {
		q.dropped++
		q.droppedBytes += q.held[i].size()
	}
	q.disk = max(0, q.disk-n)
	q.drop(n)
}

// drop removes n readings from the front.
func (q *spool[T]) drop(n int) {
	for i := 0; i < n; i++ {
		q.bytes -= q.held[i].size()
		q.held[i] = heldItem[T]{}
	}
	q.held = q.held[n:]
}

// delivered removes the reading at the front because it has gone (or GWatch
// refused it for good), which the file must stop offering.
func (q *spool[T]) delivered() {
	if q.disk > 0 {
		q.stale = true
		q.disk--
	}
	q.drop(1)
}

// flush sends held readings oldest first and stops at the first that cannot go.
// It reports how many were delivered, and the failure if there was one. A
// reading the server refuses outright (not an outage, a verdict) is dropped,
// because holding it would block everything behind it for ever.
func (q *spool[T]) flush(ctx context.Context, sendOne func(T) error) (sent int, err error) {
	for len(q.held) > 0 && sent < maxFlushPerCycle {
		if ctx.Err() != nil {
			return sent, ctx.Err()
		}
		err := sendOne(q.held[0].m)
		if err == nil {
			q.delivered()
			sent++
			continue
		}
		var sf *sendFailure
		if errors.As(err, &sf) && !sf.retry {
			log.Printf("dropping a %s GWatch refused: %v", q.noun, err)
			q.delivered()
			continue
		}
		return sent, err
	}
	return sent, nil
}

// flushBatch is flush for a receiver that takes many at once: up to n items
// from the front go in one call, and leave the queue together. A batch GWatch
// refuses outright is dropped whole, for the same reason flush drops one item.
func (q *spool[T]) flushBatch(ctx context.Context, n int, sendMany func([]T) error) (sent int, err error) {
	if len(q.held) == 0 || ctx.Err() != nil {
		return 0, ctx.Err()
	}
	k := min(n, len(q.held))
	batch := make([]T, k)
	for i := range batch {
		batch[i] = q.held[i].m
	}
	err = sendMany(batch)
	var sf *sendFailure
	if err != nil && !(errors.As(err, &sf) && !sf.retry) {
		return 0, err
	}
	if err != nil {
		log.Printf("dropping %d %s(s) GWatch refused: %v", k, q.noun, err)
	}
	for i := 0; i < k; i++ {
		q.delivered()
	}
	if err != nil {
		return 0, nil
	}
	return k, nil
}

// sync makes the file match the queue, doing as little as it can: nothing
// while nothing is held and no file exists, an append of the readings added
// since the last sync, or — once readings have been delivered, or enough have
// been pushed out by a limit — a rewrite of the whole queue. A failure to write
// is logged once and otherwise ignored: the readings are still held in memory,
// and the agent's job is to report, not to insist on a disk.
func (q *spool[T]) sync() {
	if q.path == "" {
		return
	}
	var err error
	switch {
	case len(q.held) == 0:
		if q.disk > 0 || q.dropped > 0 || q.stale {
			if err = os.Remove(q.path); errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		}
		if err == nil {
			q.disk, q.dropped, q.droppedBytes, q.stale = 0, 0, 0, false
		}
	case q.stale || q.dropped > max(q.max/10, 1) || q.droppedBytes > q.maxBytes/10:
		if err = writeQueueFile(q.path, encodeLines(q.held)); err == nil {
			q.disk, q.dropped, q.droppedBytes, q.stale = len(q.held), 0, 0, false
		}
	case q.disk < len(q.held):
		err = q.appendLines(q.held[q.disk:])
		if err == nil {
			q.disk = len(q.held)
		} else {
			// Part of a line may have reached the file. Rewriting it whole next
			// time is what guarantees the next append starts on a line of its own.
			q.stale = true
		}
	}
	if err != nil {
		if !q.warned {
			q.warned = true
			log.Printf("could not keep held %ss in %s (%v); they are held in memory and lost if the agent restarts before GWatch answers", q.noun, q.path, err)
		}
		return
	}
	q.warned = false
}

// appendLines adds readings to the end of the file, creating it privately if it
// is not there, and flushes them to disk so a power cut keeps them.
func (q *spool[T]) appendLines(hs []heldItem[T]) error {
	if _, err := os.Stat(q.path); errors.Is(err, os.ErrNotExist) {
		// A new file is created the careful way, private from its first byte.
		return writeQueueFile(q.path, encodeLines(hs))
	}
	f, err := os.OpenFile(q.path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(encodeLines(hs)); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// encodeLines is readings as the file holds them: one per line.
func encodeLines[T any](hs []heldItem[T]) []byte {
	var buf bytes.Buffer
	for _, h := range hs {
		buf.Write(h.line)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

// writeQueueFile replaces the queue file whole. The readings in it describe
// this machine, so it is private like everything else beside agent.json: 0600
// under a 0700 directory, or SYSTEM and administrators on Windows.
func writeQueueFile(path string, data []byte) error {
	if err := permissions.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return err
	}
	if err := writeFileAtomic(path, data); err != nil {
		return err
	}
	restrictWindowsACL(path)
	return nil
}
