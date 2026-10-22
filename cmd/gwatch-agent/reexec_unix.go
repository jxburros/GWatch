//go:build unix

package main

import "syscall"

// reexec replaces this process with exe, keeping its process ID, so that a
// service manager watching it sees nothing happen. It returns only on failure.
func reexec(exe string, argv, env []string) error {
	return syscall.Exec(exe, argv, env)
}
