// Asking the operating system for an administrator's rights.
//
// The control panel runs as whoever started it (ui.go, and entry 6 of
// docs/AGENT-DECISIONS.md), which is right, but on most machines the agent's
// settings and its service belong to root or the administrators, and "run
// sudo gwatch-agent ui from a terminal" is no answer for someone who opened
// the panel from the Start menu precisely because they do not use a terminal.
// So where the desktop has a way to ask for elevation, the panel uses it, in
// two places and nowhere else:
//
//   - starting, stopping and restarting the service: the panel first asks the
//     service manager itself, and only if that is refused for want of rights
//     does it run `gwatch-agent start|stop|restart` again through the system's
//     own prompt (UAC on Windows, polkit's pkexec on a Linux desktop, the
//     administrator dialog on macOS). Nothing secret is on that command line.
//   - opening the panel over settings this account cannot read: rather than
//     refusing, `gwatch-agent ui` starts itself again through the same prompt.
//     The elevated copy serves the panel; this one opens the browser.
//
// The second needs care, because the panel's one-time address is a secret and
// the elevated copy is a different account (root, or an administrator's
// elevated token). It must not be put on a command line, where every user of
// the machine can read it, and the elevated copy must not open the browser
// itself, which would run the browser as root. So this copy makes a private
// directory of its own and passes its path (not a secret) with --ui-handoff;
// the elevated copy writes the page that leads to the panel into it, makes
// that page belong to the directory's owner, and does nothing else with the
// desktop; this copy waits for the page to appear and opens it in the
// person's own browser, as the person. From there it is the ordinary panel:
// the code is good once, traded for a cookie, and the page that carried it is
// removed.
//
// Declining the prompt is an answer, not an error: the panel says what it
// could not do and how to do it by hand, exactly as it did before.
package main

import (
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// uiHandoffDir is --ui-handoff: set only on the elevated copy of the panel,
// to the directory its page goes in.
var uiHandoffDir string

// errElevationDeclined is what a prompt that was cancelled comes to.
var errElevationDeclined = errors.New("the request for administrator rights was declined")

// elevatedProc is a process started through the elevation prompt.
type elevatedProc interface {
	// Wait waits for it to finish and says whether it succeeded.
	Wait() error
}

// elevateService runs `gwatch-agent <action>` with an administrator's rights,
// asking for them first, and waits for it. It is what the panel's service
// buttons fall back to.
func elevateService(action string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	p, err := startElevated(exe, []string{action}, false)
	if err != nil {
		return err
	}
	return p.Wait()
}

// relaunchUIElevated starts the panel again with an administrator's rights,
// waits for it to hand over its page, and opens that page in this account's
// browser. It returns once the browser has been asked to open it.
func relaunchUIElevated(addr string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "gwatch-panel-")
	if err != nil {
		return err
	}
	args := []string{"ui", "--ui-handoff", dir, "--ui-listen", addr}
	if configPathOverride != "" {
		args = append(args, "--config", configPathOverride)
	}
	fmt.Println("The agent's settings belong to an administrator. Asking for their rights to open the control panel...")
	p, err := startElevated(exe, args, true)
	if err != nil {
		os.RemoveAll(dir)
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- p.Wait() }()

	page := filepath.Join(dir, handoffPage)
	deadline := time.Now().Add(uiCodeLifetime)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(page); err == nil {
			openInBrowser(page)
			fmt.Println("The control panel is open in your browser. It runs with administrator rights, only on this machine, and closes itself after half an hour of not being used.")
			return nil
		}
		select {
		case err := <-exited:
			// A prompt that returns at once (macOS hands the panel to the
			// background and comes back) is not a failure; anything that
			// ended badly is.
			if err != nil {
				os.RemoveAll(dir)
				return err
			}
			exited = nil
		case <-time.After(250 * time.Millisecond):
		}
	}
	os.RemoveAll(dir)
	return errors.New("the control panel did not start in time; run `gwatch-agent ui` again, or `sudo gwatch-agent ui` from a terminal")
}

const handoffPage = "open-panel.html"

// launchPageHTML is the page that sends the browser on to link.
func launchPageHTML(link string) []byte {
	esc := html.EscapeString(link)
	return []byte(`<!doctype html><meta charset="utf-8"><meta name="referrer" content="no-referrer">` +
		`<meta http-equiv="refresh" content="0;url=` + esc + `"><title>GWatch agent</title>` +
		`<p>Opening the GWatch agent control panel… <a href="` + esc + `">continue</a></p>`)
}

// writeHandoffPage is the elevated panel's half of the handover: it writes the
// page into the directory the unelevated copy made, as a new file (never over
// one that is already there), and gives it to the directory's owner so that
// person's browser can read it. The returned function removes the directory,
// which is the unelevated copy's and has no other use.
func writeHandoffPage(dir, link string) (string, func(), error) {
	st, err := os.Lstat(dir)
	if err != nil {
		return "", nil, err
	}
	if !st.IsDir() {
		return "", nil, fmt.Errorf("%s is not a directory", dir)
	}
	page := filepath.Join(dir, handoffPage)
	f, err := os.OpenFile(page, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", nil, err
	}
	_, werr := f.Write(launchPageHTML(link))
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = giveToDirOwner(page, st)
	}
	var once sync.Once
	cleanup := func() { once.Do(func() { _ = os.RemoveAll(dir) }) }
	if werr != nil {
		cleanup()
		return "", nil, werr
	}
	return page, cleanup, nil
}
