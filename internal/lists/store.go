package lists

import (
	"fmt"
	"os"
	"path/filepath"
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

func writeAtomic(path string, lines []string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	content := strings.Join(lines, "\n")
	if len(lines) > 0 {
		content += "\n"
	}

	tmp, err := os.CreateTemp(dir, ".lists-tg-*")
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

func AddAll(path string, values []string) error {
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
	return writeAtomic(path, lines)
}

func AddNew(path string, values []string) error {
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
	return writeAtomic(path, lines)
}

func Delete(path string, values []string) error {
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
	return writeAtomic(path, lines)
}

func Disable(path string, values []string) error {
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
	return writeAtomic(path, lines)
}

func DisableExistingOnly(path string, values []string) error {
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
	return writeAtomic(path, lines)
}

func AddDisabled(path string, values []string) error {
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
	return writeAtomic(path, lines)
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
