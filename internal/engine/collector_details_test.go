package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

// A collector is another machine: what it reports is bounded before it is
// stored, details included. Without that one result could carry most of the
// four-megabyte batch limit, kept with the history and sent with every node
// view.
func TestCollectorResultDetailsAreBounded(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	many := make([]string, 10000)
	for i := range many {
		many[i] = big[:1000]
	}
	in := model.Result{Success: true, Details: model.ResultDetails{
		FinalURL: big, ContentValue: big, JSONValue: big, Output: big,
		ResolvedValues: many, RTTs: make([]float64, 100000),
		SNMP:   []model.SNMPValue{{OID: big, Raw: big}},
		Cert:   &model.CertInfo{Subject: big, DNSNames: many},
		Host:   &model.HostMetrics{Hostname: big},
		Matter: []model.MatterValue{{Raw: big}},
	}}
	out := cleanCollectorResult(in, 7)
	b, _ := json.Marshal(out)
	if len(b) > 200<<10 {
		t.Fatalf("a cleaned result is still %d bytes", len(b))
	}
	if out.Details.Host != nil || out.Details.Matter != nil {
		t.Error("details a collector never produces were kept")
	}
	if !strings.HasPrefix(out.Details.FinalURL, "xxx") || len(out.Details.SNMP) != 1 {
		t.Error("the bounded details should still be there")
	}
}
