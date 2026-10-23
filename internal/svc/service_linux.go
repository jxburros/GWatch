//go:build linux

package svc

import (
	"bufio"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// platformManager picks the machine's init system, in the order the kardianos
// library tried them: systemd, then OpenRC, then a System V script as the
// fallback that works anywhere there is a /etc/init.d.
func platformManager() (manager, error) {
	switch {
	case isSystemd():
		return systemdManager{root: "/", r: execRunner{}}, nil
	case isOpenRC():
		return openrcManager{root: "/", r: execRunner{}}, nil
	}
	return sysvManager{root: "/", r: execRunner{}}, nil
}

// isSystemd is the test systemd itself documents (sd_booted): the directory
// it creates at boot. The check of PID 1's name covers a container started
// with systemd as its init but without /run mounted the usual way.
func isSystemd() bool {
	if fi, err := os.Stat("/run/systemd/system"); err == nil && fi.IsDir() {
		return true
	}
	b, err := os.ReadFile("/proc/1/comm")
	return err == nil && strings.TrimSpace(string(b)) == "systemd"
}

var openrcInittab = regexp.MustCompile(`::sysinit:.*openrc.*sysinit`)

// isOpenRC looks for OpenRC as init, or as what /etc/inittab runs at boot
// (Alpine's BusyBox init hands over to it that way), or for the state
// directory it keeps while it is managing services.
func isOpenRC() bool {
	if _, err := exec.LookPath("openrc-init"); err == nil {
		return true
	}
	if fi, err := os.Stat("/run/openrc"); err == nil && fi.IsDir() {
		return true
	}
	b, err := os.ReadFile("/etc/inittab")
	return err == nil && openrcInittab.Match(b)
}

var (
	interactiveOnce sync.Once
	interactive     bool
)

// Interactive reports whether this process was started by a person rather
// than by the service manager. It is the kardianos library's test, kept so
// that nothing changes for the programs that ask: a process in a Docker or
// LXC container is always interactive (it is the container's main process,
// and the container's restart policy is its service manager); otherwise one
// whose parent is PID 1, or a process named systemd (a user manager), was
// started by the init system.
func Interactive() bool {
	interactiveOnce.Do(func() { interactive = linuxInteractive() })
	return interactive
}

func linuxInteractive() bool {
	if inContainer("/proc/1/cgroup") {
		return true
	}
	ppid := os.Getppid()
	if ppid == 1 {
		return false
	}
	return parentName(ppid) != "systemd"
}

// inContainer reads the first few lines of PID 1's control groups for the
// marks Docker and LXC leave there.
func inContainer(cgroupFile string) bool {
	f, err := os.Open(cgroupFile)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for i := 0; i < 6 && sc.Scan(); i++ {
		if strings.Contains(sc.Text(), "docker") || strings.Contains(sc.Text(), "lxc") {
			return true
		}
	}
	return false
}

// parentName is the command name of process pid, from /proc/<pid>/stat.
func parentName(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return ""
	}
	return statName(string(b))
}

// statName takes the command name out of a /proc/<pid>/stat line. The name is
// in parentheses and may itself contain them, so it runs to the last ")".
func statName(stat string) string {
	open := strings.IndexByte(stat, '(')
	end := strings.LastIndexByte(stat, ')')
	if open < 0 || end <= open {
		return ""
	}
	return stat[open+1 : end]
}
