package logging

import (
	"bytes"
	"strings"
	"testing"
)

func TestJournalPrioritiesAndFileTimestamps(t *testing.T) {
	t.Setenv("INVOCATION_ID", "systemd-test")
	var output bytes.Buffer
	l, err := New(t.TempDir(), &output)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.Warnf("warning %d", 42)
	l.Errorf("failed")
	if output.String() != "<4>warning 42\n<3>failed\n" {
		t.Fatal(output.String())
	}
	if !strings.Contains(l.Recent(1)[0], "ERROR failed") {
		t.Fatal("file/ring format changed")
	}
}
