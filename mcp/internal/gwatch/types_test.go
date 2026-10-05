package gwatch

import (
	"encoding/json"
	"testing"
)

func TestOverviewPreservesGroupAndCertificateDetails(t *testing.T) {
	var ov Overview
	if err := json.Unmarshal([]byte(`{"groups":[{"name":"Home","paused":2,"maintenance":3}],"certWarnings":[{"valid":false,"notAfter":"2026-10-01T00:00:00Z","error":"hostname mismatch","daysRemaining":365}]}`), &ov); err != nil {
		t.Fatal(err)
	}
	if len(ov.Groups) != 1 || ov.Groups[0].Paused != 2 || ov.Groups[0].Maintenance != 3 {
		t.Fatalf("group fields lost: %+v", ov.Groups)
	}
	if len(ov.CertWarnings) != 1 || ov.CertWarnings[0].Valid || ov.CertWarnings[0].NotAfter.IsZero() || ov.CertWarnings[0].Error != "hostname mismatch" {
		t.Fatalf("certificate fields lost: %+v", ov.CertWarnings)
	}
}
