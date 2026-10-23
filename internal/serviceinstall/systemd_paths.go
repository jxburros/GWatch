package serviceinstall

// The names and paths of the Linux systemd installation (systemd_linux.go).
// They are here, outside the Linux-only files, only so that main.go can print
// them on every platform it is compiled for; nothing off Linux uses them.

// Where the Linux service lives. They are constants rather than settings: a
// unit, a user and an environment file that move around are a unit, a user and
// an environment file nobody can find again to uninstall.
const (
	// UnitName is the service's name to systemctl and journalctl.
	UnitName = "gwatch.service"
	// LegacyUnitName is the unit kardianos/service wrote for releases before
	// this one, named after the Windows service. Installing over it migrates
	// it; nothing writes it any more.
	LegacyUnitName = "GWatch.service"

	unitDir        = "/etc/systemd/system"
	BinaryPath     = "/usr/local/bin/gwatch"
	legacyBinDir   = "/usr/local/libexec/gwatch"
	EnvDir         = "/etc/gwatch"
	EnvPath        = "/etc/gwatch/gwatch.env"
	DefaultDataDir = "/var/lib/gwatch"
	ServiceUser    = "gwatch"
	DefaultListen  = "127.0.0.1:7230"
)
