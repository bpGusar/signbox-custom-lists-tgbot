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
	category string
}

// splitInputTokens reads a message the way a list file is read: a
// "// #cat: name" line puts the values below it into that category, until the
// next header or "// #uncat". A header with a name that cannot be a category
// is returned in badHeaders.
func splitInputTokens(text string) (tokens []inputToken, badHeaders []string, hasHeaders bool) {
	text = strings.ReplaceAll(text, "\r", "")
	seen := make(map[string]struct{})
	current := Uncategorized

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch kind, _, name := classifyLine(line); kind {
		case lineCategoryHeader:
			hasHeaders = true
			valid, err := ValidateCategoryName(name)
			if err != nil {
				badHeaders = append(badHeaders, line)
				continue
			}
			current = valid
			continue
		case lineUncatMarker:
			hasHeaders = true
			current = Uncategorized
			continue
		}
		for _, part := range strings.Split(line, ",") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "//") {
				part = strings.TrimSpace(strings.TrimPrefix(part, "//"))
			}
			if part == "" {
				continue
			}
			key := strings.ToLower(part)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			tokens = append(tokens, inputToken{value: part, category: current})
		}
	}
	return tokens, badHeaders, hasHeaders
}

func SplitInput(text string) []string {
	tokens, _, _ := splitInputTokens(text)
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, t.value)
	}
	return out
}

type ParseResult struct {
	Type    EntryType
	Valid   []string
	Invalid []string
	Mixed   bool
	Empty   bool
	// Categories maps a lower-cased value to the category it was headed with
	// in the message; nil when the message has no category headers.
	Categories map[string]string
}

func ParseInput(text string) ParseResult {
	tokens, badHeaders, hasHeaders := splitInputTokens(text)
	if len(tokens) == 0 && len(badHeaders) == 0 {
		return ParseResult{Empty: true}
	}

	result := ParseResult{Invalid: badHeaders}
	if hasHeaders {
		result.Categories = make(map[string]string, len(tokens))
	}
	var firstType EntryType

	for _, tok := range tokens {
		value := tok.value
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
		if result.Categories != nil {
			result.Categories[strings.ToLower(value)] = tok.category
		}
	}

	if result.Mixed {
		result.Valid = nil
		result.Categories = nil
	}
	if !result.Mixed && len(result.Valid) == 0 && len(result.Invalid) == 0 {
		result.Empty = true
	}
	return result
}
