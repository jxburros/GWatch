// Package subprocess bounds script output and the lifetime of inherited pipes.
package subprocess

import (
	"bytes"
	"os/exec"
	"time"
)

const OutputLimit = 64 << 10

type limitedBuffer struct{ buffer bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if remaining := OutputLimit - b.buffer.Len(); remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.buffer.Write(p)
	}
	return n, nil
}

// Run drains excess output without retaining it, kills descendants on timeout,
// and bounds Wait even when a descendant inherited an output pipe.
func Run(cmd *exec.Cmd) (string, error) {
	var output limitedBuffer
	cmd.Stdout, cmd.Stderr = &output, &output
	cmd.WaitDelay = time.Second
	configure(cmd)
	if err := cmd.Start(); err != nil {
		return "", err
	}
	cleanup, err := contain(cmd)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", err
	}
	defer cleanup()
	err = cmd.Wait()
	return output.buffer.String(), err
}
