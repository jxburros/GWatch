package api

import (
	"context"
	"encoding/csv"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jxburros/GWatch/internal/model"
)

// credentialedNode is a node an administrator set up with an HTTP login probe
// carrying a request body and an SNMP check (which reads the node host)
// carrying a community string.
func credentialedNode() model.Node {
	return model.Node{Name: "Router", Host: "192.168.1.1", Enabled: true, Importance: model.ImportanceNormal, Checks: []model.Check{
		{Type: model.CheckHTTP, Name: "Login", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5,
			Config: model.CheckConfig{Target: "https://192.168.1.1/login", Method: "POST", Body: "user=admin&pass=B0DYSECRET"}},
		{Type: model.CheckSNMP, Name: "SNMP", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5,
			Config: model.CheckConfig{SNMPVersion: "2c", SNMPPort: 161, SNMPCommunity: "C0MMUNITYSECRET",
				SNMPOIDs: []model.SNMPOID{{OID: "1.3.6.1.2.1.1.3.0", Name: "Uptime", Kind: "gauge"}}}},
	}}
}

// Finding [1]: a read-write key may not point a credentialed check somewhere
// new while leaving its stored credentials to be filled back in.
func TestAPIKeyCannotRetargetStoredCredentials(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	admin := bootstrapAdmin(t, ts, "admin", "correct horse battery")
	key := mintKey(t, ts, admin, "integration", "readwrite")
	var created nodeDoc
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/nodes", credentialedNode(), &created); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	path := fmt.Sprintf("/api/nodes/%d", created.ID)
	// What the key sees: the body stripped, the community masked.
	read := func() model.Node {
		var doc nodeDoc
		if code, body, _ := as(t, ts, creds{APIKey: key}, "GET", path, nil, &doc); code != 200 {
			t.Fatalf("key read: %d %s", code, body)
		}
		return doc.Node
	}

	retargets := map[string]func(n *model.Node){
		"node host (SNMP)": func(n *model.Node) { n.Host = "203.0.113.9" },
		"SNMP port":        func(n *model.Node) { n.Checks[1].Config.SNMPPort = 1161 },
		"SNMP target":      func(n *model.Node) { n.Checks[1].Config.Target = "203.0.113.9" },
		"HTTP target":      func(n *model.Node) { n.Checks[0].Config.Target = "https://203.0.113.9/login" },
		"HTTP scheme":      func(n *model.Node) { n.Checks[0].Config.Target = "http://192.168.1.1/login" },
		"HTTP port":        func(n *model.Node) { n.Checks[0].Config.Target = "https://192.168.1.1:8443/login" },
		"HTTP method":      func(n *model.Node) { n.Checks[0].Config.Method = "PUT" },
		"Host header":      func(n *model.Node) { n.Checks[0].Config.Headers = map[string]string{"Host": "attacker.example"} },
		"check type":       func(n *model.Node) { n.Checks[0].Type = model.CheckKeyword; n.Checks[0].Config.Keyword = "ok" },
	}
	for name, mutate := range retargets {
		n := read()
		mutate(&n)
		code, body, _ := as(t, ts, creds{APIKey: key}, "PUT", path, n, nil)
		if code != 403 || !strings.Contains(body, "administrator") {
			t.Errorf("%s: a key retargeting a credentialed check should be refused, got %d %s", name, code, body)
		}
	}
	for _, c := range created.Checks {
		stored, err := srv.Store.GetCheck(ctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		if c.Type == model.CheckHTTP && (stored.Config.Target != "https://192.168.1.1/login" || stored.Config.Body != "user=admin&pass=B0DYSECRET") {
			t.Fatalf("the HTTP check changed under a refused update: %+v", stored.Config)
		}
	}
	if n, _ := srv.Store.GetNode(ctx, created.ID); n.Host != "192.168.1.1" {
		t.Fatalf("the node host changed under a refused update: %q", n.Host)
	}

	// An edit that leaves the destination alone still keeps the stored
	// credentials, so the key's ordinary writes keep working.
	n := read()
	n.Name = "Core router"
	if code, body, _ := as(t, ts, creds{APIKey: key}, "PUT", path, n, nil); code != 200 {
		t.Fatalf("a key renaming the node should work: %d %s", code, body)
	}
	if stored, _ := srv.Store.GetCheck(ctx, created.Checks[1].ID); stored.Config.SNMPCommunity != "C0MMUNITYSECRET" {
		t.Fatalf("community lost on a rename: %q", stored.Config.SNMPCommunity)
	}

	// A key that sends every credential itself relies on nothing stored, and
	// may retarget.
	n = read()
	n.Checks[0].Config.Target = "https://203.0.113.9/login"
	n.Checks[0].Config.Body = "user=key&pass=its-own"
	if code, body, _ := as(t, ts, creds{APIKey: key}, "PUT", path, n, nil); code != 200 {
		t.Fatalf("a key retargeting with fresh credentials should work: %d %s", code, body)
	}

	// The administrator in the browser keeps the old behaviour.
	var doc nodeDoc
	as(t, ts, creds{Cookie: admin}, "GET", path, nil, &doc)
	doc.Node.Host = "192.168.1.254"
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "PUT", path, doc.Node, nil); code != 200 {
		t.Fatalf("an admin retargeting should work: %d %s", code, body)
	}
	if stored, _ := srv.Store.GetCheck(ctx, created.Checks[1].ID); stored.Config.SNMPCommunity != "C0MMUNITYSECRET" {
		t.Fatalf("the admin's retarget lost the community: %q", stored.Config.SNMPCommunity)
	}
}

// Finding [2]: the enable endpoint answers with the same projection as every
// other read, never the opened configuration.
func TestEnableCheckDoesNotLeakSecrets(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := bootstrapAdmin(t, ts, "admin", "correct horse battery")
	key := mintKey(t, ts, admin, "integration", "readwrite")
	secrets := []string{"HDRSECRET", "B0DYSECRET", "C0MMUNITYSECRET", "AUTHSECRET", "PRIVSECRET", "ENVSECRET", "T0KENSECRET", "metrics-url-secret"}
	node := model.Node{Name: "Everything", Host: "192.168.1.1", Enabled: true, Importance: model.ImportanceNormal, Checks: []model.Check{
		{Type: model.CheckHTTP, Name: "HTTP", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5,
			Config: model.CheckConfig{Target: "https://192.168.1.1/", Method: "POST", Body: "B0DYSECRET", Headers: map[string]string{"Authorization": "Bearer HDRSECRET"}}},
		{Type: model.CheckSNMP, Name: "SNMP v2c", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5,
			Config: model.CheckConfig{SNMPVersion: "2c", SNMPCommunity: "C0MMUNITYSECRET", SNMPOIDs: []model.SNMPOID{{OID: "1.3.6.1.2.1.1.3.0", Name: "Uptime"}}}},
		{Type: model.CheckSNMP, Name: "SNMP v3", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5,
			Config: model.CheckConfig{SNMPVersion: "3", SNMPUser: "mon", SNMPAuthProto: "SHA", SNMPAuthPass: "AUTHSECRET", SNMPPrivProto: "AES", SNMPPrivPass: "PRIVSECRET", SNMPOIDs: []model.SNMPOID{{OID: "1.3.6.1.2.1.1.3.0", Name: "Uptime"}}}},
		{Type: model.CheckCustom, Name: "Script", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5,
			Config: model.CheckConfig{Command: "echo message=up", Env: map[string]string{"PASSWORD": "ENVSECRET"}}},
		{Type: model.CheckSystem, Name: "NAS", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5,
			Config: model.CheckConfig{HostSource: model.HostSourceURL, MetricsURL: "https://192.168.1.5/metrics?k=metrics-url-secret", MetricsToken: "T0KENSECRET"}},
	}}
	var created nodeDoc
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/nodes", node, &created); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	for _, who := range []struct {
		name string
		c    creds
	}{{"key", creds{APIKey: key}}, {"admin", creds{Cookie: admin}}} {
		for _, c := range created.Checks {
			code, body, _ := as(t, ts, who.c, "POST", fmt.Sprintf("/api/checks/%d/enable", c.ID), map[string]bool{"enabled": false}, nil)
			if code != 200 {
				t.Fatalf("%s disabling %s: %d %s", who.name, c.Name, code, body)
			}
			for _, s := range secrets {
				// A browser administrator still sees the request headers,
				// body, environment and URL the editor needs; the masked
				// scalar secrets are what must never come back to anyone.
				if who.name == "admin" && (s == "HDRSECRET" || s == "B0DYSECRET" || s == "ENVSECRET" || s == "metrics-url-secret") {
					continue
				}
				if strings.Contains(body, s) {
					t.Errorf("%s: enabling %s handed back %s:\n%s", who.name, c.Name, s, body)
				}
			}
		}
	}
}

// Finding [10]: the stored-check guard covers run, enable, duplicate, bulk
// and node run, not only create, edit and test.
func TestAPIKeyCannotDriveStoredCodeChecks(t *testing.T) {
	ts, _ := newTestServer(t)
	admin := bootstrapAdmin(t, ts, "admin", "correct horse battery")
	key := mintKey(t, ts, admin, "integration", "readwrite")
	k := creds{APIKey: key}
	custom := func(enabled bool) model.Check {
		return model.Check{Type: model.CheckCustom, Name: "Script", Enabled: enabled, IntervalSeconds: 60, TimeoutSeconds: 5,
			Config: model.CheckConfig{Command: "echo message=up"}}
	}
	ping := model.Check{Type: model.CheckPing, Name: "Ping", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}
	mk := func(name string, enabled bool, cs ...model.Check) nodeDoc {
		var doc nodeDoc
		n := model.Node{Name: name, Host: "127.0.0.1", Enabled: enabled, Importance: model.ImportanceNormal, Checks: cs}
		if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", "/api/nodes", n, &doc); code != 201 {
			t.Fatalf("create %s: %d %s", name, code, body)
		}
		// POST /api/nodes defaults a node to enabled only when the field is
		// absent; set it explicitly so the fixture is what it says.
		if !enabled {
			as(t, ts, creds{Cookie: admin}, "POST", fmt.Sprintf("/api/nodes/%d/enable", doc.ID), map[string]bool{"enabled": false}, nil)
		}
		return doc
	}
	// A: enabled node, switched-off custom check, enabled ping.
	a := mk("A", true, custom(false), ping)
	// B: switched-off node holding a switched-on custom check.
	b := mk("B", false, custom(true), ping)
	// C: enabled node with a switched-on custom check beside a ping.
	c := mk("C", true, custom(true), ping)
	aCustom, aPing := a.Checks[0].ID, a.Checks[1].ID

	expect := func(what string, want int, method, path string, body any) {
		t.Helper()
		if code, resp, _ := as(t, ts, k, method, path, body, nil); code != want {
			t.Errorf("%s: want %d, got %d %s", what, want, code, resp)
		}
	}
	expect("run a disabled custom check", 403, "POST", fmt.Sprintf("/api/checks/%d/run", aCustom), nil)
	expect("run an enabled custom check", 403, "POST", fmt.Sprintf("/api/checks/%d/run", c.Checks[0].ID), nil)
	expect("enable a custom check", 403, "POST", fmt.Sprintf("/api/checks/%d/enable", aCustom), map[string]bool{"enabled": true})
	expect("disable a custom check", 200, "POST", fmt.Sprintf("/api/checks/%d/enable", c.Checks[0].ID), map[string]bool{"enabled": false})
	expect("duplicate a node with a custom check", 403, "POST", fmt.Sprintf("/api/nodes/%d/duplicate", a.ID), nil)
	expect("enable a node with an enabled custom check", 403, "POST", fmt.Sprintf("/api/nodes/%d/enable", b.ID), map[string]bool{"enabled": true})
	expect("run a disabled node", 409, "POST", fmt.Sprintf("/api/nodes/%d/run", b.ID), nil)
	expect("bulk-edit a custom check", 403, "PATCH", "/api/nodes/bulk", map[string]any{"checkIds": []int64{aCustom}, "check": map[string]any{"enabled": true}})
	expect("bulk-edit a node's checks including a custom one", 403, "PATCH", "/api/nodes/bulk", map[string]any{"nodeIds": []int64{a.ID}, "check": map[string]any{"intervalSeconds": 120}})
	expect("bulk-enable a node with an enabled custom check", 403, "PATCH", "/api/nodes/bulk", map[string]any{"nodeIds": []int64{b.ID}, "node": map[string]any{"enabled": true}})

	// Ordinary checks are unaffected.
	expect("run a ping check", 200, "POST", fmt.Sprintf("/api/checks/%d/run", aPing), nil)
	expect("bulk-edit a ping check", 200, "PATCH", "/api/nodes/bulk", map[string]any{"checkIds": []int64{aPing}, "check": map[string]any{"intervalSeconds": 120}})
	expect("disable a ping check", 200, "POST", fmt.Sprintf("/api/checks/%d/enable", aPing), map[string]bool{"enabled": false})
	expect("run a disabled ping check", 409, "POST", fmt.Sprintf("/api/checks/%d/run", aPing), nil)
	expect("enable a ping check", 200, "POST", fmt.Sprintf("/api/checks/%d/enable", aPing), map[string]bool{"enabled": true})

	// A node run through a key skips the custom check and runs the rest.
	as(t, ts, creds{Cookie: admin}, "POST", fmt.Sprintf("/api/checks/%d/enable", c.Checks[0].ID), map[string]bool{"enabled": true}, nil)
	var results []model.Result
	if code, body, _ := as(t, ts, k, "POST", fmt.Sprintf("/api/nodes/%d/run", c.ID), nil, &results); code != 200 {
		t.Fatalf("key node run: %d %s", code, body)
	}
	if len(results) != 1 || results[0].CheckID != c.Checks[1].ID {
		t.Fatalf("a key's node run should run only the ping check, got %+v", results)
	}

	// The administrator in the browser may still do all of it.
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", fmt.Sprintf("/api/checks/%d/run", aCustom), nil, nil); code != 200 {
		t.Fatalf("admin run of a custom check: %d %s", code, body)
	}
	if code, body, _ := as(t, ts, creds{Cookie: admin}, "POST", fmt.Sprintf("/api/nodes/%d/duplicate", a.ID), nil, nil); code != 201 {
		t.Fatalf("admin duplicate: %d %s", code, body)
	}
	if code, _, _ := as(t, ts, creds{Cookie: admin}, "POST", fmt.Sprintf("/api/nodes/%d/run", c.ID), nil, &results); code != 200 || len(results) != 2 {
		t.Fatalf("admin node run should run both checks: %d %+v", code, results)
	}
}

// Finding [12]: a node holds a bounded number of checks.
func TestNodeCheckCountIsBounded(t *testing.T) {
	ts, _ := newTestServer(t)
	cs := make([]map[string]any, maxChecksPerNode+1)
	for i := range cs {
		cs[i] = map[string]any{"type": "ping", "name": fmt.Sprintf("p%d", i), "intervalSeconds": 60}
	}
	if code := call(t, ts, "POST", "/api/nodes", map[string]any{"name": "Wide", "host": "127.0.0.1", "checks": cs}, nil); code != 400 {
		t.Fatalf("a node over the check limit should be refused, got %d", code)
	}
	if code := call(t, ts, "POST", "/api/nodes", map[string]any{"name": "Wide", "host": "127.0.0.1", "checks": cs[:maxChecksPerNode]}, nil); code != 201 {
		t.Fatalf("a node at the limit should be accepted, got %d", code)
	}
}

func TestCSVTextNeutralizesFormulas(t *testing.T) {
	for in, want := range map[string]string{
		"=1+1":        "'=1+1",
		"+cmd":        "'+cmd",
		"-2":          "'-2",
		"@SUM(A1)":    "'@SUM(A1)",
		"\tx":         "'\tx",
		"\rx":         "'\rx",
		"plain":       "plain",
		"":            "",
		"a=b":         "a=b",
		"https://x/y": "https://x/y",
	} {
		if got := csvText(in); got != want {
			t.Errorf("csvText(%q) = %q, want %q", in, got, want)
		}
	}
}

// Finding [17]: every text cell of the events and results exports is safe to
// open in a spreadsheet; the numbers GWatch writes itself are left alone.
func TestCSVExportsNeutralizeFormulas(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx := context.Background()
	admin := bootstrapAdmin(t, ts, "admin", "correct horse battery")
	key := mintKey(t, ts, admin, "integration", "readwrite")

	var node nodeDoc
	n := map[string]any{"name": "+SUM(1,2)", "host": "127.0.0.1", "checks": []map[string]any{{"type": "ping", "name": "@ping", "intervalSeconds": 60}}}
	if code, body, _ := as(t, ts, creds{APIKey: key}, "POST", "/api/nodes", n, &node); code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	if code, body, _ := as(t, ts, creds{APIKey: key}, "POST", "/api/events/note", map[string]any{"nodeId": node.ID, "text": `=HYPERLINK("http://evil.example","x")`}, nil); code != 201 {
		t.Fatalf("note: %d %s", code, body)
	}
	cid := node.Checks[0].ID
	lat := -1.5
	if _, err := srv.Store.InsertResult(ctx, model.Result{CheckID: cid, Timestamp: time.Now(), Status: model.StatusDown,
		Message: "-cmd|' /C calc'!A0", Error: "\t=evil()", LatencyMS: &lat, Attempts: 1,
		Details: model.ResultDetails{FinalURL: "@http://evil.example/"}}); err != nil {
		t.Fatal(err)
	}

	assertSafe := func(path string, numeric map[int]bool) [][]string {
		t.Helper()
		code, body, _ := as(t, ts, creds{APIKey: key}, "GET", path, nil, nil)
		if code != 200 {
			t.Fatalf("%s: %d %s", path, code, body)
		}
		rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, row := range rows[1:] {
			for i, cell := range row {
				if numeric[i] || cell == "" {
					continue
				}
				if strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
					t.Errorf("%s: column %s starts a formula: %q", path, rows[0][i], cell)
				}
			}
		}
		return rows
	}

	rows := assertSafe("/api/export/events.csv", nil)
	found := false
	for _, row := range rows[1:] {
		if row[5] == `'=HYPERLINK("http://evil.example","x")` && row[2] == "'+SUM(1,2)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the note should be exported with its text kept behind an apostrophe:\n%v", rows)
	}

	// Columns 5–10 are latencies and loss GWatch formats itself.
	rows = assertSafe(fmt.Sprintf("/api/export/results.csv?checkId=%d", cid), map[int]bool{5: true, 6: true, 7: true, 8: true, 9: true, 10: true})
	last := rows[len(rows)-1]
	if last[3] != "'-cmd|' /C calc'!A0" || last[4] != "'\t=evil()" || last[12] != "'@http://evil.example/" || last[5] != "-1.50" {
		t.Fatalf("unexpected results row: %q", last)
	}
}
