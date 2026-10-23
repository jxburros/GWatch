//go:build linux

package serviceinstall

// This file is the part of the Linux service installation that decides what
// to write: the systemd unit, the environment file, and which of the paths a
// person might hand us can be written into either. None of it touches the
// machine, so all of it is tested directly (unit_linux_test.go); the part that
// creates users and runs systemctl is systemd_linux.go.
//
// The unit is modelled on the hardware agent's (packaging/nfpm/
// gwatch-agent.service), with the differences the server needs:
//
//   - it runs as its own system user rather than as root. The server has a
//     database full of credentials, a web interface on the network and, if an
//     administrator allows it, script actions; root is far more than any of
//     that needs, and the one thing it did need root for — opening an ICMP
//     socket — is a single capability, granted below.
//   - CAP_NET_RAW is ambient, not merely allowed: it is what lets the built-in
//     ping open a raw socket when the kernel's ping_group_range does not cover
//     the service's group, and what lets the system ping command work when the
//     built-in one cannot. Ambient capabilities are inherited by the programs
//     the service runs, which is the point: `ping` is one of them.
//   - CAP_NET_BIND_SERVICE is added only when the listen address is a port
//     below 1024. Nobody else gets it, because nobody else needs it.
//   - MemoryDenyWriteExecute is left off. It applies to every program the
//     service starts, and script actions run interpreters (node, for one) that
//     generate code at run time and stop working under it.
//   - the address families cover what the monitor uses and no more: IPv4 and
//     IPv6 for every check (ICMP and UDP included — they are AF_INET too),
//     AF_UNIX for the journal and a Docker socket, AF_NETLINK for reading the
//     interface list the network settings and discovery show.

import (
	"fmt"
	"net"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// UnitConfig is everything the unit file depends on.
type UnitConfig struct {
	Executable string // the program systemd starts; BinaryPath when installed
	User       string // the account it runs as; also used as the group
	DataDir    string // the one directory it may write to
	EnvFile    string // GWATCH_* settings, read by systemd as root
	Listen     string // only consulted for whether a low port needs a capability
}

// SystemdUnit renders the unit file for c. It is a pure function of its
// argument so that what a machine ends up running can be read, and tested,
// without installing anything. The caller is expected to have checked the
// paths with CheckUnitPath; SystemdUnit itself only escapes the characters
// systemd treats specially in them.
func SystemdUnit(c UnitConfig) string {
	caps := []string{"CAP_NET_RAW"}
	if NeedsBindCapability(c.Listen) {
		caps = append(caps, "CAP_NET_BIND_SERVICE")
	}
	var b strings.Builder
	line := func(s string) { b.WriteString(s); b.WriteByte('\n') }
	line("# GWatch network monitor, written by `gwatch install` (docs/INSTALL-LINUX.md).")
	line("#")
	line("# Re-running `gwatch install` rewrites this file, so keep local changes in a")
	line("# drop-in instead: sudo systemctl edit gwatch. Settings such as the listen")
	line("# address and the data directory are in " + c.EnvFile + ".")
	line("#")
	line("# The service runs as the " + c.User + " user with one capability, CAP_NET_RAW, for")
	line("# ping. It may write to its data directory and nowhere else; /home is hidden")
	line("# from it. Custom checks and script actions inherit all of that — the")
	line("# installation guide says how to grant them more.")
	line("[Unit]")
	line("Description=GWatch Network Monitor")
	line("Documentation=https://github.com/jxburros/GWatch/blob/main/docs/INSTALL-LINUX.md")
	line("Wants=network-online.target")
	line("After=network-online.target")
	line("StartLimitIntervalSec=60")
	line("StartLimitBurst=10")
	line("")
	line("[Service]")
	line("Type=simple")
	line("User=" + c.User)
	line("Group=" + c.User)
	line("EnvironmentFile=-" + unitEscape(c.EnvFile))
	line("ExecStart=" + unitEscape(c.Executable) + " run")
	// 78 is EX_CONFIG: the service exits with it when its settings are wrong
	// in a way another attempt five seconds later cannot fix (a malformed
	// listen address, say). Restarting it would only fill the journal.
	line("Restart=on-failure")
	line("RestartSec=5s")
	line("RestartPreventExitStatus=78")
	if filepath.Clean(c.DataDir) == DefaultDataDir {
		// systemd creates /var/lib/gwatch, owned by the service user, before
		// every start, so even a data directory someone removed by hand comes
		// back with the right owner and mode.
		line("StateDirectory=gwatch")
		line("StateDirectoryMode=0700")
	}
	line("ReadWritePaths=" + unitEscape(c.DataDir))
	line("UMask=0077")
	line("StandardOutput=journal")
	line("StandardError=journal")
	line("")
	line("AmbientCapabilities=" + strings.Join(caps, " "))
	line("CapabilityBoundingSet=" + strings.Join(caps, " "))
	line("NoNewPrivileges=yes")
	line("ProtectSystem=strict")
	line("ProtectHome=yes")
	line("PrivateTmp=yes")
	line("PrivateDevices=yes")
	line("ProtectKernelTunables=yes")
	line("ProtectKernelModules=yes")
	line("ProtectKernelLogs=yes")
	line("ProtectControlGroups=yes")
	line("ProtectClock=yes")
	line("ProtectHostname=yes")
	line("RestrictNamespaces=yes")
	line("RestrictRealtime=yes")
	line("RestrictSUIDSGID=yes")
	line("LockPersonality=yes")
	line("SystemCallArchitectures=native")
	line("RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK")
	line("")
	line("[Install]")
	line("WantedBy=multi-user.target")
	return b.String()
}

// unitEscape doubles the percent signs in a value, which systemd would
// otherwise read as the start of a specifier (%h, %n and the rest).
func unitEscape(s string) string { return strings.ReplaceAll(s, "%", "%%") }

// NeedsBindCapability reports whether listening on addr needs
// CAP_NET_BIND_SERVICE: a port from 1 to 1023. An address that does not parse
// does not, because it will not get as far as binding.
func NeedsBindCapability(addr string) bool {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n < 1024
}

// CheckUnitPath refuses paths the unit cannot carry or the service could not
// use. Whitespace and quotes are refused rather than escaped: ReadWritePaths=
// splits on spaces, and a data directory nobody can type without quoting is
// one nobody will get right in a drop-in later either. A directory under /home
// or /root is refused because ProtectHome= hides those from the service — it
// would start, find nothing, and fail.
func CheckUnitPath(what, p string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%s must be an absolute path, not %q", what, p)
	}
	if strings.ContainsAny(p, " \t\r\n\"'\\;") {
		return fmt.Errorf("%s %q contains a space, quote, backslash or semicolon; choose a plainer path such as %s", what, p, DefaultDataDir)
	}
	if UnderProtectedHome(p) {
		return fmt.Errorf("%s %s is inside a home directory, which the service is not allowed to see; use %s (the default) or another directory outside /home and /root", what, p, DefaultDataDir)
	}
	return nil
}

// UnderProtectedHome reports whether p is one of the directories
// ProtectHome=yes makes inaccessible, or inside one.
func UnderProtectedHome(p string) bool {
	p = filepath.Clean(p)
	for _, root := range []string{"/home", "/root", "/run/user"} {
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return false
}

// ParseEnv reads the KEY=value lines of an environment file the way systemd's
// EnvironmentFile= does for the files GWatch writes: blank lines and lines
// starting with # or ; are ignored, an "export " prefix is tolerated, and a
// value in double quotes has its backslash escapes undone. Single-quoted values
// are taken literally. It is only used to find out what an earlier install
// chose, so it does not need to accept everything systemd would.
func ParseEnv(text string) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(text, "\n") {
		key, val, ok := envLine(raw)
		if ok {
			out[key] = val
		}
	}
	return out
}

func envLine(raw string) (key, val string, ok bool) {
	s := strings.TrimSpace(raw)
	if s == "" || s[0] == '#' || s[0] == ';' {
		return "", "", false
	}
	s = strings.TrimPrefix(s, "export ")
	i := strings.IndexByte(s, '=')
	if i <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(s[:i])
	val = strings.TrimSpace(s[i+1:])
	switch {
	case len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"':
		inner := val[1 : len(val)-1]
		var b strings.Builder
		for j := 0; j < len(inner); j++ {
			if inner[j] == '\\' && j+1 < len(inner) {
				j++
			}
			b.WriteByte(inner[j])
		}
		val = b.String()
	case len(val) >= 2 && val[0] == '\'' && val[len(val)-1] == '\'':
		val = val[1 : len(val)-1]
	}
	return key, val, true
}

// quoteEnv writes a value so that ParseEnv, and systemd, read it back exactly.
func quoteEnv(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return `"` + v + `"`
}

// envHeader opens an environment file GWatch creates. An existing file keeps
// whatever its owner wrote at the top of it.
const envHeader = `# Settings for the GWatch service (gwatch.service). systemd reads this file as
# root when the service starts; it is readable by root alone because a
# database password (GWATCH_DB_PASSWORD) may be kept here.
#
# gwatch install writes GWATCH_DATA_DIR and GWATCH_LISTEN, and rewrites them
# when it is run again with --data-dir or --listen. Every other line is left as
# it is. After editing by hand: sudo systemctl restart gwatch
`

// MergeEnv returns the environment file text with each key in set given its
// new value. A key already in the file has its line replaced where it stands;
// a key that is not is appended. Every other line — comments, a database
// password someone added, settings this build has never heard of — is kept
// exactly as it was. An empty existing file starts with envHeader.
func MergeEnv(existing string, set map[string]string) (string, error) {
	keys := make([]string, 0, len(set))
	for k, v := range set {
		if strings.ContainsAny(v, "\r\n") {
			return "", fmt.Errorf("%s cannot contain a line break", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.TrimSpace(existing) == "" {
		existing = envHeader
	}
	lines := strings.Split(strings.TrimRight(existing, "\n"), "\n")
	done := map[string]bool{}
	for i, raw := range lines {
		key, _, ok := envLine(raw)
		if !ok {
			continue
		}
		if v, want := set[key]; want {
			if done[key] {
				// A second line for the same key would win over the one we
				// just wrote; comment it out rather than lose what it said.
				lines[i] = "# " + raw + "  (superseded by gwatch install)"
				continue
			}
			lines[i] = key + "=" + quoteEnv(v)
			done[key] = true
		}
	}
	for _, k := range keys {
		if !done[k] {
			lines = append(lines, k+"="+quoteEnv(set[k]))
		}
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// purgeRefused lists directories `gwatch uninstall --purge` will not delete
// whatever the environment file says the data directory is. A data directory
// is somewhere GWatch was told to keep its files, and a typo in that setting
// must not become `rm -rf /var`.
var purgeRefused = map[string]bool{
	"/": true, "/bin": true, "/boot": true, "/dev": true, "/etc": true, "/home": true,
	"/lib": true, "/lib64": true, "/media": true, "/mnt": true, "/opt": true, "/proc": true,
	"/root": true, "/run": true, "/sbin": true, "/srv": true, "/sys": true, "/tmp": true,
	"/usr": true, "/usr/local": true, "/var": true, "/var/lib": true, "/var/log": true,
}

// SafeToPurge reports whether dir may be deleted as GWatch's data directory:
// an absolute path that is not one of the system's own directories, and that
// is the default or already holds something only GWatch puts there. names are
// the entries in dir; an empty directory is safe to remove.
func SafeToPurge(dir string, names []string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("data directory %q is not an absolute path", dir)
	}
	dir = filepath.Clean(dir)
	if purgeRefused[dir] {
		return fmt.Errorf("refusing to delete %s: it is a system directory, not a GWatch data directory", dir)
	}
	if dir == DefaultDataDir || len(names) == 0 {
		return nil
	}
	for _, n := range names {
		switch n {
		case "gwatch.db", "gwatch.key", "database.json":
			return nil
		}
	}
	return fmt.Errorf("refusing to delete %s: it does not look like a GWatch data directory (no gwatch.db, gwatch.key or database.json in it); remove it by hand if you are sure", dir)
}
