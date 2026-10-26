package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
)

// DefaultRestartCmd restarts whichever of podkop and forkop is installed.
const DefaultRestartCmd = "/etc/init.d/$([ -x /etc/init.d/forkop ] && echo forkop || echo podkop) restart"

const (
	uciPackage     = "lst-signbox-lists-tgbot"
	defaultState   = "/var/lib/lst-signbox-lists-tgbot/state.json"
	defaultLogPath = "/etc/lst-signbox-lists-tgbot/logs/bot.log"
)

type Config struct {
	Enabled bool
	Token   string
	// RestartCmds are only ever set by the admin via UCI/LuCI (or the
	// equivalent env var) at process startup, never by Telegram user input.
	// Each is executed with "sh -c" (see service.RunRestart) — keep it that
	// way, since accepting any part of them from a chat message would be
	// remote code execution on the router.
	RestartCmds []string
	autoRestart atomic.Bool
	StatePath   string
	LogPath     string
}

func (c *Config) GetAutoRestart() bool {
	return c.autoRestart.Load()
}

// SetAutoRestartValue sets the in-memory flag without persisting to UCI.
// Used during config loading and in tests; runtime toggles from the bot
// should go through SetAutoRestart instead, which also writes to UCI.
func (c *Config) SetAutoRestartValue(v bool) {
	c.autoRestart.Store(v)
}

func Load() (*Config, error) {
	cfg := &Config{
		Enabled:   true,
		StatePath: defaultState,
		LogPath:   defaultLogPath,
	}

	token := os.Getenv("LST_SIGNBOX_LISTS_TGBOT_TOKEN")
	if token == "" {
		token = os.Getenv("LISTS_TG_TOKEN")
	}
	if token != "" {
		cfg.Token = token
		if v := getenvAny("LST_SIGNBOX_LISTS_TGBOT_RESTART_CMD", "LISTS_TG_RESTART_CMD"); v != "" {
			cfg.RestartCmds = nonEmpty(strings.Split(v, "\n"))
		}
		if v := getenvAny("LST_SIGNBOX_LISTS_TGBOT_STATE_PATH", "LISTS_TG_STATE_PATH"); v != "" {
			cfg.StatePath = v
		}
		if v := getenvAny("LST_SIGNBOX_LISTS_TGBOT_LOG_PATH", "LISTS_TG_LOG_PATH"); v != "" {
			cfg.LogPath = normalizeLogPath(v)
		}
		if v := getenvAny("LST_SIGNBOX_LISTS_TGBOT_ENABLED", "LISTS_TG_ENABLED"); v != "" {
			cfg.Enabled = v == "1" || strings.EqualFold(v, "true")
		}
		if v := getenvAny("LST_SIGNBOX_LISTS_TGBOT_AUTO_RESTART", "LISTS_TG_AUTO_RESTART"); v != "" {
			cfg.autoRestart.Store(ParseBool(v))
		}
		return cfg, nil
	}

	enabled, err := uciGet("main", "enabled")
	if err == nil {
		cfg.Enabled = enabled == "1"
	}

	token, err = uciGet("main", "token")
	if err != nil {
		return nil, fmt.Errorf("read token from UCI: %w", err)
	}
	cfg.Token = token

	if v, err := uciShow("main", "restart_cmd"); err == nil {
		cfg.RestartCmds = nonEmpty(parseUCIValues(v))
	} else {
		cfg.RestartCmds = []string{DefaultRestartCmd}
	}
	if v, err := uciGet("main", "auto_restart"); err == nil {
		cfg.autoRestart.Store(ParseBool(v))
	}
	if v, err := uciGet("main", "state_path"); err == nil && v != "" {
		cfg.StatePath = v
	}
	if v, err := uciGet("main", "log_path"); err == nil && v != "" {
		cfg.LogPath = normalizeLogPath(v)
	}

	return cfg, nil
}

func normalizeLogPath(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return defaultLogPath
	}
	if strings.HasSuffix(s, "/") || filepath.Ext(s) == "" {
		return filepath.Join(s, "bot.log")
	}
	return s
}

func uciGet(section, option string) (string, error) {
	out, err := exec.Command("uci", "get", fmt.Sprintf("%s.%s.%s", uciPackage, section, option)).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// uciShow returns an option's value the way "uci show" prints it: every
// value single-quoted, list values separated by spaces. Unlike "uci get" it
// keeps list values that contain spaces apart.
func uciShow(section, option string) (string, error) {
	key := fmt.Sprintf("%s.%s.%s", uciPackage, section, option)
	out, err := exec.Command("uci", "-q", "show", key).Output()
	if err != nil {
		return "", err
	}
	_, value, ok := strings.Cut(strings.TrimRight(string(out), "\n"), "=")
	if !ok {
		return "", fmt.Errorf("unexpected uci show output for %s", key)
	}
	return value, nil
}

// parseUCIValues splits "uci show" output into values: single-quoted runs
// separated by spaces, a quote inside a value escaped shell-style.
func parseUCIValues(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote, started := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuote && c == '\'':
			inQuote = false
		case inQuote:
			cur.WriteByte(c)
		case c == '\'':
			inQuote, started = true, true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			started = true
		case c == ' ':
			if started {
				out = append(out, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteByte(c)
			started = true
		}
	}
	if started {
		out = append(out, cur.String())
	}
	return out
}

func nonEmpty(values []string) []string {
	var out []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func uciSet(section, option, value string) error {
	key := fmt.Sprintf("%s.%s.%s=%s", uciPackage, section, option, value)
	return exec.Command("uci", "set", key).Run()
}

func uciCommit() error {
	return exec.Command("uci", "commit", uciPackage).Run()
}

func usesEnvConfig() bool {
	return os.Getenv("LST_SIGNBOX_LISTS_TGBOT_TOKEN") != "" || os.Getenv("LISTS_TG_TOKEN") != ""
}

func (c *Config) SetAutoRestart(enabled bool) error {
	prev := c.autoRestart.Swap(enabled)
	if usesEnvConfig() {
		return nil
	}
	val := "0"
	if enabled {
		val = "1"
	}
	if err := uciSet("main", "auto_restart", val); err != nil {
		c.autoRestart.Store(prev)
		return fmt.Errorf("uci set auto_restart: %w", err)
	}
	if err := uciCommit(); err != nil {
		c.autoRestart.Store(prev)
		return fmt.Errorf("uci commit: %w", err)
	}
	return nil
}

func ParseBool(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b || s == "1"
}

func getenvAny(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}
