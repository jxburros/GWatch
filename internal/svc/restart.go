package svc

// RestartsOnExit reports whether the service manager this machine has starts
// the service again when it exits by itself, which is how both programs move
// onto a version they have just installed. systemd, OpenRC's
// supervise-daemon, launchd and the Windows Service Control Manager do; a
// System V init script, and a system with no service manager of ours, do
// not, and a program there that exited to be restarted would stay stopped.
func RestartsOnExit() bool {
	m, err := platformManager()
	if err != nil {
		return false
	}
	return managerRestartsOnExit(m)
}

func managerRestartsOnExit(m manager) bool {
	if r, ok := m.(interface{ restartsOnExit() bool }); ok {
		return r.restartsOnExit()
	}
	return true
}
