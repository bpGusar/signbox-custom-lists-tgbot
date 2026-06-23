package lists

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const disabledPrefix = "// "

type LineEntry struct {
	Value    string
	Disabled bool
	Original string
}

type EntryStatus int

const (
	StatusNew EntryStatus = iota
	StatusActive
	StatusDisabled
)

type Classified struct {
	Value  string
	Status EntryStatus
}

func ParseLine(line string) (LineEntry, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return LineEntry{}, false
	}

	if strings.HasPrefix(trimmed, "//") {
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "//"))
		if value == "" {
			return LineEntry{}, false
		}
		return LineEntry{Value: value, Disabled: true, Original: line}, true
	}

	return LineEntry{Value: trimmed, Disabled: false, Original: line}, true
}

func ReadFile(path string) ([]LineEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var entries []LineEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		if e, ok := ParseLine(line); ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

func ClassifyValues(path string, values []string) ([]Classified, error) {
	entries, err := ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	index := make(map[string]EntryStatus)
	for _, e := range entries {
		key := strings.ToLower(e.Value)
		if e.Disabled {
			index[key] = StatusDisabled
		} else {
			index[key] = StatusActive
		}
	}

	var out []Classified
	for _, v := range values {
		key := strings.ToLower(v)
		if st, ok := index[key]; ok {
			out = append(out, Classified{Value: v, Status: st})
		} else {
			out = append(out, Classified{Value: v, Status: StatusNew})
		}
	}
	return out, nil
}

func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func MissingFiles(domainPath, ipPath string) []string {
	var missing []string
	if !FileExists(domainPath) {
		missing = append(missing, domainPath)
	}
	if !FileExists(ipPath) {
		missing = append(missing, ipPath)
	}
	return missing
}

func CreateFiles(paths ...string) error {
	for _, p := range paths {
		dir := filepath.Dir(p)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("create file %s: %w", p, err)
		}
		_ = f.Close()
	}
	return nil
}

func hostWithoutPath(host string) string {
	host = strings.TrimSpace(host)
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	return strings.ToLower(host)
}

// baseDomain returns registrable domain (eTLD+1 heuristic: last two labels).
func baseDomain(host string) string {
	host = hostWithoutPath(host)
	labels := strings.Split(host, ".")
	if len(labels) <= 2 {
		return host
	}
	return labels[len(labels)-2] + "." + labels[len(labels)-1]
}

func domainSortKey(line string) string {
	var host string
	if e, ok := ParseLine(line); ok {
		host = e.Value
	} else {
		host = line
	}
	// Sort by base domain first, then by full hostname within the same domain.
	return baseDomain(host) + "\x00" + hostWithoutPath(host)
}

func sortDomainLines(lines []string) []string {
	if len(lines) < 2 {
		return lines
	}
	sorted := append([]string(nil), lines...)
	slices.SortFunc(sorted, func(a, b string) int {
		return strings.Compare(domainSortKey(a), domainSortKey(b))
	})
	return sorted
}

func maybeSortLines(lines []string, listType EntryType) []string {
	if listType == TypeDomain {
		return sortDomainLines(lines)
	}
	return lines
}

func writeAtomic(path string, lines []string, listType EntryType) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	lines = maybeSortLines(lines, listType)
	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}

	tmp, err := os.CreateTemp(dir, ".lst-signbox-lists-tgbot-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

func AddAll(path string, values []string, listType EntryType) error {
	classified, err := ClassifyValues(path, values)
	if err != nil {
		return err
	}

	entries, err := ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	toEnable := make(map[string]struct{})
	for _, c := range classified {
		switch c.Status {
		case StatusDisabled:
			toEnable[strings.ToLower(c.Value)] = struct{}{}
		}
	}

	var lines []string
	for _, e := range entries {
		key := strings.ToLower(e.Value)
		if _, ok := toEnable[key]; ok {
			lines = append(lines, e.Value)
			delete(toEnable, key)
		} else {
			if e.Disabled {
				lines = append(lines, disabledPrefix+e.Value)
			} else {
				lines = append(lines, e.Value)
			}
		}
	}

	for _, c := range classified {
		if c.Status == StatusNew {
			lines = append(lines, c.Value)
		}
	}
	return writeAtomic(path, lines, listType)
}

func AddNew(path string, values []string, listType EntryType) error {
	classified, err := ClassifyValues(path, values)
	if err != nil {
		return err
	}

	entries, err := ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var lines []string
	for _, e := range entries {
		if e.Disabled {
			lines = append(lines, disabledPrefix+e.Value)
		} else {
			lines = append(lines, e.Value)
		}
	}

	for _, c := range classified {
		if c.Status == StatusNew {
			lines = append(lines, c.Value)
		}
	}
	return writeAtomic(path, lines, listType)
}

func Delete(path string, values []string, listType EntryType) error {
	entries, err := ReadFile(path)
	if err != nil {
		return err
	}

	remove := make(map[string]struct{})
	for _, v := range values {
		remove[strings.ToLower(v)] = struct{}{}
	}

	var lines []string
	for _, e := range entries {
		if _, ok := remove[strings.ToLower(e.Value)]; ok {
			continue
		}
		if e.Disabled {
			lines = append(lines, disabledPrefix+e.Value)
		} else {
			lines = append(lines, e.Value)
		}
	}
	return writeAtomic(path, lines, listType)
}

func Disable(path string, values []string, listType EntryType) error {
	entries, err := ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	toDisable := make(map[string]struct{})
	for _, v := range values {
		toDisable[strings.ToLower(v)] = struct{}{}
	}

	var lines []string
	for _, e := range entries {
		key := strings.ToLower(e.Value)
		if _, ok := toDisable[key]; ok {
			lines = append(lines, disabledPrefix+e.Value)
			delete(toDisable, key)
		} else if e.Disabled {
			lines = append(lines, disabledPrefix+e.Value)
		} else {
			lines = append(lines, e.Value)
		}
	}

	for _, v := range values {
		if _, ok := toDisable[strings.ToLower(v)]; ok {
			lines = append(lines, disabledPrefix+v)
		}
	}
	return writeAtomic(path, lines, listType)
}

func DisableExistingOnly(path string, values []string, listType EntryType) error {
	entries, err := ReadFile(path)
	if err != nil {
		return err
	}

	toDisable := make(map[string]struct{})
	for _, v := range values {
		toDisable[strings.ToLower(v)] = struct{}{}
	}

	var lines []string
	for _, e := range entries {
		key := strings.ToLower(e.Value)
		if _, ok := toDisable[key]; ok && !e.Disabled {
			lines = append(lines, disabledPrefix+e.Value)
		} else if e.Disabled {
			lines = append(lines, disabledPrefix+e.Value)
		} else {
			lines = append(lines, e.Value)
		}
	}
	return writeAtomic(path, lines, listType)
}

func AddDisabled(path string, values []string, listType EntryType) error {
	entries, err := ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var lines []string
	for _, e := range entries {
		if e.Disabled {
			lines = append(lines, disabledPrefix+e.Value)
		} else {
			lines = append(lines, e.Value)
		}
	}

	existing := make(map[string]struct{})
	for _, e := range entries {
		existing[strings.ToLower(e.Value)] = struct{}{}
	}

	for _, v := range values {
		if _, ok := existing[strings.ToLower(v)]; !ok {
			lines = append(lines, disabledPrefix+v)
		}
	}
	return writeAtomic(path, lines, listType)
}

func GroupByStatus(classified []Classified) (newVals, active, disabled []string) {
	for _, c := range classified {
		switch c.Status {
		case StatusNew:
			newVals = append(newVals, c.Value)
		case StatusActive:
			active = append(active, c.Value)
		case StatusDisabled:
			disabled = append(disabled, c.Value)
		}
	}
	return
}

func FormatList(items []string) string {
	if len(items) == 0 {
		return "—"
	}
	return strings.Join(items, "\n")
}

func FormatDisabledList(items []string) string {
	if len(items) == 0 {
		return "—"
	}
	lines := make([]string, len(items))
	for i, v := range items {
		lines[i] = "// " + v
	}
	return strings.Join(lines, "\n")
}
