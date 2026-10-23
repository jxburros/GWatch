// Package serviceinstall relocates service binaries out of user-writable downloads.
package serviceinstall

import (
	"fmt"
	"github.com/jxburros/GWatch/internal/permissions"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func Executable(name string) (string, error) {
	src, err := os.Executable()
	if err != nil {
		return "", err
	}
	src, err = filepath.EvalSymlinks(src)
	if err != nil {
		return "", err
	}
	dir := filepath.Join("/usr/local/libexec", name)
	ext := ""
	if runtime.GOOS == "windows" {
		base, err := programFiles()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, map[string]string{"gwatch": "GWatch", "gwatch-agent": "GWatch Agent"}[name])
		ext = ".exe"
	}
	if !filepath.IsAbs(dir) {
		return "", fmt.Errorf("cannot determine protected installation directory")
	}
	if err := permissions.EnsureServiceDir(dir); err != nil {
		return "", fmt.Errorf("install requires administrator/root permissions: %w", err)
	}
	dst := filepath.Join(dir, name+ext)
	if err := copyExecutable(src, dst); err != nil {
		return "", err
	}
	return dst, nil
}

// copyExecutable puts a copy of src at dst, by way of a temporary file in
// dst's own directory so that the final step is a rename: a service that is
// started while the copy is under way finds either the old program or the new
// one, never half of either. Copying a file onto itself is not an error — it
// is what re-running an install from the installed program amounts to.
func copyExecutable(src, dst string) error {
	if strings.EqualFold(src, dst) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(dst), "install-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Chmod(0755)
	}
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(out.Name(), dst)
}

// SystemdScript is the unit internal/svc writes for the hardware agent
// (cmd/gwatch-agent). The server no longer uses it on a systemd machine: there
// `gwatch install` writes a unit of its own, hardened and run as a dedicated
// user (systemd_linux.go).
//
// It is a text/template over svc's unit fields (.Description, .Path,
// .Arguments, .UserName) and its cmd and cmdEscape quoting functions, which
// are the ones the kardianos/service library offered before #46 replaced it,
// so that the unit an earlier release installed is the unit this one writes.
const SystemdScript = `[Unit]
Description={{.Description}}
After=network-online.target
Wants=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=10
[Service]
ExecStart={{.Path|cmdEscape}}{{range .Arguments}} {{.|cmd}}{{end}}
{{if .UserName}}User={{.UserName}}{{end}}
EnvironmentFile=-/etc/gwatch/gwatch.env
Restart=on-failure
RestartSec=5s
UMask=0077
StandardOutput=journal
StandardError=journal
SyslogLevelPrefix=yes
[Install]
WantedBy=multi-user.target
`
