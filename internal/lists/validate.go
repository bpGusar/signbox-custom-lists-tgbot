package lists

import (
	"net"
	"regexp"
	"strings"
)

var domainRe = regexp.MustCompile(`^(?:(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)\.)+(?:[a-zA-Z]{2,}|xn--[a-zA-Z0-9-]{1,59}[a-zA-Z0-9])(?:/[^\s]*)?$`)

var dotTLDRe = regexp.MustCompile(`^\.[a-zA-Z]{2,}$`)

type EntryType int

const (
	TypeUnknown EntryType = iota
	TypeDomain
	TypeIP
)

func ClassifyToken(token string) EntryType {
	token = strings.TrimSpace(token)
	if token == "" {
		return TypeUnknown
	}
	if IsDomain(token) {
		return TypeDomain
	}
	if IsIPOrCIDR(token) {
		return TypeIP
	}
	return TypeUnknown
}

func IsDomain(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) > 253 {
		return false
	}
	if dotTLDRe.MatchString(s) {
		return true
	}
	return domainRe.MatchString(s)
}

func IsIPOrCIDR(s string) bool {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		_, _, err := net.ParseCIDR(s)
		return err == nil
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	return ip.To4() != nil
}

type inputToken struct {
	value    string
	disabled bool
}

func splitInputTokens(text string) []inputToken {
	text = strings.ReplaceAll(text, "\r", "")
	lines := strings.Split(text, "\n")
	if len(lines) == 1 {
		lines = []string{text}
	}

	seen := make(map[string]int)
	var out []inputToken

	add := func(value string, disabled bool) {
		key := strings.ToLower(value)
		if i, ok := seen[key]; ok {
			if disabled {
				out[i].disabled = true
			}
			return
		}
		seen[key] = len(out)
		out = append(out, inputToken{value: value, disabled: disabled})
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for _, part := range strings.Split(line, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			disabled := false
			if strings.HasPrefix(part, "//") {
				disabled = true
				part = strings.TrimSpace(strings.TrimPrefix(part, "//"))
				if part == "" {
					continue
				}
			}
			add(part, disabled)
		}
	}
	return out
}

func SplitInput(text string) []string {
	tokens := splitInputTokens(text)
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, t.value)
	}
	return out
}

type ParseResult struct {
	Type      EntryType
	Valid     []string
	ToDisable []string
	Invalid   []string
	Mixed     bool
	Empty     bool
}

func ParseInput(text string) ParseResult {
	tokens := splitInputTokens(text)
	if len(tokens) == 0 {
		return ParseResult{Empty: true}
	}

	var result ParseResult
	var firstType EntryType

	for _, tok := range tokens {
		et := ClassifyToken(tok.value)
		if et == TypeUnknown {
			result.Invalid = append(result.Invalid, tok.value)
			continue
		}
		if firstType == TypeUnknown {
			firstType = et
			result.Type = et
		} else if et != firstType {
			result.Mixed = true
		}
		if tok.disabled {
			result.ToDisable = append(result.ToDisable, tok.value)
		} else {
			result.Valid = append(result.Valid, tok.value)
		}
	}

	if result.Mixed {
		result.Valid = nil
		result.ToDisable = nil
	}
	if len(result.Valid) == 0 && len(result.ToDisable) == 0 && len(result.Invalid) == 0 {
		result.Empty = true
	}
	return result
}
