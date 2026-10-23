package sysmetrics

import "testing"

func TestParseIORegDiskStats(t *testing.T) {
	out := []byte(`+-o IOBlockStorageDriver  <class IOBlockStorageDriver, id 0x100000331, registered, matched, active, busy 0 (0 ms), retain 6>
  | {
  |   "IOClass" = "IOBlockStorageDriver"
  |   "Statistics" = {"Operations (Write)"=4553433,"Latency Time (Write)"=0,"Bytes (Read)"=103735529472,"Errors (Write)"=0,"Total Time (Read)"=1081573574181,"Latency Time (Read)"=0,"Retries (Read)"=0,"Errors (Read)"=0,"Total Time (Write)"=1452547697285,"Bytes (Write)"=96285831168,"Operations (Read)"=3398123,"Retries (Write)"=0}
  | }
  |
  +-o APPLE SSD AP0512Q Media  <class IOMedia, id 0x100000332, registered, matched, active, busy 0 (0 ms), retain 12>
    | {
    |   "Content" = "GUID_partition_scheme"
    |   "BSD Name" = "disk0"
    | }
    +-o IOGUIDPartitionScheme  <class IOGUIDPartitionScheme>
      +-o Container@2  <class IOMedia>
        | {
        |   "BSD Name" = "disk0s2"
        | }
+-o IOBlockStorageDriver  <class IOBlockStorageDriver, id 0x100000500>
  | {
  |   "Statistics" = {"Bytes (Read)"=1000,"Bytes (Write)"=2000,"Operations (Read)"=3,"Operations (Write)"=4,"Total Time (Read)"=5000000,"Total Time (Write)"=7000000}
  | }
`)
	got := parseIORegDiskStats(out)
	if len(got) != 2 {
		t.Fatalf("got %d disks, want 2: %+v", len(got), got)
	}
	d := got[0]
	if d.name != "disk0" || d.readBytes != 103735529472 || d.writeBytes != 96285831168 ||
		d.readOps != 3398123 || d.writeOps != 4553433 || d.busyMillis != (1081573574181+1452547697285)/1_000_000 {
		t.Errorf("first disk = %+v", d)
	}
	if got[1].name != "disk#1" || got[1].busyMillis != 12 || got[1].readBytes != 1000 {
		t.Errorf("second disk = %+v", got[1])
	}
}
