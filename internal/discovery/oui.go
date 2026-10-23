package discovery

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// The vendor list is the IEEE's registry of MAC address prefixes, reduced by
// internal/discovery/ouigen to one "PREFIX<tab>Name" line per 24-bit block
// and gzipped (about 340 KB for some forty thousand makers). It is read the
// first time a sweep asks for a vendor and kept as two sorted slices, which
// is a third of the memory a map of the same entries would take.
//
//go:generate go run ./ouigen -out oui.txt.gz
//go:embed oui.txt.gz
var ouiData []byte

var (
	ouiOnce     sync.Once
	ouiPrefixes []uint32
	ouiNames    []string
)

func loadOUI() {
	zr, err := gzip.NewReader(bytes.NewReader(ouiData))
	if err != nil {
		return
	}
	defer zr.Close()
	names := map[string]string{} // interned, since many makers own dozens of blocks
	sc := bufio.NewScanner(zr)
	for sc.Scan() {
		line := sc.Text()
		hex, name, ok := strings.Cut(line, "\t")
		if !ok || len(hex) != 6 {
			continue
		}
		v, err := strconv.ParseUint(hex, 16, 32)
		if err != nil {
			continue
		}
		if n, seen := names[name]; seen {
			name = n
		} else {
			names[name] = name
		}
		ouiPrefixes = append(ouiPrefixes, uint32(v))
		ouiNames = append(ouiNames, name)
	}
	// The generator writes the file sorted; sorting again costs nothing and
	// keeps the binary search honest if someone edits it by hand.
	if !sort.SliceIsSorted(ouiPrefixes, func(i, j int) bool { return ouiPrefixes[i] < ouiPrefixes[j] }) {
		idx := make([]int, len(ouiPrefixes))
		for i := range idx {
			idx[i] = i
		}
		sort.Slice(idx, func(a, b int) bool { return ouiPrefixes[idx[a]] < ouiPrefixes[idx[b]] })
		p, n := make([]uint32, len(idx)), make([]string, len(idx))
		for i, j := range idx {
			p[i], n[i] = ouiPrefixes[j], ouiNames[j]
		}
		ouiPrefixes, ouiNames = p, n
	}
}

// RandomisedMAC is the vendor shown for a locally administered address: the
// private, per-network address phones, tablets and laptops now use by
// default, which by design names no maker.
const RandomisedMAC = "Private (randomised) address"

// Vendor names the maker of the network card behind a MAC address, or ""
// when the prefix is not in the registry. An address with the locally
// administered bit set is not a maker's at all, and says so.
func Vendor(mac string) string {
	b, ok := parseMAC(mac)
	if !ok {
		return ""
	}
	if b[0]&0x02 != 0 {
		return RandomisedMAC
	}
	ouiOnce.Do(loadOUI)
	key := uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
	i := sort.Search(len(ouiPrefixes), func(i int) bool { return ouiPrefixes[i] >= key })
	if i < len(ouiPrefixes) && ouiPrefixes[i] == key {
		return ouiNames[i]
	}
	return ""
}

// parseMAC reads the first three bytes of a MAC address written with colons,
// dashes or nothing between the bytes ("b8:27:eb:..", "B8-27-EB-..").
func parseMAC(mac string) ([3]byte, bool) {
	var out [3]byte
	hex := strings.NewReplacer(":", "", "-", "", ".", "").Replace(strings.TrimSpace(mac))
	if len(hex) < 6 {
		return out, false
	}
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
		if err != nil {
			return out, false
		}
		out[i] = byte(v)
	}
	return out, true
}

// normalizeMAC writes a MAC address the one way the results show it:
// lower-case, colon-separated. It returns "" for anything that is not six
// bytes, and for the all-zero address the kernel records for a neighbour it
// never resolved.
func normalizeMAC(mac string) string {
	hex := strings.ToLower(strings.NewReplacer(":", "", "-", "", ".", "").Replace(strings.TrimSpace(mac)))
	if len(hex) != 12 || hex == "000000000000" {
		return ""
	}
	if _, err := strconv.ParseUint(hex, 16, 64); err != nil {
		return ""
	}
	var b strings.Builder
	for i := 0; i < 12; i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(hex[i : i+2])
	}
	return b.String()
}
