//go:build !windows

package serviceinstall

func programFiles() (string, error) { return "", nil }
