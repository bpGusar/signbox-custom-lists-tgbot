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

func SplitInput(text string) []string {
	text = strings.ReplaceAll(text, "\n", ",")
	text = strings.ReplaceAll(text, "\r", "")
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t'
	})

	seen := make(map[string]struct{})
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		key := strings.ToLower(p)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	return out
}

type ParseResult struct {
	Type     EntryType
	Valid    []string
	Invalid  []string
	Mixed    bool
	Empty    bool
}

func ParseInput(text string) ParseResult {
	tokens := SplitInput(text)
	if len(tokens) == 0 {
		return ParseResult{Empty: true}
	}

	var result ParseResult
	var firstType EntryType

	for _, t := range tokens {
		et := ClassifyToken(t)
		if et == TypeUnknown {
			result.Invalid = append(result.Invalid, t)
			continue
		}
		if firstType == TypeUnknown {
			firstType = et
			result.Type = et
		} else if et != firstType {
			result.Mixed = true
		}
		result.Valid = append(result.Valid, t)
	}

	if result.Mixed {
		result.Valid = nil
	}
	return result
}
