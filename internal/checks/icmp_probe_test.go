package checks

import (
	"errors"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

func TestExplainPingGroupRange(t *testing.T) {
	cases := []struct {
		text string
		gids []int
		want string
	}{
		{"0\t2147483647\n", []int{998}, ""},
		{"1\t0\n", []int{998}, "unprivileged ICMP not permitted: gid 998 not in ping_group_range 1 0 (switched off for every group)"},
		{"0 0", []int{998, 4, 998, 24}, "unprivileged ICMP not permitted: gids 998, 4, 24 not in ping_group_range 0 0"},
		{"100 200", []int{998, 150}, ""}, // a supplementary group is enough
		{"100 200", []int{100}, ""},      // the bounds are inclusive
		{"100 200", []int{200}, ""},
		{"", []int{998}, ""},
		{"garbage here", []int{998}, ""},
	}
	for _, c := range cases {
		if got := explainPingGroupRange(c.text, c.gids); got != c.want {
			t.Errorf("explainPingGroupRange(%q, %v) = %q, want %q", c.text, c.gids, got, c.want)
		}
	}
}

func TestProbePingOrder(t *testing.T) {
	ok := func() error { return nil }
	denied := func() error { return errors.New("socket: operation not permitted") }
	havePing := func() (string, error) { return "/usr/bin/ping", nil }
	noPing := func() (string, error) { return "", errors.New("not found") }
	hint := func(err error) string { return "hint: " + err.Error() }

	st := probePing(ok, ok, havePing, hint)
	if st.Auto != model.PingViaUnprivileged || !st.Unprivileged || st.UnprivilegedReason != "" {
		t.Errorf("all available: %+v", st)
	}

	st = probePing(denied, ok, havePing, hint)
	if st.Auto != model.PingViaRaw || st.UnprivilegedReason != "hint: socket: operation not permitted" || !strings.Contains(st.Summary, "raw ICMP socket (hint:") {
		t.Errorf("raw only: %+v", st)
	}

	st = probePing(denied, denied, havePing, hint)
	if st.Auto != model.PingViaSystem || !strings.Contains(st.RawReason, "needs root or CAP_NET_RAW") || !strings.Contains(st.Summary, "/usr/bin/ping") {
		t.Errorf("system only: %+v", st)
	}

	st = probePing(denied, denied, noPing, hint)
	if st.Auto != model.PingViaNone || !strings.Contains(st.Summary, "no ping command") || !strings.Contains(st.Summary, "hint:") {
		t.Errorf("nothing: %+v", st)
	}
}

func TestPingStatusNowReportsLastFallback(t *testing.T) {
	recordFallback(errors.New("no permission to open an ICMP socket"))
	t.Cleanup(func() {
		fallbackMu.Lock()
		fallbackMsg = ""
		fallbackMu.Unlock()
	})
	st := PingStatusNow()
	if st.LastFallback != "no permission to open an ICMP socket" || st.LastFallbackAt == nil {
		t.Errorf("fallback not reported: %+v", st)
	}
	if st.Summary == "" || st.Auto == "" {
		t.Errorf("probe missing: %+v", st)
	}
}
