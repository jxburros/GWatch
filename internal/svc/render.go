package svc

// What gets written to disk: the systemd unit, the launchd property list and
// the OpenRC and System V init scripts. Each is a pure function of the
// configuration, so what a machine ends up running can be read, and tested,
// on any machine (render_test.go), whatever the platform the code that writes
// them is compiled for.

import (
	"fmt"
	"path/filepath"
	"strings"
	"text/template"
)

// defaultSystemdUnit is the unit for a configuration that brings none of its
// own. Both programs bring their own (internal/serviceinstall.SystemdScript),
// so it is only here so that the zero Config still means something sensible.
const defaultSystemdUnit = `[Unit]
Description={{.Description}}
After=network-online.target
Wants=network-online.target

[Service]
ExecStart={{.Path|cmdEscape}}{{range .Arguments}} {{.|cmd}}{{end}}
Restart=always
RestartSec=5s

[Install]
WantedBy=multi-user.target
`

// unitData is what a SystemdUnit template is executed against. The field
// names, and the cmd and cmdEscape functions, are the ones the kardianos
// library offered, so that the unit template in internal/serviceinstall
// (SystemdScript) is unchanged by the move away from it. UserName is always
// empty: both programs' units leave the account to systemd's default, root.
type unitData struct {
	Name, DisplayName, Description string
	Path                           string
	Arguments                      []string
	UserName                       string
}

// systemdFuncs quote for the unit's ExecStart= line. cmd puts one argument
// in double quotes; cmdEscape leaves the program path unquoted, which is what
// systemd wants for the first word, and escapes the space instead. Both double
// the percent sign, which systemd would otherwise read as a specifier (%h,
// %n and so on), and cmd escapes the backslash, which it would read as the
// start of an escape sequence.
var systemdFuncs = template.FuncMap{
	"cmd": func(s string) string {
		r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`)
		return `"` + r.Replace(s) + `"`
	},
	"cmdEscape": func(s string) string {
		r := strings.NewReplacer(`\`, `\\`, ` `, `\x20`, `%`, `%%`)
		return r.Replace(s)
	},
}

// SystemdUnit renders the unit file for c, with exe as the program. The
// template is c.SystemdUnit, or a plain unit when that is empty.
func SystemdUnit(c Config, exe string) (string, error) {
	text := c.SystemdUnit
	if text == "" {
		text = defaultSystemdUnit
	}
	t, err := template.New("unit").Funcs(systemdFuncs).Parse(text)
	if err != nil {
		return "", fmt.Errorf("systemd unit template: %w", err)
	}
	var b strings.Builder
	err = t.Execute(&b, unitData{
		Name: c.Name, DisplayName: c.DisplayName, Description: c.Description,
		Path: exe, Arguments: c.Arguments,
	})
	if err != nil {
		return "", fmt.Errorf("systemd unit template: %w", err)
	}
	return b.String(), nil
}

// launchdLogPaths are where launchd sends the daemon's standard output and
// error: /var/log/<label>.out.log and .err.log, the files earlier releases
// used, so that an upgrade does not leave the old log behind somewhere nobody
// looks.
func launchdLogPaths(name string) (stdout, stderr string) {
	return "/var/log/" + name + ".out.log", "/var/log/" + name + ".err.log"
}

// LaunchdPlist renders the property list for /Library/LaunchDaemons. The
// daemon starts when it is loaded (RunAtLoad), which is at boot and when
// Start bootstraps it, and launchd starts it again whenever it exits
// (KeepAlive) — including the deliberate exit a self-update ends with, which
// is how the new program gets to run. Stop therefore has to unload the job,
// not merely end the process.
func LaunchdPlist(c Config, exe string) string {
	stdout, stderr := launchdLogPaths(c.Name)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + xmlEscape(c.Name) + `</string>
	<key>ProgramArguments</key>
	<array>
`)
	for _, a := range append([]string{exe}, c.Arguments...) {
		b.WriteString("\t\t<string>" + xmlEscape(a) + "</string>\n")
	}
	b.WriteString(`	</array>
	<key>Disabled</key>
	<false/>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>SessionCreate</key>
	<false/>
	<key>StandardOutPath</key>
	<string>` + xmlEscape(stdout) + `</string>
	<key>StandardErrorPath</key>
	<string>` + xmlEscape(stderr) + `</string>
</dict>
</plist>
`)
	return b.String()
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

// shQuote quotes s for a POSIX shell: inside single quotes nothing is
// special, so the only character that needs care is the single quote itself.
func shQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shWords quotes each argument and joins them, giving one string a shell
// reads back as the same list.
func shWords(args []string) string {
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = shQuote(a)
	}
	return strings.Join(q, " ")
}

// envFileLines is the snippet the init scripts use to read Config.EnvFile:
// every assignment in it is exported to the service (set -a), as systemd's
// EnvironmentFile= would, and a missing file is skipped.
func envFileLines(path string) string {
	if path == "" {
		return ""
	}
	return fmt.Sprintf("if [ -f %[1]s ]; then\n\tset -a\n\t. %[1]s\n\tset +a\nfi\n", shQuote(path))
}

// initLogBase is the stem of the init scripts' log files under /var/log: the
// program's own name, as earlier releases had it (gwatch.log, gwatch.err).
func initLogBase(exe string) string {
	base := filepath.Base(strings.ReplaceAll(exe, `\`, "/"))
	return strings.TrimSuffix(base, ".exe")
}

// OpenRCScript renders /etc/init.d/<name> for OpenRC (Alpine and others that
// do not run systemd). supervise-daemon keeps the program running and restarts
// it when it exits, as systemd's Restart= does.
//
// OpenRC reads command_args through eval, so the arguments are quoted twice:
// once each, so that eval gives back the same words, and the whole list once
// more, so that the assignment itself stores it unchanged.
func OpenRCScript(c Config, exe string) string {
	log := "/var/log/" + initLogBase(exe)
	var b strings.Builder
	b.WriteString("#!/sbin/openrc-run\n")
	b.WriteString("# Written by " + initLogBase(exe) + " install. Remove it with " + initLogBase(exe) + " uninstall.\n")
	b.WriteString("supervisor=supervise-daemon\n")
	b.WriteString("name=" + shQuote(firstNonEmpty(c.DisplayName, c.Name)) + "\n")
	b.WriteString("description=" + shQuote(c.Description) + "\n")
	b.WriteString("command=" + shQuote(exe) + "\n")
	if len(c.Arguments) > 0 {
		b.WriteString("command_args=" + shQuote(shWords(c.Arguments)) + "\n")
	}
	b.WriteString("supervise_daemon_args=" + shQuote("--stdout "+shQuote(log+".log")+" --stderr "+shQuote(log+".err")) + "\n")
	b.WriteString("\n" + envFileLines(c.EnvFile))
	b.WriteString("\ndepend() {\n\tuse net\n}\n")
	return b.String()
}

// SysVScript renders /etc/init.d/<name> for a machine with neither systemd
// nor OpenRC. It is the plainest possible init script: it starts the program
// in the background with its output appended to /var/log, remembers the
// process in /var/run, and nothing restarts it if it stops.
func SysVScript(c Config, exe string) string {
	base := initLogBase(exe)
	var b strings.Builder
	fmt.Fprintf(&b, `#!/bin/sh
# chkconfig: 2345 99 01
# description: %[1]s
### BEGIN INIT INFO
# Provides:          %[2]s
# Required-Start:    $network
# Required-Stop:
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: %[3]s
# Description:       %[1]s
### END INIT INFO
# Written by %[4]s install. Remove it with %[4]s uninstall.

pid_file=%[5]s
stdout_log=%[6]s
stderr_log=%[7]s

%[8]s
run_service() {
	exec %[9]s
}

is_running() {
	[ -f "$pid_file" ] && kill -0 "$(cat "$pid_file")" 2>/dev/null
}

case "$1" in
	start)
		if is_running; then
			echo "Already started"
			exit 0
		fi
		run_service >>"$stdout_log" 2>>"$stderr_log" &
		echo $! >"$pid_file"
		sleep 1
		if ! is_running; then
			echo "Unable to start, see $stdout_log and $stderr_log"
			exit 1
		fi
		;;
	stop)
		if is_running; then
			kill "$(cat "$pid_file")"
			i=0
			while is_running && [ $i -lt 30 ]; do
				sleep 1
				i=$((i + 1))
			done
			if is_running; then
				echo "Not stopped; it may still be shutting down"
				exit 1
			fi
		fi
		rm -f "$pid_file"
		;;
	restart)
		"$0" stop && "$0" start
		;;
	status)
		if is_running; then
			echo "Running"
		else
			echo "Stopped"
			exit 3
		fi
		;;
	*)
		echo "Usage: $0 {start|stop|restart|status}"
		exit 2
		;;
esac
exit 0
`,
		oneLine(c.Description), c.Name, oneLine(firstNonEmpty(c.DisplayName, c.Name)), base,
		shQuote("/var/run/"+c.Name+".pid"), shQuote("/var/log/"+base+".log"), shQuote("/var/log/"+base+".err"),
		envFileLines(c.EnvFile), shWords(append([]string{exe}, c.Arguments...)))
	return b.String()
}

// oneLine keeps a description from breaking out of the comment it is put in.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
