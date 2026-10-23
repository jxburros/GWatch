//go:build linux

package serviceinstall

// `gwatch install` and `gwatch uninstall` on a Linux machine that runs
// systemd. This replaces kardianos/service's generic unit for the server
// (the hardware agent still uses that one): the service gets a user of its
// own, a program file it cannot overwrite, a data directory under /var/lib and
// a hardened unit (unit_linux.go), and running install a second time updates
// all of it rather than failing with "Init already exists".
//
// Everything here runs as root, from a terminal, once in a while. It is
// written to be re-run: every step either finds the machine already the way it
// wants it or puts it that way, so an install that stopped half-way is
// finished by running it again.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// SystemdAvailable reports whether this machine's service manager is systemd,
// which is what the rest of this file needs. Anything else (OpenRC, a
// container without an init) keeps the generic kardianos/service behaviour.
func SystemdAvailable() bool {
	fi, err := os.Stat("/run/systemd/system")
	return err == nil && fi.IsDir()
}

// SystemdServiceName is the name `gwatch start|stop|restart|status` should
// control: gwatch, or GWatch on a machine still running the unit an earlier
// release installed and not yet migrated by installing this one.
func SystemdServiceName() string {
	if exists(filepath.Join(unitDir, LegacyUnitName)) && !exists(filepath.Join(unitDir, UnitName)) {
		return strings.TrimSuffix(LegacyUnitName, ".service")
	}
	return strings.TrimSuffix(UnitName, ".service")
}

// SystemdInstall is what `gwatch install` was asked for.
type SystemdInstall struct {
	// DataDir and Listen are the flag values; the *Set fields say whether the
	// person actually gave them. A value that was not given falls back to what
	// the environment file already says, and only then to the default, so
	// that re-running install to update the program does not undo a change
	// made by hand or by an earlier `install --listen`.
	DataDir    string
	DataDirSet bool
	Listen     string
	ListenSet  bool
	// Env holds further settings to write into the environment file because
	// they were given on this command line (GWATCH_TRUSTED_PROXIES, say).
	Env     map[string]string
	NoStart bool
	// Prepare runs once the data directory exists and before it is handed to
	// the service user, for the caller to put files there (database.json).
	Prepare func(dataDir string) error
	Out     io.Writer
}

// SystemdResult reports what an install settled on.
type SystemdResult struct {
	DataDir  string
	Listen   string
	Started  bool
	Migrated []string // one line per thing moved over from an older install
}

// runCommand runs a system command and folds its output into the error. It
// is a variable so tests could stand in for systemctl and useradd; nothing in
// production replaces it.
var runCommand = func(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
		}
		return fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, msg)
	}
	return nil
}

// InstallSystemd installs or updates the service. See the file comment for
// the shape of it; the steps are in the order that leaves the machine usable
// if one of them fails: nothing is stopped until the new program, user, data
// directory and unit are all in place.
func InstallSystemd(o SystemdInstall) (SystemdResult, error) {
	var res SystemdResult
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	if os.Geteuid() != 0 {
		return res, errors.New("installing the service needs root: run it with sudo")
	}
	existing := readEnvFile()

	// The data directory: given, or what the last install wrote down, or the
	// default. A directory an older release put under /root (sudo gwatch
	// install without --data-dir used to do that) is moved to the default,
	// because the hardened service cannot see into /root at all.
	dataDir := DefaultDataDir
	switch {
	case o.DataDirSet:
		dataDir = o.DataDir
	case existing["GWATCH_DATA_DIR"] != "":
		dataDir = existing["GWATCH_DATA_DIR"]
	}
	if abs, err := filepath.Abs(dataDir); err == nil {
		dataDir = abs
	}
	if !o.DataDirSet && UnderProtectedHome(dataDir) && exists(dataDir) {
		// The old service is still writing there; it has to stop first.
		if exists(filepath.Join(unitDir, LegacyUnitName)) {
			_ = runCommand("systemctl", "stop", LegacyUnitName)
		}
		if err := moveDataDir(dataDir, DefaultDataDir); err != nil {
			return res, err
		}
		res.Migrated = append(res.Migrated, fmt.Sprintf("moved the data directory from %s to %s", dataDir, DefaultDataDir))
		dataDir = DefaultDataDir
	}
	if err := CheckUnitPath("the data directory", dataDir); err != nil {
		return res, err
	}
	listen := DefaultListen
	switch {
	case o.ListenSet:
		listen = o.Listen
	case existing["GWATCH_LISTEN"] != "":
		listen = existing["GWATCH_LISTEN"]
	}
	res.DataDir, res.Listen = dataDir, listen

	uid, gid, err := ensureServiceUser()
	if err != nil {
		return res, err
	}
	if err := installBinary(BinaryPath); err != nil {
		return res, err
	}

	// The environment file is root's: systemd reads it before dropping to the
	// service user, so the service never needs to.
	if err := os.MkdirAll(EnvDir, 0700); err != nil {
		return res, err
	}
	if err := os.Chmod(EnvDir, 0700); err != nil {
		return res, err
	}
	set := map[string]string{"GWATCH_DATA_DIR": dataDir, "GWATCH_LISTEN": listen}
	for k, v := range o.Env {
		set[k] = v
	}
	if err := writeEnvFile(set); err != nil {
		return res, err
	}

	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return res, fmt.Errorf("create data directory: %w", err)
	}
	if o.Prepare != nil {
		if err := o.Prepare(dataDir); err != nil {
			return res, err
		}
	}
	// A data directory that root's service wrote is root's, file by file;
	// the service user has to own all of it to go on using it.
	if err := chownTree(dataDir, uid, gid); err != nil {
		return res, fmt.Errorf("give the data directory to the %s user: %w", ServiceUser, err)
	}
	if err := os.Chmod(dataDir, 0700); err != nil {
		return res, err
	}

	unit := SystemdUnit(UnitConfig{Executable: BinaryPath, User: ServiceUser, DataDir: dataDir, EnvFile: EnvPath, Listen: listen})
	if err := writeFileAtomic(filepath.Join(unitDir, UnitName), []byte(unit), 0644); err != nil {
		return res, fmt.Errorf("write the unit file: %w", err)
	}

	// From here on the machine is being switched over.
	if exists(filepath.Join(unitDir, LegacyUnitName)) {
		_ = runCommand("systemctl", "disable", "--now", LegacyUnitName)
		if err := os.Remove(filepath.Join(unitDir, LegacyUnitName)); err != nil && !os.IsNotExist(err) {
			return res, err
		}
		res.Migrated = append(res.Migrated, "replaced the GWatch.service unit with "+UnitName)
	}
	if err := runCommand("systemctl", "daemon-reload"); err != nil {
		return res, err
	}
	if err := runCommand("systemctl", "enable", UnitName); err != nil {
		return res, err
	}
	if removed := removeLegacyBinary(); removed {
		res.Migrated = append(res.Migrated, "removed the old program in "+legacyBinDir+" (it now lives at "+BinaryPath+")")
	}
	if o.NoStart {
		return res, nil
	}
	// restart rather than start: on a machine where it is already running,
	// that is what makes it pick up the new program and settings.
	if err := runCommand("systemctl", "restart", UnitName); err != nil {
		return res, err
	}
	res.Started = true
	return res, nil
}

// SystemdUninstall is what `gwatch uninstall` was asked for.
type SystemdUninstall struct {
	// Purge also removes the data directory, the environment file, the
	// service user and the program. Yes skips the question that asks first.
	Purge   bool
	Yes     bool
	DataDir string // used only when the environment file does not name one
	In      io.Reader
	Out     io.Writer
}

// UninstallSystemd stops and removes the service, and with Purge everything
// install created. A purge asks before it deletes anything, and deletes
// nothing if the answer is not yes.
func UninstallSystemd(o SystemdUninstall) error {
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	if os.Geteuid() != 0 {
		return errors.New("removing the service needs root: run it with sudo")
	}
	dataDir := readEnvFile()["GWATCH_DATA_DIR"]
	if dataDir == "" {
		dataDir = o.DataDir
	}
	if dataDir == "" {
		dataDir = DefaultDataDir
	}
	if o.Purge {
		var names []string
		if entries, err := os.ReadDir(dataDir); err == nil {
			for _, e := range entries {
				names = append(names, e.Name())
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := SafeToPurge(dataDir, names); err != nil {
			return err
		}
		fmt.Fprintf(out, "This removes the GWatch service and deletes, permanently:\n  %s (the database, its encryption key, logs and backups)\n  %s\n  the %s user and group\n  %s\n", dataDir, EnvDir, ServiceUser, BinaryPath)
		if !o.Yes {
			fmt.Fprint(out, "Export a backup first if you might want any of it. Type yes to continue: ")
			answer := ""
			if o.In != nil {
				answer, _ = bufio.NewReader(o.In).ReadString('\n')
			}
			if strings.TrimSpace(strings.ToLower(answer)) != "yes" {
				return errors.New("nothing was removed (run with --yes to skip this question)")
			}
		}
	}
	for _, name := range []string{UnitName, LegacyUnitName} {
		path := filepath.Join(unitDir, name)
		if !exists(path) {
			continue
		}
		_ = runCommand("systemctl", "disable", "--now", name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	_ = runCommand("systemctl", "daemon-reload")
	if !o.Purge {
		return nil
	}
	if err := os.RemoveAll(dataDir); err != nil {
		return err
	}
	if err := os.RemoveAll(EnvDir); err != nil {
		return err
	}
	if _, err := user.Lookup(ServiceUser); err == nil {
		if err := deleteServiceUser(); err != nil {
			return err
		}
	}
	removeLegacyBinary()
	for _, p := range []string{BinaryPath, BinaryPath + ".old", BinaryPath + ".rollback"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// ensureServiceUser finds the gwatch system user, creating it (and its group)
// if there is none yet. useradd is what nearly every distribution has; the
// BusyBox adduser/addgroup pair covers Alpine and the small ones that do not.
func ensureServiceUser() (uid, gid int, err error) {
	if u, err := user.Lookup(ServiceUser); err == nil {
		return ids(u)
	}
	shell := "/bin/false"
	for _, s := range []string{"/usr/sbin/nologin", "/sbin/nologin"} {
		if exists(s) {
			shell = s
			break
		}
	}
	if _, lookErr := exec.LookPath("useradd"); lookErr == nil {
		err = runCommand("useradd", "--system", "--user-group", "--home-dir", DefaultDataDir, "--no-create-home",
			"--shell", shell, "--comment", "GWatch network monitor", ServiceUser)
	} else {
		if _, gerr := user.LookupGroup(ServiceUser); gerr != nil {
			if err = runCommand("addgroup", "-S", ServiceUser); err != nil {
				return 0, 0, fmt.Errorf("create the %s group: %w", ServiceUser, err)
			}
		}
		err = runCommand("adduser", "-S", "-D", "-H", "-h", DefaultDataDir, "-s", shell, "-G", ServiceUser, ServiceUser)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("create the %s user: %w", ServiceUser, err)
	}
	u, err := user.Lookup(ServiceUser)
	if err != nil {
		return 0, 0, fmt.Errorf("the %s user was created but cannot be found: %w", ServiceUser, err)
	}
	return ids(u)
}

func ids(u *user.User) (int, int, error) {
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, err
	}
	gid, err := strconv.Atoi(u.Gid)
	return uid, gid, err
}

// deleteServiceUser removes the user and, where userdel leaves it behind, its
// group.
func deleteServiceUser() error {
	var err error
	if _, lookErr := exec.LookPath("userdel"); lookErr == nil {
		err = runCommand("userdel", ServiceUser)
		if _, gerr := user.LookupGroup(ServiceUser); gerr == nil {
			_ = runCommand("groupdel", ServiceUser)
		}
	} else {
		err = runCommand("deluser", ServiceUser)
		if _, gerr := user.LookupGroup(ServiceUser); gerr == nil {
			_ = runCommand("delgroup", ServiceUser)
		}
	}
	return err
}

// installBinary copies the running program to dst. dst's directory must be
// root's and writable by nobody else — otherwise whoever could write there
// could replace what systemd starts — but unlike the private directories the
// other platforms install into, it is left readable: /usr/local/bin is on
// everyone's PATH, and the service user has to be able to run the program.
func installBinary(dst string) error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	if src, err = filepath.EvalSymlinks(src); err != nil {
		return err
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	for d := dir; ; d = filepath.Dir(d) {
		fi, err := os.Lstat(d)
		if err != nil {
			return err
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !fi.IsDir() || !ok || st.Uid != 0 || fi.Mode().Perm()&0022 != 0 {
			return fmt.Errorf("refusing to install into %s: %s is not a root-owned directory that only root can write to", dir, d)
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	if err := copyExecutable(src, dst); err != nil {
		return fmt.Errorf("copy the program to %s: %w", dst, err)
	}
	return os.Chmod(dst, 0755)
}

// removeLegacyBinary deletes the copy an earlier release kept in
// /usr/local/libexec/gwatch, with the .old and .rollback files self-update
// left beside it, and the directory if that leaves it empty. It reports
// whether there was anything to remove.
func removeLegacyBinary() bool {
	removed := false
	for _, n := range []string{"gwatch", "gwatch.old", "gwatch.rollback"} {
		if err := os.Remove(filepath.Join(legacyBinDir, n)); err == nil {
			removed = true
		}
	}
	_ = os.Remove(legacyBinDir) // only succeeds if empty
	return removed
}

// moveDataDir moves an existing data directory to its new home. It will not
// merge two: if the destination already holds files, the person decides which
// one to keep.
func moveDataDir(from, to string) error {
	if !exists(from) {
		return nil
	}
	if entries, err := os.ReadDir(to); err == nil && len(entries) > 0 {
		return fmt.Errorf("the data directory %s is inside a home directory, which the service is not allowed to see, and %s already holds files; move what you want to keep into %s, or reinstall with --data-dir %s", from, to, to, to)
	}
	_ = os.Remove(to) // an empty directory is in the way of a rename
	if err := os.MkdirAll(filepath.Dir(to), 0755); err != nil {
		return err
	}
	if err := os.Rename(from, to); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			return fmt.Errorf("the data directory %s must move to %s (the service is not allowed to see inside home directories), but they are on different filesystems; stop GWatch, run: sudo mv %s %s, and install again", from, to, from, to)
		}
		return err
	}
	return nil
}

// chownTree gives dir and everything under it to uid:gid. Symbolic links are
// changed themselves, never followed: a link in the data directory pointing
// at /etc/shadow must not hand /etc/shadow to the service.
func chownTree(dir string, uid, gid int) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}

// readEnvFile returns what the environment file says, or nothing if there is
// no file yet.
func readEnvFile() map[string]string {
	b, err := os.ReadFile(EnvPath)
	if err != nil {
		return map[string]string{}
	}
	return ParseEnv(string(b))
}

func writeEnvFile(set map[string]string) error {
	existing := ""
	if b, err := os.ReadFile(EnvPath); err == nil {
		existing = string(b)
	} else if !os.IsNotExist(err) {
		return err
	}
	text, err := MergeEnv(existing, set)
	if err != nil {
		return err
	}
	return writeFileAtomic(EnvPath, []byte(text), 0600)
}

// writeFileAtomic writes through a temporary file and a rename, so a unit or
// environment file is never seen half-written, and sets the mode before the
// contents go in.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".gwatch-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}
