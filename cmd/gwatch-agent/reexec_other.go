//go:build !unix

package main

import "errors"

// reexec cannot replace a process in place here: Windows has no exec, and
// starting a second process would take the service out from under its
// manager. On Windows a process's command line is not readable by other
// standard users, which makes carrying on the lesser harm.
func reexec(exe string, argv, env []string) error {
	return errors.New("this system cannot restart a process in place")
}
