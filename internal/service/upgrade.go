package service

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
	UpgradeScript  = "/usr/sbin/lst-signbox-lists-tgbot-upgrade"
	upgradeLogPath = "/tmp/lst-signbox-lists-tgbot-upgrade.log"
)

// Upgrade states reported by the script's status/check JSON.
const (
	UpgradeStateIdle    = "idle"
	UpgradeStateRunning = "running"
	UpgradeStateSuccess = "success"
	UpgradeStateFailed  = "failed"
)

type UpgradeInfo struct {
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	UpdateAvailable bool   `json:"update_available"`
	Running         bool   `json:"running"`
	State           string `json:"state"`
}

// InProgress reports whether an upgrade is still working. The script clears
// its lock only after the whole install finishes, so a fresh process that
// starts mid-upgrade (the install restarts us) still sees this as true.
func (i UpgradeInfo) InProgress() bool {
	return i.Running || i.State == UpgradeStateRunning
}

// UpgradeSupported reports whether the self-upgrade script is installed.
// It is absent in dev builds and on non-OpenWrt systems.
func UpgradeSupported() bool {
	st, err := os.Stat(UpgradeScript)
	return err == nil && !st.IsDir()
}

// CheckUpgrade queries GitHub for the latest release via the upgrade script.
// It is slow (a network round trip) — give it a generous context.
func CheckUpgrade(ctx context.Context) (UpgradeInfo, error) {
	return runUpgradeJSON(ctx, "check")
}

// UpgradeStatus reports on a running or finished upgrade. Unlike CheckUpgrade
// it serves cached versions while an install holds the opkg lock.
func UpgradeStatus(ctx context.Context) (UpgradeInfo, error) {
	return runUpgradeJSON(ctx, "status")
}

// StartUpgrade kicks off the background upgrade and returns as soon as it is
// launched. The install ends by restarting this service, so the caller must
// persist anything it wants to report on afterwards *before* calling this.
func StartUpgrade(ctx context.Context) error {
	out, err := runUpgrade(ctx, "start")
	if err != nil {
		if strings.Contains(out, "already_running") {
			return fmt.Errorf("upgrade already running")
		}
		return fmt.Errorf("start upgrade: %w", err)
	}
	return nil
}

// UpgradeLogTail returns the last n lines of the upgrade log, for reporting a
// failure back to the user.
func UpgradeLogTail(n int) string {
	data, err := os.ReadFile(upgradeLogPath)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func runUpgradeJSON(ctx context.Context, arg string) (UpgradeInfo, error) {
	var info UpgradeInfo

	out, err := runUpgrade(ctx, arg)
	if err != nil {
		return info, fmt.Errorf("%s %s: %w", UpgradeScript, arg, err)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &info); err != nil {
		return info, fmt.Errorf("parse %s output: %w", arg, err)
	}
	return info, nil
}

// runUpgrade invokes the script through setsid so the detached install
// survives procd killing this service's process group — which is exactly what
// the install itself triggers when it restarts us. Systems without setsid
// (dev boxes) fall back to a plain call.
func runUpgrade(ctx context.Context, arg string) (string, error) {
	script := UpgradeScript + " " + arg
	c := exec.CommandContext(ctx, "sh", "-c",
		"if command -v setsid >/dev/null 2>&1; then setsid "+script+"; else "+script+"; fi")

	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	err := c.Run()
	return buf.String(), err
}
