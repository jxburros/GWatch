//go:build !windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

// isElevated reports whether this process already has root's rights, in which
// case there is nothing to ask for.
func isElevated() bool { return os.Geteuid() == 0 }

// canElevate reports whether there is a desktop here that can show an
// administrator prompt. Over SSH, or on a machine with no desktop session,
// there is not, and the panel says "use sudo" as it always has.
func canElevate() bool {
	if isElevated() || os.Getenv("SSH_CONNECTION") != "" {
		return false
	}
	switch runtime.GOOS {
	case "linux":
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return false
		}
		_, err := exec.LookPath("pkexec")
		return err == nil
	case "darwin":
		_, err := exec.LookPath("osascript")
		return err == nil
	}
	return false
}

type unixElevated struct {
	cmd        *exec.Cmd
	background bool
}

func (p unixElevated) Wait() error {
	var out []byte
	var err error
	if p.background {
		// A panel that outlives this process must not be left writing into a
		// pipe nobody reads: its first line after that (pair() prints one)
		// would end it with SIGPIPE. Its output goes nowhere instead; the
		// person is looking at the browser.
		err = p.cmd.Run()
	} else {
		out, err = p.cmd.CombinedOutput()
	}
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		// pkexec: 126 when the person dismissed the prompt, 127 when they
		// could not authenticate. osascript reports "User canceled" (-128).
		if c := exit.ExitCode(); c == 126 || c == 127 || strings.Contains(string(out), "-128") {
			return errElevationDeclined
		}
	}
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	return err
}

// startElevated runs exe with args as root, through pkexec on Linux (polkit
// shows the desktop's own password dialog) or an AppleScript "with
// administrator privileges" on macOS. background is for a process that keeps
// running (the panel): on macOS it is sent to the background so that the
// dialog's script returns.
func startElevated(exe string, args []string, background bool) (elevatedProc, error) {
	if !canElevate() {
		return nil, errors.New("there is no way to ask for administrator rights here; use sudo")
	}
	switch runtime.GOOS {
	case "linux":
		// pkexec wants an absolute path, and runs the program with a clean
		// environment; nothing the panel needs comes from the environment.
		cmd := exec.Command("pkexec", append([]string{exe}, args...)...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		return unixElevated{cmd: cmd, background: background}, nil
	case "darwin":
		line := shellQuote(exe)
		for _, a := range args {
			line += " " + shellQuote(a)
		}
		if background {
			line += " >/dev/null 2>&1 &"
		}
		script := `do shell script "` + appleScriptEscape(line) + `" with administrator privileges`
		return unixElevated{cmd: exec.Command("osascript", "-e", script), background: background}, nil
	}
	return nil, errors.New("no administrator prompt on this platform")
}

// shellQuote quotes s for /bin/sh.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// appleScriptEscape escapes s for the inside of an AppleScript string.
func appleScriptEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

// giveToDirOwner makes the handed-over page belong to whoever owns the
// directory it is in: the person whose browser has to read it.
func giveToDirOwner(path string, dir os.FileInfo) error {
	st, ok := dir.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) == os.Geteuid() {
		return nil
	}
	return os.Lchown(path, int(st.Uid), int(st.Gid))
}
