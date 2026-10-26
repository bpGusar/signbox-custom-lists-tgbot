package service

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

type RestartResult struct {
	Success bool
	Output  string
	Err     error
}

// RunRestart runs the commands one after another and stops at the first one
// that fails.
func RunRestart(ctx context.Context, cmds []string) RestartResult {
	if len(cmds) == 0 {
		return RestartResult{Success: false, Err: fmt.Errorf("restart command not configured")}
	}

	var buf bytes.Buffer
	for _, cmd := range cmds {
		c := exec.CommandContext(ctx, "sh", "-c", cmd)
		c.Stdout = &buf
		c.Stderr = &buf
		if err := c.Run(); err != nil {
			return RestartResult{Success: false, Output: buf.String(), Err: fmt.Errorf("%s: %w", cmd, err)}
		}
	}
	return RestartResult{Success: true, Output: buf.String()}
}

func RunRestartWithProgress(ctx context.Context, cmds []string, onTick func(elapsed time.Duration)) RestartResult {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	done := make(chan RestartResult, 1)
	start := time.Now()

	go func() {
		done <- RunRestart(ctx, cmds)
	}()

	for {
		select {
		case res := <-done:
			return res
		case <-ticker.C:
			if onTick != nil {
				onTick(time.Since(start))
			}
		case <-ctx.Done():
			return RestartResult{Success: false, Err: ctx.Err()}
		}
	}
}
