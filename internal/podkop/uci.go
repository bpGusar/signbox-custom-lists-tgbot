package podkop

import (
	"context"
	"os/exec"
)

// run executes uci with the given arguments. It is a variable so tests can
// stand in for the router's config, and it never goes through a shell: every
// argument is passed as-is, including paths that came from a chat message.
var run = func(ctx context.Context, args ...string) (string, error) {
	return runCmd(ctx, "uci", args...)
}

// runCmd is the same for any binary the package shells out to.
func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
