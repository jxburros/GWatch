// Package logging provides a small file + memory logger appropriate for a
// local personal application: lines go to stdout, to a size-rotated log file
// and to an in-memory ring buffer served by the API.
package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	ringSize    = 1000
	maxFileSize = 5 * 1024 * 1024
)

// Logger is safe for concurrent use.
type Logger struct {
	mu     sync.Mutex
	file   *os.File
	path   string
	ring   []string
	next   int
	count  int
	stdout io.Writer
}

// New creates a logger writing to dir/gwatch.log. When dir is empty only
// stdout and the ring buffer are used.
func New(dir string, stdout io.Writer) (*Logger, error) {
	l := &Logger{ring: make([]string, ringSize), stdout: stdout}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		l.path = filepath.Join(dir, "gwatch.log")
		if err := l.open(); err != nil {
			return nil, err
		}
	}
	return l, nil
}

func (l *Logger) open() error {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	l.file = f
	return nil
}

// Path returns the log file path ("" when file logging is off).
func (l *Logger) Path() string { return l.path }

// Printf logs a formatted line.
func (l *Logger) Printf(format string, args ...any) {
	l.write("INFO", fmt.Sprintf(format, args...))
}

// Errorf logs an error line.
func (l *Logger) Errorf(format string, args ...any) {
	l.write("ERROR", fmt.Sprintf(format, args...))
}

// Warnf logs a warning line.
func (l *Logger) Warnf(format string, args ...any) {
	l.write("WARN", fmt.Sprintf(format, args...))
}

func (l *Logger) write(level, msg string) {
	line := fmt.Sprintf("%s %-5s %s", time.Now().Format("2006-01-02 15:04:05"), level, msg)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring[l.next] = line
	l.next = (l.next + 1) % ringSize
	if l.count < ringSize {
		l.count++
	}
	if l.stdout != nil {
		fmt.Fprintln(l.stdout, line)
	}
	if l.file != nil {
		fmt.Fprintln(l.file, line)
		if fi, err := l.file.Stat(); err == nil && fi.Size() > maxFileSize {
			l.rotate()
		}
	}
}

func (l *Logger) rotate() {
	l.file.Close()
	_ = os.Remove(l.path + ".1")
	_ = os.Rename(l.path, l.path+".1")
	_ = l.open()
}

// Recent returns up to limit most recent lines, oldest first.
func (l *Logger) Recent(limit int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if limit <= 0 || limit > l.count {
		limit = l.count
	}
	out := make([]string, 0, limit)
	start := (l.next - limit + ringSize) % ringSize
	for i := 0; i < limit; i++ {
		out = append(out, l.ring[(start+i)%ringSize])
	}
	return out
}

// Close closes the log file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}
