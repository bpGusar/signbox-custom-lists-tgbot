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

func splitInputTokens(text string) []string {
	text = strings.ReplaceAll(text, "\r", "")
	lines := strings.Split(text, "\n")
	if len(lines) == 1 {
		lines = []string{text}
	}

	seen := make(map[string]struct{})
	var out []string

	add := func(value string) {
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, value)
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
			if strings.HasPrefix(part, "//") {
				part = strings.TrimSpace(strings.TrimPrefix(part, "//"))
				if part == "" {
					continue
				}
			}
			add(part)
		}
	}
	return out
}

func SplitInput(text string) []string {
	return splitInputTokens(text)
}

type ParseResult struct {
	Type    EntryType
	Valid   []string
	Invalid []string
	Mixed   bool
	Empty   bool
}

func ParseInput(text string) ParseResult {
	tokens := splitInputTokens(text)
	if len(tokens) == 0 {
		return ParseResult{Empty: true}
	}

	var result ParseResult
	var firstType EntryType

	for _, value := range tokens {
		et := ClassifyToken(value)
		if et == TypeUnknown {
			result.Invalid = append(result.Invalid, value)
			continue
		}
		if firstType == TypeUnknown {
			firstType = et
			result.Type = et
		} else if et != firstType {
			result.Mixed = true
		}
		result.Valid = append(result.Valid, value)
	}

	if result.Mixed {
		result.Valid = nil
	}
	if !result.Mixed && len(result.Valid) == 0 && len(result.Invalid) == 0 {
		result.Empty = true
	}
	return result
}
