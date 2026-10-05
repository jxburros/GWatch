// Package permissions protects local state before credentials or config are read.
package permissions

import (
	"fmt"
	"os"
)

func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("data directory must be a real directory: %s", path)
	}
	if err := validateOwner(path); err != nil {
		return err
	}
	return protectDir(path)
}

func EnsurePrivateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("private file must be a regular file: %s", path)
	}
	if err := validateOwner(path); err != nil {
		return err
	}
	return protectFile(path)
}
