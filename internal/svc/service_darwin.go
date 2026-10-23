//go:build darwin

package svc

import "os"

func platformManager() (manager, error) {
	return launchdManager{root: "/", r: execRunner{}}, nil
}

// Interactive reports whether this process was started by a person rather
// than by launchd. launchd is PID 1 and the parent of every daemon it starts,
// and nothing a person runs from a terminal has it as its parent, which is
// the test the kardianos library used. (XPC_SERVICE_NAME is not a better one:
// Terminal sets it in every shell too.)
func Interactive() bool { return os.Getppid() != 1 }
