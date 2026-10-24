package main

// `gwatch install` and `gwatch uninstall` on a Linux machine that runs
// systemd. The work is in internal/serviceinstall (systemd_linux.go); what is
// here is the part that belongs to the command line — which flags were given,
// what to print, and the exit codes.

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/jxburros/GWatch/internal/dbconfig"
	"github.com/jxburros/GWatch/internal/serviceinstall"
)

// configExitCode is EX_CONFIG from sysexits.h. The systemd unit names it in
// RestartPreventExitStatus=, so a service whose settings are wrong stops and
// says so once instead of being restarted every five seconds.
const configExitCode = 78

// configError marks a failure that is the settings' fault rather than the
// moment's: retrying will not help.
type configError struct{ error }

func (e configError) Unwrap() error { return e.error }

// fatalExitCode picks the exit status for a service that could not start.
func fatalExitCode(err error) int {
	var ce configError
	if errors.As(err, &ce) {
		return configExitCode
	}
	return 1
}

// usageExitCode is the status for a malformed flag or setting: 2, as for any
// other usage error, except for the service itself under systemd, where the
// setting came from /etc/gwatch/gwatch.env and a restart loop would follow.
func usageExitCode(cmd string) int {
	if cmd == "run" && os.Getenv("INVOCATION_ID") != "" {
		return configExitCode
	}
	return 2
}

// givenFlags reports which flags were set on the command line, and counts a
// GWATCH_DATA_DIR or GWATCH_LISTEN in the environment as given too, since
// that is how those defaults are overridden. Install uses it to tell "keep
// what the last install chose" from "change it to this".
func givenFlags(fs *flag.FlagSet) map[string]bool {
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	for flagName, env := range map[string]string{"data-dir": "GWATCH_DATA_DIR", "listen": "GWATCH_LISTEN", "trusted-proxies": "GWATCH_TRUSTED_PROXIES"} {
		if os.Getenv(env) != "" {
			given[flagName] = true
		}
	}
	return given
}

// systemdCommand runs install or uninstall and returns the exit status.
func systemdCommand(cmd string, cfg config, given map[string]bool) int {
	if cmd == "uninstall" {
		err := serviceinstall.UninstallSystemd(serviceinstall.SystemdUninstall{
			Purge: cfg.purge, Yes: cfg.yes, In: os.Stdin, Out: os.Stdout,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "error: uninstall failed:", err)
			return 1
		}
		if cfg.purge {
			fmt.Println("Service removed, and its data, settings, user and program deleted.")
		} else {
			fmt.Printf("Service %s removed. The data, %s and %s were kept; `gwatch uninstall --purge` deletes them.\n", serviceinstall.UnitName, serviceinstall.EnvPath, serviceinstall.BinaryPath)
		}
		return 0
	}

	env := map[string]string{}
	if given["trusted-proxies"] {
		env["GWATCH_TRUSTED_PROXIES"] = cfg.trustedProxies
	}
	opts := serviceinstall.SystemdInstall{
		DataDir: cfg.dataDir, DataDirSet: given["data-dir"],
		Listen: cfg.listen, ListenSet: given["listen"],
		Env: env, NoStart: cfg.noStart, Out: os.Stdout,
	}
	if !cfg.db.Empty() {
		// The service is started with no flags at all, so a database given
		// here has to be written down for it to find.
		opts.Prepare = func(dataDir string) error {
			dbCfg, _, err := dbconfig.Resolve(dataDir, cfg.db)
			if err != nil {
				return err
			}
			if err := dbconfig.Save(dataDir, dbCfg); err != nil {
				return fmt.Errorf("cannot save database settings: %w", err)
			}
			fmt.Printf("Database settings saved to %s (%s).\n", dbconfig.Path(dataDir), dbCfg.Describe())
			return nil
		}
	}
	res, err := serviceinstall.InstallSystemd(opts)
	for _, m := range res.Migrated {
		fmt.Println("Upgraded the earlier installation:", m+".")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: install failed:", err)
		return 1
	}
	fmt.Printf("Installed %s as %s, running as the %s user (data in %s, settings in %s).\n",
		serviceinstall.BinaryPath, serviceinstall.UnitName, serviceinstall.ServiceUser, res.DataDir, serviceinstall.EnvPath)
	if !res.Started {
		fmt.Println("Not started (--no-start). Start it with: sudo systemctl start gwatch")
		return 0
	}
	fmt.Printf("Service started. Open http://%s in your browser (or run: gwatch open).\n", browserHost(res.Listen))
	fmt.Println("Logs: sudo journalctl -u gwatch -f")
	return 0
}
