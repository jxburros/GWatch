package subprocess

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GWATCH_PROCESS_HELPER") != "1" {
		return
	}
	fmt.Print(strings.Repeat("x", OutputLimit*8))
	os.Exit(0)
}

func TestOutputIsBounded(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=TestHelperProcess")
	cmd.Env = append(os.Environ(), "GWATCH_PROCESS_HELPER=1")
	output, err := Run(cmd)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) != OutputLimit {
		t.Fatalf("captured %d bytes", len(output))
	}
}

func TestGrandchildCannotHoldPipeOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell regression; Windows uses kill-on-close job objects")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _ = Run(exec.CommandContext(ctx, "sh", "-c", "sleep 30 & wait"))
	if time.Since(start) > 3*time.Second {
		t.Fatal("grandchild held output pipe beyond deadline")
	}
}

func TestExitedParentCannotHoldPipeOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell regression")
	}
	start := time.Now()
	_, _ = Run(exec.CommandContext(context.Background(), "sh", "-c", "sleep 30 & echo done"))
	if time.Since(start) > 3*time.Second {
		t.Fatal("orphan held output pipe")
	}
}
