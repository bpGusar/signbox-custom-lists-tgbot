// Package frp is a thin wrapper around /usr/sbin/lst-frp-setup, the shell
// script that installs and configures the frp client (frpc) on the router for
// remote access through a VPS. The bot never touches frpc directly: it reads
// and writes the frp_* UCI options and asks the script to reconcile them.
package frp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	// SetupScript is the reconcile/status entry point shipped with the bot
	// package. It is absent in dev builds and on non-OpenWrt systems.
	SetupScript = "/usr/sbin/lst-frp-setup"
	setupLog    = "/tmp/lst-frp-setup.log"
)

// Info is the JSON the script's `status` command prints. Secrets are never
// included — only booleans saying whether they are set.
type Info struct {
	Enabled          bool   `json:"enabled"`
	Configured       bool   `json:"configured"`
	InstalledVersion string `json:"installed_version"`
	TargetVersion    string `json:"target_version"`
	Arch             string `json:"arch"`
	Asset            string `json:"asset"`
	FreeKB           int64  `json:"free_kb"`
	FreeOK           bool   `json:"free_ok"`
	Running          bool   `json:"running"`
	State            string `json:"state"`
	Detail           string `json:"detail"`
	ServerAddr       string `json:"server_addr"`
	ServerPort       string `json:"server_port"`
	ProxyPrefix      string `json:"proxy_prefix"`
	SSHRemotePort    string `json:"ssh_remote_port"`
	LuciDomain       string `json:"luci_domain"`
	LuciUser         string `json:"luci_user"`
	HasToken         bool   `json:"has_token"`
	HasLuciPassword  bool   `json:"has_luci_password"`
	LuciReady        bool   `json:"luci_ready"`
}

// Terminal states the ensure run settles into. Anything else means it is still
// working or has never run.
const (
	StateOK            = "ok"
	StateDisabled      = "disabled"
	StateNotConfigured = "not_configured"
	StateError         = "error"
	StateNoSpace       = "no_space"
	StateUnsupported   = "unsupported"
	StateRemoved       = "removed"
)

// Settled reports whether the last ensure run reached a terminal state.
func (i Info) Settled() bool {
	switch i.State {
	case StateOK, StateDisabled, StateNotConfigured, StateError, StateNoSpace, StateUnsupported, StateRemoved:
		return true
	default:
		return false
	}
}

// Supported reports whether the setup script is installed.
func Supported() bool {
	st, err := os.Stat(SetupScript)
	return err == nil && !st.IsDir()
}

// Status queries the script for the current state. It does no network I/O, so a
// short context is fine.
func Status(ctx context.Context) (Info, error) {
	var info Info
	out, err := run(ctx, false, "status")
	if err != nil {
		return info, fmt.Errorf("%s status: %w", SetupScript, err)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &info); err != nil {
		return info, fmt.Errorf("parse frp status: %w", err)
	}
	return info, nil
}

// Ensure kicks off a reconcile in the background and returns as soon as it is
// launched. The caller polls Status to see how it went. The script keeps the
// running frpc untouched on every failure path, so a bad reconcile never costs
// the router its connectivity.
func Ensure(ctx context.Context) error {
	if _, err := run(ctx, true, "ensure"); err != nil {
		return fmt.Errorf("%s ensure: %w", SetupScript, err)
	}
	return nil
}

// Remove stops frpc and deletes its binary and config.
func Remove(ctx context.Context) error {
	if _, err := run(ctx, false, "remove"); err != nil {
		return fmt.Errorf("%s remove: %w", SetupScript, err)
	}
	return nil
}

// LogTail returns the last n lines of the most recent ensure run's log.
func LogTail(n int) string {
	data, err := os.ReadFile(setupLog)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// run invokes the script. detach wraps it in setsid so a long reconcile
// survives the bot being restarted (an upgrade does exactly that), mirroring
// service.runUpgrade.
func run(ctx context.Context, detach bool, arg string) (string, error) {
	cmdline := SetupScript + " " + arg
	if detach {
		cmdline = "if command -v setsid >/dev/null 2>&1; then setsid " + cmdline + "; else " + cmdline + "; fi"
	}
	c := exec.CommandContext(ctx, "sh", "-c", cmdline)

	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	err := c.Run()
	return buf.String(), err
}
