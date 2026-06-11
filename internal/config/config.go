package config

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const (
	uciPackage     = "lists-tg"
	defaultDomain  = "/etc/lists-tg/domain_list.lst"
	defaultIP      = "/etc/lists-tg/ip_list.lst"
	defaultLabel   = "сервис"
	defaultState   = "/var/lib/lists-tg/state.json"
)

type Config struct {
	Enabled      bool
	Token        string
	DomainList   string
	IPList       string
	RestartCmd   string
	ServiceLabel string
	StatePath    string
}

func Load() (*Config, error) {
	cfg := &Config{
		Enabled:      true,
		DomainList:   defaultDomain,
		IPList:       defaultIP,
		ServiceLabel: defaultLabel,
		StatePath:    defaultState,
	}

	if token := os.Getenv("LISTS_TG_TOKEN"); token != "" {
		cfg.Token = token
		if v := os.Getenv("LISTS_TG_DOMAIN_LIST"); v != "" {
			cfg.DomainList = v
		}
		if v := os.Getenv("LISTS_TG_IP_LIST"); v != "" {
			cfg.IPList = v
		}
		if v := os.Getenv("LISTS_TG_RESTART_CMD"); v != "" {
			cfg.RestartCmd = v
		}
		if v := os.Getenv("LISTS_TG_SERVICE_LABEL"); v != "" {
			cfg.ServiceLabel = v
		}
		if v := os.Getenv("LISTS_TG_STATE_PATH"); v != "" {
			cfg.StatePath = v
		}
		if v := os.Getenv("LISTS_TG_ENABLED"); v != "" {
			cfg.Enabled = v == "1" || strings.EqualFold(v, "true")
		}
		return cfg, nil
	}

	enabled, err := uciGet("main", "enabled")
	if err == nil {
		cfg.Enabled = enabled == "1"
	}

	token, err := uciGet("main", "token")
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
	if v, err := uciGet("main", "state_path"); err == nil && v != "" {
		cfg.StatePath = v
	}

	return cfg, nil
}

func uciGet(section, option string) (string, error) {
	out, err := exec.Command("uci", "get", fmt.Sprintf("%s.%s.%s", uciPackage, section, option)).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func ParseBool(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b || s == "1"
}
