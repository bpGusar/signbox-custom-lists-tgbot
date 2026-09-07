package frp

import (
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const (
	uciPackage = "lst-signbox-lists-tgbot"
	uciSection = "main"
)

// Field is one editable frp_* option. The key is the UCI option name without
// the frp_ prefix used in messages.
type Field string

const (
	FieldServerAddr    Field = "server_addr"
	FieldServerPort    Field = "server_port"
	FieldToken         Field = "token"
	FieldProxyPrefix   Field = "proxy_prefix"
	FieldSSHRemotePort Field = "ssh_remote_port"
	FieldLuciDomain    Field = "luci_domain"
	FieldLuciUser      Field = "luci_user"
	FieldLuciPassword  Field = "luci_password"
)

// EditableFields is the order they are shown in.
var EditableFields = []Field{
	FieldServerAddr, FieldServerPort, FieldToken, FieldProxyPrefix, FieldSSHRemotePort,
	FieldLuciDomain, FieldLuciUser, FieldLuciPassword,
}

func (f Field) uciOption() string { return "frp_" + string(f) }

// Label is the human name shown on buttons and prompts.
func (f Field) Label() string {
	switch f {
	case FieldServerAddr:
		return "адрес VPS"
	case FieldServerPort:
		return "порт frps"
	case FieldToken:
		return "токен frp"
	case FieldProxyPrefix:
		return "имя роутера (префикс прокси)"
	case FieldSSHRemotePort:
		return "публичный порт SSH"
	case FieldLuciDomain:
		return "домен LuCI"
	case FieldLuciUser:
		return "логин LuCI (basic-auth)"
	case FieldLuciPassword:
		return "пароль LuCI (basic-auth)"
	default:
		return string(f)
	}
}

// Secret reports whether the field's value must never be echoed back.
func (f Field) Secret() bool {
	return f == FieldToken || f == FieldLuciPassword
}

// Hint is the one-line input instruction shown when asking for the value.
func (f Field) Hint() string {
	switch f {
	case FieldServerAddr:
		return "IPv4 или хост VPS, например 203.0.113.10"
	case FieldServerPort:
		return "число 1–65535 (по умолчанию 12243)"
	case FieldToken:
		return "8–512 символов: буквы, цифры и + / = _ . -"
	case FieldProxyPrefix:
		return "1–32 символа: a-z, 0-9, дефис. Уникальное для каждого роутера на этом VPS (напр. home, dacha)"
	case FieldSSHRemotePort:
		return "число 4640–4643 (на VPS открыт этот диапазон)"
	case FieldLuciDomain:
		return "домен вида vps-ru.example.com (только a-z, 0-9, точка, дефис)"
	case FieldLuciUser:
		return "1–64 символа: буквы, цифры и . _ -"
	case FieldLuciPassword:
		return "8–128 печатных символов без кавычек и обратного слэша"
	default:
		return ""
	}
}

// Validate checks and normalises a value typed into the bot before it is
// written to UCI. Every value ends up inside a TOML config on the router, so
// the rules are strict allowlists — no newlines, no quotes, no shell/TOML
// metacharacters — matching the second check done in lst-frp-setup itself.
func (f Field) Validate(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if strings.ContainsAny(v, "\r\n\t") {
		return "", fmt.Errorf("значение не должно содержать переносы строк")
	}
	switch f {
	case FieldServerAddr:
		return validateServerAddr(v)
	case FieldServerPort:
		return validatePort(v, 1, 65535)
	case FieldToken:
		return validateToken(v)
	case FieldProxyPrefix:
		return validateProxyPrefix(v)
	case FieldSSHRemotePort:
		return validatePort(v, 4640, 4643)
	case FieldLuciDomain:
		return validateDomain(v)
	case FieldLuciUser:
		return validateLuciUser(v)
	case FieldLuciPassword:
		return validateLuciPassword(v)
	default:
		return "", fmt.Errorf("неизвестное поле")
	}
}

var (
	serverAddrRe  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	tokenRe       = regexp.MustCompile(`^[A-Za-z0-9+/=_.-]{8,512}$`)
	domainRe      = regexp.MustCompile(`^[a-z0-9.-]+$`)
	luciUserRe    = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	proxyPrefixRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
)

func validateProxyPrefix(v string) (string, error) {
	v = strings.ToLower(v)
	if !proxyPrefixRe.MatchString(v) {
		return "", fmt.Errorf("1–32 символа: a-z, 0-9 и дефис")
	}
	if strings.HasPrefix(v, "-") || strings.HasSuffix(v, "-") || strings.Contains(v, "--") {
		return "", fmt.Errorf("дефис не может быть первым/последним и не должен повторяться")
	}
	if v == "openwrt" || v == "lede" || v == "router" {
		return "", fmt.Errorf("выберите уникальное имя, не %q", v)
	}
	return v, nil
}

func validateServerAddr(v string) (string, error) {
	if v == "" {
		return "", fmt.Errorf("адрес не может быть пустым")
	}
	if len(v) > 253 {
		return "", fmt.Errorf("адрес слишком длинный")
	}
	if ip := net.ParseIP(v); ip != nil {
		if ip.To4() == nil {
			return "", fmt.Errorf("нужен IPv4-адрес, а не IPv6")
		}
		return v, nil
	}
	if !serverAddrRe.MatchString(v) || strings.HasPrefix(v, ".") || strings.HasSuffix(v, ".") ||
		strings.HasPrefix(v, "-") || strings.Contains(v, "..") {
		return "", fmt.Errorf("допустимы латинские буквы, цифры и . _ -")
	}
	return v, nil
}

func validatePort(v string, lo, hi int) (string, error) {
	n, err := strconv.Atoi(v)
	if err != nil {
		return "", fmt.Errorf("нужно число")
	}
	if n < lo || n > hi {
		return "", fmt.Errorf("порт должен быть в диапазоне %d–%d", lo, hi)
	}
	return strconv.Itoa(n), nil
}

func validateToken(v string) (string, error) {
	if !tokenRe.MatchString(v) {
		return "", fmt.Errorf("8–512 символов: буквы, цифры и + / = _ . -")
	}
	return v, nil
}

func validateDomain(v string) (string, error) {
	v = strings.ToLower(v)
	if len(v) > 253 || !domainRe.MatchString(v) {
		return "", fmt.Errorf("только a-z, 0-9, точка и дефис, до 253 символов")
	}
	if strings.HasPrefix(v, ".") || strings.HasSuffix(v, ".") || strings.Contains(v, "..") {
		return "", fmt.Errorf("некорректное расположение точек")
	}
	if !strings.Contains(v, ".") {
		return "", fmt.Errorf("нужно доменное имя с точкой")
	}
	for _, label := range strings.Split(v, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") || len(label) > 63 {
			return "", fmt.Errorf("некорректная часть домена: %q", label)
		}
	}
	return v, nil
}

func validateLuciUser(v string) (string, error) {
	if !luciUserRe.MatchString(v) {
		return "", fmt.Errorf("1–64 символа: буквы, цифры и . _ -")
	}
	return v, nil
}

func validateLuciPassword(v string) (string, error) {
	if len(v) < 8 || len(v) > 128 {
		return "", fmt.Errorf("длина пароля должна быть 8–128 символов")
	}
	for _, r := range v {
		if r < 0x20 || r > 0x7e {
			return "", fmt.Errorf("только печатные ASCII-символы")
		}
		if r == '"' || r == '\\' {
			return "", fmt.Errorf("нельзя использовать \" и \\")
		}
	}
	return v, nil
}

// Settings is the current frp_* config, read straight from UCI.
type Settings struct {
	Enabled       bool
	ServerAddr    string
	ServerPort    string
	Token         string
	ProxyPrefix   string
	SSHRemotePort string
	LuciDomain    string
	LuciUser      string
	LuciPassword  string
}

func LoadSettings() Settings {
	return Settings{
		Enabled:       uciGet("frp_enabled") == "1",
		ServerAddr:    uciGet("frp_server_addr"),
		ServerPort:    uciGet("frp_server_port"),
		Token:         uciGet("frp_token"),
		ProxyPrefix:   uciGet("frp_proxy_prefix"),
		SSHRemotePort: uciGet("frp_ssh_remote_port"),
		LuciDomain:    uciGet("frp_luci_domain"),
		LuciUser:      uciGet("frp_luci_user"),
		LuciPassword:  uciGet("frp_luci_password"),
	}
}

// SetField validates and persists one field, then commits.
func SetField(f Field, raw string) (string, error) {
	v, err := f.Validate(raw)
	if err != nil {
		return "", err
	}
	if err := uciSet(f.uciOption(), v); err != nil {
		return "", err
	}
	if err := uciCommit(); err != nil {
		return "", err
	}
	return v, nil
}

// SetEnabled flips frp_enabled and commits.
func SetEnabled(on bool) error {
	val := "0"
	if on {
		val = "1"
	}
	if err := uciSet("frp_enabled", val); err != nil {
		return err
	}
	return uciCommit()
}

func uciGet(option string) string {
	out, err := exec.Command("uci", "-q", "get", fmt.Sprintf("%s.%s.%s", uciPackage, uciSection, option)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func uciSet(option, value string) error {
	return exec.Command("uci", "set", fmt.Sprintf("%s.%s.%s=%s", uciPackage, uciSection, option, value)).Run()
}

func uciCommit() error {
	return exec.Command("uci", "commit", uciPackage).Run()
}
