//go:build !linux

package checks

// unprivilegedICMPHint has nothing to add off Linux: macOS allows the socket
// to everyone, and Windows has none (icmp_windows.go says so in its error).
func unprivilegedICMPHint(err error) string {
	return "unprivileged ICMP socket: " + err.Error()
}
