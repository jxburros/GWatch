//go:build linux

package checks

import "os"

// unprivilegedICMPHint turns the error from opening an unprivileged ICMP
// socket into the reason, where Linux lets us find one: the
// net.ipv4.ping_group_range sysctl not covering any of our groups.
func unprivilegedICMPHint(err error) string {
	gids := []int{os.Getegid()}
	if more, gerr := os.Getgroups(); gerr == nil {
		gids = append(gids, more...)
	}
	if b, rerr := os.ReadFile("/proc/sys/net/ipv4/ping_group_range"); rerr == nil {
		if why := explainPingGroupRange(string(b), gids); why != "" {
			return why
		}
	}
	return "unprivileged ICMP socket: " + err.Error()
}
