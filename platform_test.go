package main

import (
	"context"
	"github.com/jxburros/GWatch/internal/logging"
	"github.com/jxburros/GWatch/internal/model"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestPortSettingAndRebindFallback(t *testing.T) {
	if got := effectiveListen("127.0.0.1:7230", model.GeneralSettings{ListenPort: 8123}); got != "127.0.0.1:8123" {
		t.Fatal(got)
	}
	logger, _ := logging.New("", nil)
	defer logger.Close()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })}
	defer srv.Shutdown(context.Background())
	lm := &listenManager{base: "127.0.0.1:0", log: logger, server: srv}
	errs := make(chan error, 10)
	if err := lm.apply(model.GeneralSettings{}, errs); err != nil {
		t.Fatal(err)
	}
	// Use the allocated port for fallback rather than asking for a second ephemeral listener.
	lm.addr = lm.ln.Addr().String()
	old := lm.addr
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if err := lm.apply(model.GeneralSettings{ListenPort: occupied.Addr().(*net.TCPAddr).Port}, errs); err == nil {
		t.Fatal("occupied port accepted")
	}
	if lm.current() != old || lm.ln == nil {
		t.Fatal("old listener was not restored")
	}
	if !lm.info(model.GeneralSettings{}).RestartNeeded {
		t.Fatal("failed bind not reported")
	}
}
func TestRollbackServerRestoresPreviousBinary(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "gwatch")
	if err := os.WriteFile(exe, []byte("new"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe+".rollback", []byte("old"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := rollbackServer(exe); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(exe)
	if err != nil || string(got) != "old" {
		t.Fatalf("%s %v", got, err)
	}
}

func TestRestartRequestsCancelOnlyOnce(t *testing.T) {
	calls := 0
	p := &program{mode: "console", cancel: func() { calls++ }}
	p.requestRestart()
	p.requestRestart()
	if calls != 1 || !p.restart {
		t.Fatalf("restart=%v cancellations=%d", p.restart, calls)
	}
	// A service-manager helper owns the restart once launched.
	p.helper = true
	p.afterStop()
	(&program{}).afterStop()
}
