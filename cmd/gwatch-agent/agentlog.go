// The agent's own log file, and reading it back for the control panel.
//
// Until now the agent's log went wherever the service manager put its standard
// error: the journal under systemd, nowhere at all for a Windows service or a
// launchd job that was not told where to write. That is fine for someone with
// a terminal and journalctl, and useless for the person the control panel is
// for, who wants to press one button and see the last few things the agent
// said. So the running agent also copies its log to agent.log in its data
// directory, beside the status file, and the panel shows the end of it.
//
// It is kept small on purpose. When the file reaches maxLogFile it is moved
// aside to agent.log.1 (replacing the one before) and a fresh one is started,
// so the agent never holds more than about two megabytes of its own chatter on
// a machine and nothing has to clean up after it. It is written with owner-only
// permissions in a directory that is already private, so reading it takes the
// same rights as reading the configuration does; the panel has those rights or
// it would not have opened.
//
// Nothing secret should ever be logged, and nothing is, but a log is exactly
// the kind of text that gets pasted into an issue, so anything that looks like
// an agent token is replaced on the way into the file, and again on the way out
// to the panel (which also knows this machine's own token and removes it by
// value, in case it is ever not in the usual form).
package main

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/permissions"
)

const (
	// maxLogFile is the size at which agent.log is moved aside and restarted.
	maxLogFile = 1 << 20
	// logTailLines is how much of the log the panel and the diagnostics show.
	logTailLines = 50
	// logTailBytes bounds how much of the end of the file is read to find
	// those lines, so a log full of very long lines cannot make the panel
	// read a megabyte to show fifty of them.
	logTailBytes = 64 << 10
)

func logFile() string { return filepath.Join(filepath.Dir(statusFile()), "agent.log") }

// tokenPattern is the shape of an agent token (auth.AgentTokenPrefix and the
// key alphabet after it). It is generous about the characters on purpose: a
// false positive costs a line of log some letters, a miss costs a credential.
var tokenPattern = regexp.MustCompile(`gwa_[A-Za-z0-9_\-]{6,}`)

// redactSecrets takes anything that looks like a token, and each of the
// tokens given by value, out of text.
func redactSecrets(text string, tokens ...string) string {
	for _, t := range tokens {
		if t = strings.TrimSpace(t); len(t) >= 8 {
			text = strings.ReplaceAll(text, t, "[token removed]")
		}
	}
	return tokenPattern.ReplaceAllString(text, "gwa_[token removed]")
}

// cappedLog is the file half of the agent's log output. Writes that fail are
// dropped: a full disk must not stop the agent reporting, and standard error
// still has every line.
type cappedLog struct {
	mu   sync.Mutex
	path string
	f    *os.File
	size int64
}

func openCappedLog(path string) (*cappedLog, error) {
	if err := permissions.EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &cappedLog{path: path, f: f, size: st.Size()}, nil
}

func (c *cappedLog) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.f == nil {
		return len(p), nil
	}
	if c.size+int64(len(p)) > maxLogFile {
		c.f.Close()
		_ = os.Rename(c.path, c.path+".1")
		f, err := os.OpenFile(c.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			c.f = nil
			return len(p), nil
		}
		c.f, c.size = f, 0
	}
	n, _ := c.f.Write([]byte(redactSecrets(string(p))))
	c.size += int64(n)
	return len(p), nil
}

func (c *cappedLog) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.f == nil {
		return nil
	}
	err := c.f.Close()
	c.f = nil
	return err
}

// startServiceLog sends the standard logger to agent.log as well as to
// standard error, and returns what puts it back. An agent that cannot write
// the file (run by hand from an account that cannot write the data directory,
// say) logs to standard error alone, exactly as before.
func startServiceLog() func() {
	cl, err := openCappedLog(logFile())
	if err != nil {
		return func() {}
	}
	prev := log.Writer()
	log.SetOutput(io.MultiWriter(prev, cl))
	return func() {
		log.SetOutput(prev)
		cl.Close()
	}
}

// recentLog is the end of the agent's log as the panel shows it.
type recentLog struct {
	// Source says where the lines came from, in words, so that someone reading
	// them knows where to find the rest.
	Source string   `json:"source"`
	Lines  []string `json:"lines"`
	// Note is said when there are no lines, instead of an empty box.
	Note string `json:"note,omitempty"`
}

// journalTail reads the end of a systemd unit's journal. It is a variable so
// tests need not have systemd, and so it is never run anywhere it cannot work.
var journalTail = func(unit string, n int) ([]string, error) {
	if runtime.GOOS != "linux" {
		return nil, os.ErrNotExist
	}
	if _, err := exec.LookPath("journalctl"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "journalctl", "-u", unit, "-n", strconv.Itoa(n), "--no-pager", "-q", "-o", "short-iso").Output()
	if err != nil {
		return nil, err
	}
	return splitLines(string(out)), nil
}

func splitLines(s string) []string {
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		if t := strings.TrimRight(sc.Text(), "\r"); t != "" {
			lines = append(lines, t)
		}
	}
	return lines
}

// tailFile returns up to n of the last lines of path, reading no more than
// logTailBytes of it.
func tailFile(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	start := st.Size() - logTailBytes
	if start < 0 {
		start = 0
	}
	buf := make([]byte, st.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
		return nil, err
	}
	if start > 0 {
		// The first line is probably cut in half; drop it.
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	lines := splitLines(string(buf))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

// readRecentLog finds the end of the agent's log: its own file first, then,
// on Linux, the journal of whichever unit runs it (a service started by an
// agent from before the log file existed has only that).
func readRecentLog(n int, tokens ...string) recentLog {
	path := logFile()
	if lines, err := tailFile(path, n); err == nil && len(lines) > 0 {
		if len(lines) < n {
			if older, err := tailFile(path+".1", n-len(lines)); err == nil {
				lines = append(older, lines...)
			}
		}
		return recentLog{Source: path, Lines: redactLines(lines, tokens)}
	} else if err != nil && isPermissionError(err) {
		return recentLog{Source: path, Note: "This account cannot read the agent's log. " + elevateHint + "."}
	}
	unit := serviceName + ".service"
	if p, ok := currentPackaging(); ok && p.systemd {
		unit = "gwatch-agent.service"
	}
	if lines, err := journalTail(unit, n); err == nil && len(lines) > 0 {
		return recentLog{Source: "journalctl -u " + unit, Lines: redactLines(lines, tokens)}
	}
	return recentLog{Source: path, Note: "The agent has not written a log here yet. A running agent of this version keeps one; an older one, or one that has not started since it was updated, does not."}
}

func redactLines(lines, tokens []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = redactSecrets(l, tokens...)
	}
	return out
}
