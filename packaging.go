package main

import (
	"fmt"
	"os"
	"strings"
)

// Packaged builds (#126).
//
// The .deb and .rpm (packaging/nfpm/gwatch.yaml) are built with
// -ldflags "-X main.packagedBy=deb" (or rpm) by packaging/build-server.sh.
// packagedBy reaches the updater, which then reports new releases but never
// installs one (internal/api/packaging.go), and `update --rollback`, which
// refuses. This file covers the rest: the package installs, enables and
// removes its own gwatch.service, so `gwatch install` and `gwatch uninstall`
// on a packaged binary would register a second, competing service (or remove
// the package's) behind apt's back. They are refused with the package's own
// command instead, before main parses anything.
func init() {
	if msg := packagedServiceRefusal(packagedBy, os.Args[1:]); msg != "" {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(2)
	}
}

// packagedServiceRefusal is the message `gwatch install` and `gwatch
// uninstall` get on a build packaged for a Linux package manager, or "" when
// the command is allowed.
func packagedServiceRefusal(by string, args []string) string {
	if len(args) == 0 {
		return ""
	}
	var source, remove string
	switch strings.ToLower(strings.TrimSpace(by)) {
	case "deb":
		source, remove = "a .deb package", "sudo apt remove gwatch (or apt purge gwatch to delete its data too)"
	case "rpm":
		source, remove = "an .rpm package", "sudo dnf remove gwatch"
	default:
		return ""
	}
	switch args[0] {
	case "install":
		return fmt.Sprintf("gwatch was installed from %s, which already installed and started the gwatch service (systemctl status gwatch). Settings go in /etc/gwatch/gwatch.env; then: sudo systemctl restart gwatch", source)
	case "uninstall":
		return fmt.Sprintf("gwatch was installed from %s, which manages the gwatch service; remove the package instead: %s", source, remove)
	}
	return ""
}
