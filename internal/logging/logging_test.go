package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestMemoryOnlyLogger(t *testing.T) {
	var buf bytes.Buffer
	l, err := New("", &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	if l.Path() != "" {
		t.Errorf("Path() = %q, want empty when file logging is off", l.Path())
	}
	l.Printf("hello %s", "world")
	l.Warnf("careful")
	l.Errorf("boom %d", 7)

	lines := l.Recent(0)
	if len(lines) != 3 {
		t.Fatalf("Recent(0) returned %d lines, want 3: %v", len(lines), lines)
	}
	for i, want := range []string{"INFO  hello world", "WARN  careful", "ERROR boom 7"} {
		if !strings.HasSuffix(lines[i], want) {
			t.Errorf("line %d = %q, want suffix %q", i, lines[i], want)
		}
	}
	// Every line is timestamped "2006-01-02 15:04:05" before the level.
	if len(lines[0]) < 19 || lines[0][4] != '-' || lines[0][10] != ' ' {
		t.Errorf("line not timestamped: %q", lines[0])
	}
	if got := buf.String(); strings.Count(got, "\n") != 3 || !strings.Contains(got, "hello world") {
		t.Errorf("stdout mirror = %q", got)
	}
}

func TestNilStdoutIsAllowed(t *testing.T) {
	l, err := New("", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Printf("no writer attached") // must not panic
	if got := l.Recent(0); len(got) != 1 {
		t.Fatalf("Recent = %v", got)
	}
}

func TestFileLogging(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "logs") // New must create the tree
	l, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "gwatch.log")
	if l.Path() != want {
		t.Fatalf("Path() = %q, want %q", l.Path(), want)
	}
	l.Printf("first")
	l.Errorf("second")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "INFO  first") || !strings.Contains(string(b), "ERROR second") {
		t.Errorf("log file = %q", b)
	}

	// Reopening appends rather than truncating: restarts keep history.
	l2, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	l2.Printf("third")
	l2.Close()
	b, _ = os.ReadFile(want)
	if !strings.Contains(string(b), "first") || !strings.Contains(string(b), "third") {
		t.Errorf("reopen truncated the log: %q", b)
	}
}

func TestRotation(t *testing.T) {
	dir := t.TempDir()
	l, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	big := strings.Repeat("x", 64*1024)
	for i := 0; i < (maxFileSize/len(big))+2; i++ {
		l.Printf("%s", big)
	}

	rotated := filepath.Join(dir, "gwatch.log.1")
	fi, err := os.Stat(rotated)
	if err != nil {
		t.Fatalf("expected rotated file: %v", err)
	}
	if fi.Size() <= maxFileSize {
		t.Errorf("rotated at %d bytes, want > %d", fi.Size(), maxFileSize)
	}
	cur, err := os.Stat(filepath.Join(dir, "gwatch.log"))
	if err != nil {
		t.Fatal(err)
	}
	if cur.Size() > maxFileSize {
		t.Errorf("current log is %d bytes, rotation did not reset it", cur.Size())
	}
	// A second rotation must replace .1 instead of failing.
	for i := 0; i < (maxFileSize/len(big))+2; i++ {
		l.Printf("%s", big)
	}
	if _, err := os.Stat(rotated); err != nil {
		t.Errorf("second rotation lost the archive: %v", err)
	}
}

func TestRecentLimitAndRingWrap(t *testing.T) {
	l, _ := New("", nil)
	defer l.Close()

	for i := 0; i < 5; i++ {
		l.Printf("line-%d", i)
	}
	got := l.Recent(2)
	if len(got) != 2 || !strings.HasSuffix(got[0], "line-3") || !strings.HasSuffix(got[1], "line-4") {
		t.Fatalf("Recent(2) = %v, want the two newest, oldest first", got)
	}
	if n := len(l.Recent(100)); n != 5 {
		t.Errorf("Recent(100) = %d lines, want all 5", n)
	}
	if n := len(l.Recent(-1)); n != 5 {
		t.Errorf("Recent(-1) = %d lines, want all 5", n)
	}

	// Overflow the ring: it keeps the newest ringSize lines, in order.
	for i := 5; i < ringSize+20; i++ {
		l.Printf("line-%d", i)
	}
	all := l.Recent(0)
	if len(all) != ringSize {
		t.Fatalf("ring holds %d lines, want %d", len(all), ringSize)
	}
	if !strings.HasSuffix(all[len(all)-1], "line-"+strconv.Itoa(ringSize+19)) {
		t.Errorf("newest line = %q", all[len(all)-1])
	}
	if !strings.HasSuffix(all[0], "line-"+strconv.Itoa(20)) {
		t.Errorf("oldest retained line = %q", all[0])
	}
}

// TestConcurrentWrites is meaningful under -race, which CI runs.
func TestConcurrentWrites(t *testing.T) {
	l, _ := New(t.TempDir(), nil)
	defer l.Close()

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				l.Printf("writer %d line %d", w, i)
				l.Recent(10)
			}
		}(w)
	}
	wg.Wait()
	if n := len(l.Recent(0)); n != 400 {
		t.Errorf("logged %d lines, want 400", n)
	}
}
