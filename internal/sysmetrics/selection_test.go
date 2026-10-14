package sysmetrics

import (
	"reflect"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

func f(v float64) *float64 { return &v }

func TestAutomaticInterfacesLeaveOutScaffolding(t *testing.T) {
	real := []string{"eth0", "ens3", "enp3s0", "wlan0", "wlp2s0", "en0", "Ethernet", "Wi-Fi", "Local Area Connection", "bond0", "apc-lan", "apple-bridge-net"}
	for _, name := range real {
		if !InterfaceIsAutomatic(model.HostInterface{Name: name, Up: true}) {
			t.Errorf("%q is a real connection and should be automatic", name)
		}
	}
	virtual := []string{
		"lo", "lo0", "veth1a2b3c", "docker0", "br-0a1b2c3d4e5f", "virbr0", "vmnet8", "tun0", "tap0", "utun3",
		"awdl0", "llw0", "ap1", "gif0", "stf0", "wg0", "flannel.1", "cni0", "cali1234", "kube-ipvs0", "ppp0",
		"Loopback Pseudo-Interface 1", "Teredo Tunneling Pseudo-Interface", "isatap.{ABC-123}",
		"WAN Miniport (IP)", "vEthernet (Default Switch)", "Local Area Connection* 3",
		"Bluetooth Network Connection", "Microsoft Wi-Fi Direct Virtual Adapter", "TAP-Windows Adapter V9",
	}
	for _, name := range virtual {
		if InterfaceIsAutomatic(model.HostInterface{Name: name, Up: true, RxBytes: 5}) {
			t.Errorf("%q is scaffolding and should not be automatic", name)
		}
	}
	// A real name that is down and has never carried a byte is an unplugged
	// port nobody is using.
	if InterfaceIsAutomatic(model.HostInterface{Name: "eth1"}) {
		t.Error("an idle, down interface should not be automatic")
	}
	if !InterfaceIsAutomatic(model.HostInterface{Name: "eth1", RxBytes: 1}) {
		t.Error("a down interface that has carried traffic should stay, so its loss is visible")
	}
}

func sample() model.HostMetrics {
	return model.HostMetrics{
		Hostname: "nas",
		CPU:      model.HostCPU{Cores: 4, UsagePct: f(10), Load1: f(1), LoadPerCore: f(0.25)},
		Memory:   model.HostMemory{TotalBytes: 100, UsedBytes: 50, UsedPct: 50, SwapTotalBytes: 10, SwapUsedBytes: 1, SwapUsedPct: f(10)},
		Filesystems: []model.HostFilesystem{
			{Mount: "/", UsedPct: 40, InodesUsedPct: f(5), InodesTotal: 9},
			{Mount: "/data", UsedPct: 90},
		},
		Interfaces: []model.HostInterface{
			{Name: "eth0", Up: true, Addresses: []string{"192.168.1.9"}, SpeedMbit: 1000},
			{Name: "docker0", Up: true},
			{Name: "tun0", Up: true},
		},
		Disks: []model.HostDiskIO{{Name: "sda"}, {Name: "sdb"}},
	}
}

func names(m model.HostMetrics) (nics, disks, mounts []string) {
	for _, n := range m.Interfaces {
		nics = append(nics, n.Name)
	}
	for _, d := range m.Disks {
		disks = append(disks, d.Name)
	}
	for _, x := range m.Filesystems {
		mounts = append(mounts, x.Mount)
	}
	return
}

func TestApplyWithNothingChosenIsAutomatic(t *testing.T) {
	got := Apply(sample(), model.CollectSelection{}, true)
	nics, disks, mounts := names(got)
	if !reflect.DeepEqual(nics, []string{"eth0"}) || len(disks) != 2 || len(mounts) != 2 {
		t.Errorf("automatic selection: nics=%v disks=%v mounts=%v", nics, disks, mounts)
	}
	if got.CPU.UsagePct == nil || got.Memory.TotalBytes == 0 {
		t.Error("every family is on by default")
	}
	// Narrowing to a machine's own ceiling is not allowed to hide anything the
	// automatic selection would, or the inventory could never offer it.
	ceil := Apply(sample(), model.CollectSelection{}, false)
	if n, _, _ := names(ceil); len(n) != 3 {
		t.Errorf("the ceiling pass hid interfaces: %v", n)
	}
}

func TestApplyKeepsOnlyWhatIsChosen(t *testing.T) {
	sel := model.CollectSelection{
		Metrics:    []string{model.MetricCPU, model.MetricNet, model.MetricDisk},
		Interfaces: []string{"tun0"},
		Mounts:     []string{"/data"},
	}
	got := Apply(sample(), sel, true)
	nics, disks, mounts := names(got)
	if !reflect.DeepEqual(nics, []string{"tun0"}) {
		t.Errorf("an explicitly chosen tunnel must be kept: %v", nics)
	}
	if len(disks) != 0 {
		t.Errorf("diskio is not a chosen family, got %v", disks)
	}
	if !reflect.DeepEqual(mounts, []string{"/data"}) {
		t.Errorf("mounts: %v", mounts)
	}
	if got.CPU.UsagePct == nil || got.CPU.LoadPerCore != nil {
		t.Errorf("cpu on, load off: %+v", got.CPU)
	}
	if got.Memory.TotalBytes != 0 || got.Memory.SwapUsedPct != nil {
		t.Errorf("memory and swap are off: %+v", got.Memory)
	}
}

func TestApplyInodesTravelWithTheFilesystem(t *testing.T) {
	onlyDisk := Apply(sample(), model.CollectSelection{Metrics: []string{model.MetricDisk}}, true)
	if onlyDisk.Filesystems[0].InodesUsedPct != nil || onlyDisk.Filesystems[0].InodesTotal != 0 {
		t.Error("choosing disk space alone should not send inode figures")
	}
	withInodes := Apply(sample(), model.CollectSelection{Metrics: []string{model.MetricInodes}}, true)
	if len(withInodes.Filesystems) != 2 || withInodes.Filesystems[0].InodesUsedPct == nil {
		t.Error("choosing inodes keeps the filesystem entry with its inode figures")
	}
	none := Apply(sample(), model.CollectSelection{Metrics: []string{model.MetricCPU}}, true)
	if none.Filesystems != nil {
		t.Error("neither disk nor inodes chosen: no filesystems")
	}
}

func TestApplyAnImpossibleSelectionSendsNothingRatherThanEverything(t *testing.T) {
	impossible := model.CollectSelection{Interfaces: []string{"eth0"}}.Effective(model.CollectSelection{Interfaces: []string{"eth9"}})
	got := Apply(sample(), impossible, true)
	if len(got.Interfaces) != 0 {
		t.Errorf("a ceiling and a choice with nothing in common must send no interfaces, got %v", got.Interfaces)
	}
}

func TestInventoryListsEverythingWithItsAutomaticFlag(t *testing.T) {
	inv := InventoryOf(sample())
	if len(inv.Interfaces) != 3 || len(inv.Disks) != 2 || len(inv.Mounts) != 2 {
		t.Fatalf("inventory %+v", inv)
	}
	byName := map[string]model.InventoryItem{}
	for _, i := range inv.Interfaces {
		byName[i.Name] = i
	}
	if !byName["eth0"].Auto || byName["docker0"].Auto || byName["tun0"].Auto {
		t.Errorf("automatic flags: %+v", byName)
	}
	if byName["eth0"].Detail != "192.168.1.9 · 1000 Mbit/s" {
		t.Errorf("detail %q", byName["eth0"].Detail)
	}
}
