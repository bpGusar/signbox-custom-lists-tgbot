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

const (
	uciPackage     = "lst-signbox-lists-tgbot"
	defaultDomain  = "/etc/lst-signbox-lists-tgbot/domain_list.lst"
	defaultIP      = "/etc/lst-signbox-lists-tgbot/ip_list.lst"
	defaultLabel   = "сервис"
	defaultState   = "/var/lib/lst-signbox-lists-tgbot/state.json"
	defaultLogPath = "/etc/lst-signbox-lists-tgbot/logs/bot.log"
)

type Config struct {
	Enabled      bool
	Token        string
	DomainList   string
	IPList       string
	// RestartCmd is only ever set by the admin via UCI/LuCI (or the
	// equivalent env var) at process startup, never by Telegram user input.
	// It is executed with "sh -c" (see service.RunRestart) — keep it that
	// way, since accepting any part of it from a chat message would be
	// remote code execution on the router.
	RestartCmd   string
	ServiceLabel string
	autoRestart  atomic.Bool
	StatePath    string
	LogPath      string
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
		Enabled:      true,
		DomainList:   defaultDomain,
		IPList:       defaultIP,
		ServiceLabel: defaultLabel,
		StatePath:    defaultState,
		LogPath:      defaultLogPath,
	}

	token := os.Getenv("LST_SIGNBOX_LISTS_TGBOT_TOKEN")
	if token == "" {
		token = os.Getenv("LISTS_TG_TOKEN")
	}
	if token != "" {
		cfg.Token = token
		if v := getenvAny("LST_SIGNBOX_LISTS_TGBOT_DOMAIN_LIST", "LISTS_TG_DOMAIN_LIST"); v != "" {
			cfg.DomainList = v
		}
		if v := getenvAny("LST_SIGNBOX_LISTS_TGBOT_IP_LIST", "LISTS_TG_IP_LIST"); v != "" {
			cfg.IPList = v
		}
		if v := getenvAny("LST_SIGNBOX_LISTS_TGBOT_RESTART_CMD", "LISTS_TG_RESTART_CMD"); v != "" {
			cfg.RestartCmd = v
		}
		if v := getenvAny("LST_SIGNBOX_LISTS_TGBOT_SERVICE_LABEL", "LISTS_TG_SERVICE_LABEL"); v != "" {
			cfg.ServiceLabel = v
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

	if v, err := uciGet("main", "domain_list"); err == nil && v != "" {
		cfg.DomainList = v
	}
	if v, err := uciGet("main", "ip_list"); err == nil && v != "" {
		cfg.IPList = v
	}
	if v, err := uciGet("main", "restart_cmd"); err == nil {
		cfg.RestartCmd = v
	}
	if v, err := uciGet("main", "service_label"); err == nil && v != "" {
		cfg.ServiceLabel = v
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
