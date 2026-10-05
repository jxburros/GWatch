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
	if strings.EqualFold(src, dst) {
		return dst, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, "install-*")
	if err != nil {
		return "", err
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
		return "", err
	}
	if err = os.Rename(out.Name(), dst); err != nil {
		return "", err
	}
	return dst, nil
}

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
