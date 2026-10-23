package checks

// Which way of sending a ping this machine allows, and why not the others.
//
// The built-in ping tries an unprivileged ICMP socket, then a raw one, and the
// "auto" method then falls back to the system ping command (icmp.go, ping.go).
// That order means a misconfigured machine still pings, but it also meant
// nobody could see why it had fallen back — or, when everything failed, what
// to change. ProbePing opens each kind of socket once, closes it straight away,
// and says in words what it found; on Linux that includes reading
// net.ipv4.ping_group_range and naming the groups it does not cover, which is
// almost always the answer (icmp_hint_linux.go). The result is logged at
// startup, shown in Monitor Health and given to a discovery run that would
// otherwise just find nothing.

import (
	"fmt"
	"net"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// pingProbeTTL is how long a probe's answer is reused. Health is polled every
// few seconds by every open browser, and opening two sockets for each poll
// would be waste; a minute is still quick enough to see a sysctl change.
const pingProbeTTL = time.Minute

var (
	pingProbeMu    sync.Mutex
	pingProbeCache model.PingStatus
	pingProbeAt    time.Time

	fallbackMu  sync.Mutex
	fallbackMsg string
	fallbackAt  time.Time
)

// The probe's contact with the machine, replaceable by tests.
var (
	probeUnprivileged = func() error {
		pc, err := listenUnprivilegedICMP(false)
		if err == nil {
			pc.Close()
		}
		return err
	}
	probeRaw = func() error {
		pc, err := net.ListenPacket("ip4:icmp", "")
		if err == nil {
			pc.Close()
		}
		return err
	}
	lookPing = func() (string, error) { return exec.LookPath("ping") }
)

// PingStatusNow returns the probe's answer, reusing one under a minute old,
// with the most recent fallback to the system ping command added.
func PingStatusNow() model.PingStatus {
	pingProbeMu.Lock()
	if pingProbeAt.IsZero() || time.Since(pingProbeAt) > pingProbeTTL {
		pingProbeCache = ProbePing()
		pingProbeAt = time.Now()
	}
	st := pingProbeCache
	pingProbeMu.Unlock()
	fallbackMu.Lock()
	if fallbackMsg != "" {
		at := fallbackAt
		st.LastFallback, st.LastFallbackAt = fallbackMsg, &at
	}
	fallbackMu.Unlock()
	return st
}

// ProbePing looks afresh. It is what startup logs.
func ProbePing() model.PingStatus {
	return probePing(probeUnprivileged, probeRaw, lookPing, unprivilegedICMPHint)
}

// recordFallback notes that an "auto" ping gave up on the built-in sender and
// ran the system command, and why. Only the latest is kept: it is a pointer
// to the reason, not a history.
func recordFallback(err error) {
	fallbackMu.Lock()
	fallbackMsg = err.Error()
	fallbackAt = time.Now()
	fallbackMu.Unlock()
}

func probePing(unpriv, raw func() error, look func() (string, error), hint func(error) string) model.PingStatus {
	st := model.PingStatus{CheckedAt: time.Now()}
	if err := unpriv(); err == nil {
		st.Unprivileged = true
	} else {
		st.UnprivilegedReason = hint(err)
	}
	if err := raw(); err == nil {
		st.Raw = true
	} else {
		st.RawReason = "raw ICMP socket: " + err.Error()
		if isSocketPermissionError(err) {
			st.RawReason += " (needs root or CAP_NET_RAW)"
		}
	}
	if p, err := look(); err == nil {
		st.SystemPing = p
	}
	switch {
	case st.Unprivileged:
		st.Auto = model.PingViaUnprivileged
		st.Summary = "the built-in ping uses an unprivileged ICMP socket"
	case st.Raw:
		st.Auto = model.PingViaRaw
		st.Summary = "the built-in ping uses a raw ICMP socket (" + st.UnprivilegedReason + ")"
	case st.SystemPing != "":
		st.Auto = model.PingViaSystem
		st.Summary = fmt.Sprintf("the built-in ping cannot open an ICMP socket, so ping runs the system command %s: %s; %s", st.SystemPing, st.UnprivilegedReason, st.RawReason)
	default:
		st.Auto = model.PingViaNone
		st.Summary = fmt.Sprintf("ping cannot work on this machine: %s; %s; and there is no ping command", st.UnprivilegedReason, st.RawReason)
	}
	return st
}

// explainPingGroupRange says why the kernel refused an unprivileged ICMP
// socket, given the contents of /proc/sys/net/ipv4/ping_group_range and the
// process's group ids (effective first, then supplementary). The kernel allows
// the socket when any of those groups lies within the range, inclusive; the
// default on many distributions, "1 0", allows nobody. It returns "" when one
// of the groups is in range, or the range cannot be read, because then the
// range is not the reason.
func explainPingGroupRange(rangeText string, gids []int) string {
	f := strings.Fields(rangeText)
	if len(f) != 2 {
		return ""
	}
	lo, err1 := strconv.ParseInt(f[0], 10, 64)
	hi, err2 := strconv.ParseInt(f[1], 10, 64)
	if err1 != nil || err2 != nil {
		return ""
	}
	seen := map[int]bool{}
	var list []int
	for _, g := range gids {
		if int64(g) >= lo && int64(g) <= hi {
			return ""
		}
		if !seen[g] {
			seen[g] = true
			list = append(list, g)
		}
	}
	if len(list) > 1 {
		rest := append([]int(nil), list[1:]...)
		sort.Ints(rest)
		list = append(list[:1], rest...)
	}
	names := make([]string, len(list))
	for i, g := range list {
		names[i] = strconv.Itoa(g)
	}
	who := "gid " + strings.Join(names, ", ")
	if len(names) > 1 {
		who = "gids " + strings.Join(names, ", ")
	}
	what := fmt.Sprintf("ping_group_range %d %d", lo, hi)
	if lo > hi {
		what += " (switched off for every group)"
	}
	return fmt.Sprintf("unprivileged ICMP not permitted: %s not in %s", who, what)
}
