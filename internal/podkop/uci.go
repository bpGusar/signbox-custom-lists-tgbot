package podkop

import (
	"context"
	"os/exec"
)

// run executes uci with the given arguments. It is a variable so tests can
// stand in for the router's config, and it never goes through a shell: every
// argument is passed as-is, including paths that came from a chat message.
var run = func(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "uci", args...).Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
