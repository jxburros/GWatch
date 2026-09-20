package discovery

import (
	"net/netip"
	"strings"
	"testing"
)

func TestParseRangesForms(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  []string
		count int // when the list is long, check its size and its ends instead
		first string
		last  string
	}{
		{
			name:  "a single address",
			lines: []string{"192.168.1.5"},
			want:  []string{"192.168.1.5"},
		},
		{
			name:  "a /32 is that one address, network and broadcast rules not applying",
			lines: []string{"127.0.0.1/32"},
			want:  []string{"127.0.0.1"},
		},
		{
			name:  "a /31 is its two addresses",
			lines: []string{"10.0.0.0/31"},
			want:  []string{"10.0.0.0", "10.0.0.1"},
		},
		{
			name:  "a /30 drops its network and broadcast addresses",
			lines: []string{"10.0.0.0/30"},
			want:  []string{"10.0.0.1", "10.0.0.2"},
		},
		{
			name:  "a /24 is 254 hosts, .0 and .255 left out",
			lines: []string{"192.168.1.0/24"},
			count: 254,
			first: "192.168.1.1",
			last:  "192.168.1.254",
		},
		{
			name:  "a /24 written from an address inside it is masked first",
			lines: []string{"192.168.1.77/24"},
			count: 254,
			first: "192.168.1.1",
			last:  "192.168.1.254",
		},
		{
			name:  "a dashed range written out in full",
			lines: []string{"192.168.1.10-192.168.1.13"},
			want:  []string{"192.168.1.10", "192.168.1.11", "192.168.1.12", "192.168.1.13"},
		},
		{
			name:  "a dashed range abbreviated to its last octet",
			lines: []string{"192.168.1.10-12"},
			want:  []string{"192.168.1.10", "192.168.1.11", "192.168.1.12"},
		},
		{
			name:  "a dashed range crossing an octet boundary",
			lines: []string{"10.0.0.254-10.0.1.1"},
			want:  []string{"10.0.0.254", "10.0.0.255", "10.0.1.0", "10.0.1.1"},
		},
		{
			name:  "a range of one",
			lines: []string{"192.168.1.10-10"},
			want:  []string{"192.168.1.10"},
		},
		{
			name:  "several ranges on one line, and a comment",
			lines: []string{"192.168.1.1, 192.168.1.2 ; 192.168.1.3 # the three I care about"},
			want:  []string{"192.168.1.1", "192.168.1.2", "192.168.1.3"},
		},
		{
			name:  "overlapping ranges are merged and sorted",
			lines: []string{"192.168.1.3-4", "", "  192.168.1.1-3  ", "192.168.1.2"},
			want:  []string{"192.168.1.1", "192.168.1.2", "192.168.1.3", "192.168.1.4"},
		},
		{
			name:  "the biggest block that fits",
			lines: []string{"10.0.0.0/20"},
			count: 4094,
			first: "10.0.0.1",
			last:  "10.0.15.254",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseRanges(c.lines)
			if err != nil {
				t.Fatalf("ParseRanges(%q): %v", c.lines, err)
			}
			if c.want != nil {
				if len(got) != len(c.want) {
					t.Fatalf("got %d addresses %v, want %d %v", len(got), got, len(c.want), c.want)
				}
				for i, w := range c.want {
					if got[i] != netip.MustParseAddr(w) {
						t.Errorf("address %d: got %s, want %s", i, got[i], w)
					}
				}
				return
			}
			if len(got) != c.count {
				t.Fatalf("got %d addresses, want %d", len(got), c.count)
			}
			if got[0].String() != c.first || got[len(got)-1].String() != c.last {
				t.Errorf("range runs %s…%s, want %s…%s", got[0], got[len(got)-1], c.first, c.last)
			}
		})
	}
}

func TestParseRangesRefusals(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		says  string // a phrase the message must carry, so it stays readable
	}{
		{"nothing at all", nil, "at least one range"},
		{"only blank lines", []string{"", "   ", "# just a comment"}, "at least one range"},
		{"a name rather than an address", []string{"nas.local"}, "not a range GWatch understands"},
		{"a half-typed address", []string{"192.168.1."}, "not a range GWatch understands"},
		{"an octet out of range", []string{"192.168.1.300"}, "not a range GWatch understands"},
		{"a prefix that is not a prefix", []string{"192.168.1.0/33"}, "not a range GWatch understands"},
		{"IPv6, single", []string{"fe80::1"}, "IPv6"},
		{"IPv6, a prefix", []string{"2001:db8::/64"}, "IPv6"},
		{"IPv6, inside a dashed range", []string{"fe80::1-fe80::9"}, "IPv6"},
		{"a backwards range", []string{"192.168.1.50-192.168.1.10"}, "runs backwards"},
		{"a backwards shorthand", []string{"192.168.1.50-10"}, "runs backwards"},
		{"a last octet that is not one", []string{"192.168.1.10-999"}, "last octet"},
		{"a block that is too big", []string{"10.0.0.0/8"}, "limit for one run is 4096"},
		{"a dashed range that is too long", []string{"10.0.0.1-10.1.0.1"}, "limit for one run is 4096"},
		{"several ranges that add up to too many", []string{"10.0.0.0/20", "10.1.0.0/21"}, "more than 4096 addresses"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseRanges(c.lines)
			if err == nil {
				t.Fatalf("ParseRanges(%q) was accepted", c.lines)
			}
			if !strings.Contains(err.Error(), c.says) {
				t.Errorf("message %q does not mention %q", err, c.says)
			}
		})
	}
}

// The cap is the promise the modal makes to the person typing, so it is worth
// pinning to the exact boundary rather than to "about four thousand".
func TestParseRangesCapIsExact(t *testing.T) {
	// A /20 is 4094 hosts, so two more addresses land exactly on the cap …
	got, err := ParseRanges([]string{"10.0.0.0/20", "10.1.0.0/31"})
	if err != nil {
		t.Fatalf("4094 + 2 addresses is exactly the cap and should be allowed: %v", err)
	}
	if len(got) != MaxAddresses {
		t.Fatalf("got %d addresses, want exactly %d", len(got), MaxAddresses)
	}
	// … and one more is one too many.
	if _, err := ParseRanges([]string{"10.0.0.0/20", "10.1.0.0/31", "10.1.0.2"}); err == nil {
		t.Fatal("4097 addresses should be refused")
	}
}
