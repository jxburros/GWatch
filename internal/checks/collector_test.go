package checks

import (
	"context"
	"net"
	"strings"
	"testing"

	"github.com/jxburros/GWatch/internal/model"
)

// The collector's runner refuses the check types a collector must never run,
// before anything is executed.
func TestRunCollectedRefusesScriptsAndMachines(t *testing.T) {
	marker := t.TempDir() + "/ran"
	for _, c := range []model.Check{
		{ID: 1, Type: model.CheckCustom, Config: model.CheckConfig{Command: "touch " + marker, Target: "x"}},
		{ID: 2, Type: model.CheckSystem},
		{ID: 3, Type: model.CheckMatter, Config: model.CheckConfig{MatterServer: "ws://127.0.0.1:1/ws"}},
	} {
		r := RunCollected(context.Background(), c, Options{})
		if r.Success || !strings.Contains(r.Message, "does not run") || r.CheckID != c.ID {
			t.Errorf("%s: %+v", c.Type, r)
		}
	}
}

// And runs what it may: a TCP connect to a listener here.
func TestRunCollectedRunsANetworkCheck(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	r := RunCollected(context.Background(), model.Check{ID: 9, Type: model.CheckTCP, TimeoutSeconds: 2,
		Config: model.CheckConfig{Target: "127.0.0.1", Port: port, Command: "ignored"}}, Options{})
	if !r.Success || r.CheckID != 9 {
		t.Fatalf("%+v", r)
	}
}
