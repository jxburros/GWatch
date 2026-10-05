//go:build !windows

package permissions

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func protectDir(path string) error { return os.Chmod(path, 0700) }
func ValidateConfigOwner(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("configuration is not a regular file: %s", path)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("unsafe owner or write permissions on %s", path)
	}
	return nil
}

func protectFile(path string) error { return os.Chmod(path, 0600) }

func EnsureServiceDir(path string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("service installation requires root")
	}
	for dir := filepath.Clean(path); ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err == nil {
			st, ok := info.Sys().(*syscall.Stat_t)
			if !info.IsDir() || !ok || st.Uid != 0 || info.Mode().Perm()&0022 != 0 {
				return fmt.Errorf("unsafe service installation directory: %s", dir)
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return EnsurePrivateDir(path)
}

func validateOwner(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) {
		return fmt.Errorf("untrusted owner: %s", path)
	}
	return nil
}
