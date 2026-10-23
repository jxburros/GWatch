//go:build !windows

package main

import "errors"

// runTray is the notification-area icon (tray_windows.go), which exists only
// on Windows for now. On macOS and Linux every tray library worth using needs
// cgo, which the agent's single pure-Go cross-compile cannot have, and Linux
// desktops do not agree on what a tray is (GNOME needs an extension); see
// docs/HARDWARE.md and entry 6 of docs/AGENT-DECISIONS.md. The control panel,
// `gwatch-agent ui`, does everything the tray would and works here.
func runTray(config, []string) error {
	return errors.New("the notification-area icon is Windows-only for now; use `gwatch-agent ui` for the control panel")
}
