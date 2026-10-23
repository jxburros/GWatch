package sysmetrics

import (
	"bufio"
	"bytes"
	"regexp"
	"strconv"
	"strings"
)

// macOS keeps per-disk counters in the I/O Registry, on each
// IOBlockStorageDriver, as a "Statistics" dictionary of cumulative totals.
// `ioreg` prints them without cgo. This parser lives outside the darwin build
// so that it is tested on every platform CI runs.

var (
	ioregStatistic = regexp.MustCompile(`"([A-Za-z ()]+)"=(\d+)`)
	ioregBSDName   = regexp.MustCompile(`"BSD Name" = "([^"]+)"`)
)

// parseIORegDiskStats reads the output of
//
//	ioreg -r -c IOBlockStorageDriver -w0
//
// which lists each storage driver followed by the objects beneath it. The
// driver carries the Statistics line; the first IOMedia under it carries the
// BSD name ("disk0") that the rest of macOS calls the disk by. A driver whose
// name cannot be found is numbered in the order it appeared.
func parseIORegDiskStats(out []byte) []diskCounters {
	var disks []diskCounters
	cur := -1 // the driver whose BSD name is still being looked for
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, `"Statistics" = {`) {
			stats := map[string]uint64{}
			for _, m := range ioregStatistic.FindAllStringSubmatch(line, -1) {
				if v, err := strconv.ParseUint(m[2], 10, 64); err == nil {
					stats[m[1]] = v
				}
			}
			if len(stats) == 0 {
				continue
			}
			disks = append(disks, diskCounters{
				readBytes:  stats["Bytes (Read)"],
				writeBytes: stats["Bytes (Write)"],
				readOps:    stats["Operations (Read)"],
				writeOps:   stats["Operations (Write)"],
				// Total Time is in nanoseconds spent on requests.
				busyMillis: (stats["Total Time (Read)"] + stats["Total Time (Write)"]) / 1_000_000,
			})
			cur = len(disks) - 1
			continue
		}
		if cur >= 0 && disks[cur].name == "" {
			if m := ioregBSDName.FindStringSubmatch(line); m != nil {
				disks[cur].name = m[1]
				cur = -1
			}
		}
	}
	seen := map[string]bool{}
	for i := range disks {
		if disks[i].name == "" || seen[disks[i].name] {
			disks[i].name = "disk#" + strconv.Itoa(i)
		}
		seen[disks[i].name] = true
	}
	return disks
}
