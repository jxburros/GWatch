package svc

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

// runner runs a system command (systemctl, launchctl, rc-service) and
// reports its standard output and exit status. err is set when the command
// could not be started or exited with a status other than zero, and then
// carries whatever the command printed on standard error, which is usually
// the only explanation there is. The production runner is execRunner; the
// tests stand in a fake that records the commands and answers from a script.
type runner interface {
	run(name string, args ...string) (out string, code int, err error)
}

type execRunner struct{}

func (execRunner) run(name string, args ...string) (string, int, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), 0, nil
	}
	code := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	}
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		msg = strings.TrimSpace(stdout.String())
	}
	if msg == "" {
		return stdout.String(), code, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return stdout.String(), code, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, msg)
}

// runForeground is how a program runs everywhere but under the Windows
// Service Control Manager: start it, wait for Ctrl+C or SIGTERM (which is
// how systemd, launchd, OpenRC and kill all ask a process to stop), and stop
// it. A program that ends by itself — the server after a restart request, for
// one — leaves with os.Exit from its own goroutine and never comes back here.
func runForeground(s *Service) error {
	if err := s.prog.Start(s); err != nil {
		return err
	}
	sig := make(chan os.Signal, 3)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	signal.Stop(sig)
	return s.prog.Stop(s)
}
