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

func RunRestart(ctx context.Context, cmd string) RestartResult {
	if cmd == "" {
		return RestartResult{Success: false, Err: fmt.Errorf("restart command not configured")}
	}

	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf

	err := c.Run()
	output := buf.String()
	if err != nil {
		return RestartResult{Success: false, Output: output, Err: err}
	}
	return RestartResult{Success: true, Output: output}
}

func RunRestartWithProgress(ctx context.Context, cmd string, onTick func(elapsed time.Duration)) RestartResult {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	done := make(chan RestartResult, 1)
	start := time.Now()

	go func() {
		done <- RunRestart(ctx, cmd)
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
