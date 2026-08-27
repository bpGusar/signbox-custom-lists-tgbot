package lists

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	disabledPrefix = "// "
	categoryPrefix = "// #cat: "
	uncatMarker    = "// #uncat"
)

// Uncategorized is the category of entries that live outside any "// #cat:"
// section. It is rendered last, after every named category.
const Uncategorized = ""

// UncategorizedLabel is how the uncategorized bucket is shown to the user.
const UncategorizedLabel = "Без категории"

// MaxCategoryNameLen caps category names so a header line stays readable and
// the name still fits into a Telegram message alongside its counters.
const MaxCategoryNameLen = 40

var (
	// ErrCategoryExists is returned when a rename would collide with another
	// existing category.
	ErrCategoryExists = errors.New("category already exists")
	// ErrCategoryNotFound is returned for operations on a missing category.
	ErrCategoryNotFound = errors.New("category not found")
)

type LineEntry struct {
	Value    string
	Disabled bool
	Original string
	Category string
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
	// Category is the category the value already sits in; meaningless for
	// StatusNew.
	Category string
}

// CategoryInfo summarises one category for the menus.
type CategoryInfo struct {
	Name     string
	Active   int
	Disabled int
}

func (c CategoryInfo) Total() int { return c.Active + c.Disabled }

// DisplayName renders the uncategorized bucket under its human label.
func (c CategoryInfo) DisplayName() string { return CategoryDisplayName(c.Name) }

func CategoryDisplayName(name string) string {
	if name == Uncategorized {
		return UncategorizedLabel
	}
	return name
}

// categoryKey normalises a category name for comparisons; names are matched
// case-insensitively but stored and shown as the user typed them.
func categoryKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// SameCategory reports whether two names refer to the same category.
func SameCategory(a, b string) bool { return categoryKey(a) == categoryKey(b) }

type lineKind int

const (
	lineIgnored lineKind = iota
	lineValue
	lineCategoryHeader
	lineUncatMarker
)

// classifyLine splits a raw file line into one of: an entry, a category
// directive, or something to ignore. Directives are "//"-comments starting
// with '#', which no valid domain or IP can ever look like.
func classifyLine(line string) (lineKind, LineEntry, string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return lineIgnored, LineEntry{}, ""
	}
	if !strings.HasPrefix(trimmed, "//") {
		return lineValue, LineEntry{Value: trimmed, Original: line}, ""
	}

	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "//"))
	if rest == "" {
		return lineIgnored, LineEntry{}, ""
	}
	if strings.HasPrefix(rest, "#") {
		if name, ok := categoryHeaderName(rest); ok {
			return lineCategoryHeader, LineEntry{}, name
		}
		// "// #uncat", "// #cat:" with no name, and any other "// #" comment
		// close the current section instead of becoming an entry.
		return lineUncatMarker, LineEntry{}, ""
	}
	return lineValue, LineEntry{Value: rest, Disabled: true, Original: line}, ""
}

func categoryHeaderName(rest string) (string, bool) {
	if !strings.HasPrefix(rest, "#cat:") {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(rest, "#cat:"))
	if name == "" {
		return "", false
	}
	return name, true
}

// ParseLine reports the entry a line carries, if it carries one. Category
// directives are not entries.
func ParseLine(line string) (LineEntry, bool) {
	kind, e, _ := classifyLine(line)
	if kind != lineValue {
		return LineEntry{}, false
	}
	return e, true
}

// content is a parsed list file: its entries plus the category order, kept
// separately so a category that currently holds no entries still survives a
// rewrite.
type content struct {
	entries []LineEntry
	order   []string
}

func (c *content) ensureCategory(name string) {
	if name == Uncategorized {
		return
	}
	if c.indexOfCategory(name) >= 0 {
		return
	}
	c.order = append(c.order, name)
}

func (c *content) indexOfCategory(name string) int {
	key := categoryKey(name)
	for i, n := range c.order {
		if categoryKey(n) == key {
			return i
		}
	}
	return -1
}

func (c *content) dropCategory(name string) {
	if i := c.indexOfCategory(name); i >= 0 {
		c.order = append(c.order[:i], c.order[i+1:]...)
	}
}

// canonicalCategory returns the stored spelling of a category, so entries are
// tagged consistently even if the user typed a different case.
func (c *content) canonicalCategory(name string) string {
	if i := c.indexOfCategory(name); i >= 0 {
		return c.order[i]
	}
	return strings.TrimSpace(name)
}

func readContent(path string) (content, error) {
	var c content
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}

	current := Uncategorized
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		kind, e, name := classifyLine(line)
		switch kind {
		case lineCategoryHeader:
			current = name
			c.ensureCategory(name)
		case lineUncatMarker:
			current = Uncategorized
		case lineValue:
			e.Category = current
			c.entries = append(c.entries, e)
		}
	}
	return c, nil
}

// readContentOptional treats a missing file as an empty one, matching the
// previous behaviour of the add/disable paths.
func readContentOptional(path string) (content, error) {
	c, err := readContent(path)
	if err != nil && !os.IsNotExist(err) {
		return content{}, err
	}
	return c, nil
}

func ReadFile(path string) ([]LineEntry, error) {
	c, err := readContent(path)
	if err != nil {
		return nil, err
	}
	return c.entries, nil
}

func ClassifyValues(path string, values []string) ([]Classified, error) {
	c, err := readContentOptional(path)
	if err != nil {
		return nil, err
	}

	type state struct {
		status   EntryStatus
		category string
	}
	index := make(map[string]state, len(c.entries))
	for _, e := range c.entries {
		st := StatusActive
		if e.Disabled {
			st = StatusDisabled
		}
		index[strings.ToLower(e.Value)] = state{status: st, category: e.Category}
	}

	out := make([]Classified, 0, len(values))
	for _, v := range values {
		if st, ok := index[strings.ToLower(v)]; ok {
			out = append(out, Classified{Value: v, Status: st.status, Category: st.category})
		} else {
			out = append(out, Classified{Value: v, Status: StatusNew})
		}
	}
	return out, nil
}

// Categories lists every category in render order: named ones first, the
// uncategorized bucket last, and only if it holds something.
func Categories(path string) ([]CategoryInfo, error) {
	c, err := readContentOptional(path)
	if err != nil {
		return nil, err
	}
	return c.categories(), nil
}

func (c *content) categories() []CategoryInfo {
	out := make([]CategoryInfo, 0, len(c.order)+1)
	for _, name := range c.order {
		out = append(out, CategoryInfo{Name: name})
	}

	byKey := make(map[string]*CategoryInfo, len(out))
	for i := range out {
		byKey[categoryKey(out[i].Name)] = &out[i]
	}

	var uncat CategoryInfo
	for _, e := range c.entries {
		info := &uncat
		if e.Category != Uncategorized {
			got, ok := byKey[categoryKey(e.Category)]
			if !ok {
				continue
			}
			info = got
		}
		if e.Disabled {
			info.Disabled++
		} else {
			info.Active++
		}
	}

	if uncat.Total() > 0 {
		out = append(out, uncat)
	}
	return out
}

// CategoryEntries returns one category's entries in the order they are
// written to the file.
func CategoryEntries(path, name string, listType EntryType) ([]LineEntry, error) {
	c, err := readContentOptional(path)
	if err != nil {
		return nil, err
	}
	if name != Uncategorized && c.indexOfCategory(name) < 0 {
		return nil, ErrCategoryNotFound
	}

	var group []LineEntry
	for _, e := range c.entries {
		if SameCategory(e.Category, name) {
			group = append(group, e)
		}
	}
	return sortGroup(group, listType), nil
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

func domainSortKey(host string) string {
	// Sort by base domain first, then by full hostname within the same domain.
	return baseDomain(host) + "\x00" + hostWithoutPath(host)
}

// sortGroup orders one category's entries; only domains are sorted, IP lists
// keep the order the user built them in.
func sortGroup(entries []LineEntry, listType EntryType) []LineEntry {
	if listType != TypeDomain || len(entries) < 2 {
		return entries
	}
	sorted := append([]LineEntry(nil), entries...)
	slices.SortFunc(sorted, func(a, b LineEntry) int {
		return strings.Compare(domainSortKey(a.Value), domainSortKey(b.Value))
	})
	return sorted
}

func renderEntry(e LineEntry) string {
	if e.Disabled {
		return disabledPrefix + e.Value
	}
	return e.Value
}

// renderLines lays the file out as named category sections in their existing
// order, with the uncategorized bucket last behind an explicit "// #uncat"
// marker so re-reading the file puts those entries back where they were.
func renderLines(c content, listType EntryType) []string {
	groups := make(map[string][]LineEntry, len(c.order))
	var uncat []LineEntry
	for _, e := range c.entries {
		if e.Category == Uncategorized {
			uncat = append(uncat, e)
			continue
		}
		groups[categoryKey(e.Category)] = append(groups[categoryKey(e.Category)], e)
	}

	var lines []string
	for _, name := range c.order {
		lines = append(lines, categoryPrefix+name)
		for _, e := range sortGroup(groups[categoryKey(name)], listType) {
			lines = append(lines, renderEntry(e))
		}
	}
	if len(uncat) > 0 {
		if len(c.order) > 0 {
			lines = append(lines, uncatMarker)
		}
		for _, e := range sortGroup(uncat, listType) {
			lines = append(lines, renderEntry(e))
		}
	}
	return lines
}

func writeContent(path string, c content, listType EntryType) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	lines := renderLines(c, listType)
	body := strings.Join(lines, "\n")
	if len(lines) > 0 {
		body += "\n"
	}

	tmp, err := os.CreateTemp(dir, ".lst-signbox-lists-tgbot-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(body); err != nil {
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

func lowerSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		set[strings.ToLower(v)] = struct{}{}
	}
	return set
}

// AddAll adds missing values and re-enables disabled ones. New entries land in
// category; values that already exist keep the category they are in.
func AddAll(path string, values []string, listType EntryType, category string) error {
	c, err := readContentOptional(path)
	if err != nil {
		return err
	}

	wanted := lowerSet(values)
	existing := make(map[string]struct{}, len(c.entries))
	for i := range c.entries {
		key := strings.ToLower(c.entries[i].Value)
		existing[key] = struct{}{}
		if _, ok := wanted[key]; ok {
			c.entries[i].Disabled = false
		}
	}

	c.appendNew(values, existing, category, false)
	return writeContent(path, c, listType)
}

// AddNew adds only values the file does not have yet.
func AddNew(path string, values []string, listType EntryType, category string) error {
	c, err := readContentOptional(path)
	if err != nil {
		return err
	}

	existing := make(map[string]struct{}, len(c.entries))
	for _, e := range c.entries {
		existing[strings.ToLower(e.Value)] = struct{}{}
	}

	c.appendNew(values, existing, category, false)
	return writeContent(path, c, listType)
}

// appendNew appends the values missing from existing into category, marking
// the caller's set as it goes so duplicates inside values are added once.
func (c *content) appendNew(values []string, existing map[string]struct{}, category string, disabled bool) {
	name := c.canonicalCategory(category)
	for _, v := range values {
		key := strings.ToLower(v)
		if _, ok := existing[key]; ok {
			continue
		}
		existing[key] = struct{}{}
		c.ensureCategory(name)
		c.entries = append(c.entries, LineEntry{Value: v, Disabled: disabled, Category: name})
	}
}

func Delete(path string, values []string, listType EntryType) error {
	c, err := readContent(path)
	if err != nil {
		return err
	}

	remove := lowerSet(values)
	kept := c.entries[:0]
	for _, e := range c.entries {
		if _, ok := remove[strings.ToLower(e.Value)]; ok {
			continue
		}
		kept = append(kept, e)
	}
	c.entries = kept
	return writeContent(path, c, listType)
}

// Disable disables the values already present and adds the missing ones as
// disabled entries in category.
func Disable(path string, values []string, listType EntryType, category string) error {
	c, err := readContentOptional(path)
	if err != nil {
		return err
	}

	toDisable := lowerSet(values)
	existing := make(map[string]struct{}, len(c.entries))
	for i := range c.entries {
		key := strings.ToLower(c.entries[i].Value)
		existing[key] = struct{}{}
		if _, ok := toDisable[key]; ok {
			c.entries[i].Disabled = true
		}
	}

	c.appendNew(values, existing, category, true)
	return writeContent(path, c, listType)
}

// DisableExistingOnly disables values already in the file and ignores the rest.
func DisableExistingOnly(path string, values []string, listType EntryType) error {
	c, err := readContent(path)
	if err != nil {
		return err
	}

	toDisable := lowerSet(values)
	for i := range c.entries {
		if _, ok := toDisable[strings.ToLower(c.entries[i].Value)]; ok {
			c.entries[i].Disabled = true
		}
	}
	return writeContent(path, c, listType)
}

// MoveToCategory retags existing entries; values not in the file are ignored.
func MoveToCategory(path string, values []string, category string, listType EntryType) (int, error) {
	c, err := readContent(path)
	if err != nil {
		return 0, err
	}

	name := c.canonicalCategory(category)
	move := lowerSet(values)
	moved := 0
	for i := range c.entries {
		if _, ok := move[strings.ToLower(c.entries[i].Value)]; !ok {
			continue
		}
		if SameCategory(c.entries[i].Category, name) {
			continue
		}
		c.entries[i].Category = name
		moved++
	}
	if moved == 0 {
		return 0, nil
	}
	c.ensureCategory(name)
	c.pruneEmptyCategories(name)
	return moved, writeContent(path, c, listType)
}

// pruneEmptyCategories drops headers left without entries, except keep, which
// the caller has just populated or wants preserved.
func (c *content) pruneEmptyCategories(keep string) {
	used := make(map[string]struct{}, len(c.order))
	for _, e := range c.entries {
		if e.Category != Uncategorized {
			used[categoryKey(e.Category)] = struct{}{}
		}
	}
	kept := c.order[:0]
	for _, name := range c.order {
		if _, ok := used[categoryKey(name)]; ok || SameCategory(name, keep) {
			kept = append(kept, name)
		}
	}
	c.order = kept
}

// SetCategoryEnabled disables or re-enables every entry of one category.
func SetCategoryEnabled(path, name string, enabled bool, listType EntryType) (int, error) {
	c, err := readContent(path)
	if err != nil {
		return 0, err
	}
	if name != Uncategorized && c.indexOfCategory(name) < 0 {
		return 0, ErrCategoryNotFound
	}

	changed := 0
	for i := range c.entries {
		if !SameCategory(c.entries[i].Category, name) {
			continue
		}
		if c.entries[i].Disabled == !enabled {
			continue
		}
		c.entries[i].Disabled = !enabled
		changed++
	}
	if changed == 0 {
		return 0, nil
	}
	return changed, writeContent(path, c, listType)
}

// DeleteCategoryKeepEntries removes the header and moves its entries into the
// uncategorized bucket, so nothing stops being routed.
func DeleteCategoryKeepEntries(path, name string, listType EntryType) (int, error) {
	if name == Uncategorized {
		return 0, ErrCategoryNotFound
	}
	c, err := readContent(path)
	if err != nil {
		return 0, err
	}
	if c.indexOfCategory(name) < 0 {
		return 0, ErrCategoryNotFound
	}

	moved := 0
	for i := range c.entries {
		if SameCategory(c.entries[i].Category, name) {
			c.entries[i].Category = Uncategorized
			moved++
		}
	}
	c.dropCategory(name)
	return moved, writeContent(path, c, listType)
}

// DeleteCategoryWithEntries removes the header and everything inside it.
func DeleteCategoryWithEntries(path, name string, listType EntryType) (int, error) {
	if name == Uncategorized {
		return 0, ErrCategoryNotFound
	}
	c, err := readContent(path)
	if err != nil {
		return 0, err
	}
	if c.indexOfCategory(name) < 0 {
		return 0, ErrCategoryNotFound
	}

	removed := 0
	kept := c.entries[:0]
	for _, e := range c.entries {
		if SameCategory(e.Category, name) {
			removed++
			continue
		}
		kept = append(kept, e)
	}
	c.entries = kept
	c.dropCategory(name)
	return removed, writeContent(path, c, listType)
}

// RenameCategory renames a category in place, refusing to silently merge into
// an existing one.
func RenameCategory(path, oldName, newName string, listType EntryType) error {
	if oldName == Uncategorized {
		return ErrCategoryNotFound
	}
	c, err := readContent(path)
	if err != nil {
		return err
	}
	idx := c.indexOfCategory(oldName)
	if idx < 0 {
		return ErrCategoryNotFound
	}
	newName = strings.TrimSpace(newName)
	if other := c.indexOfCategory(newName); other >= 0 && other != idx {
		return ErrCategoryExists
	}

	c.order[idx] = newName
	for i := range c.entries {
		if SameCategory(c.entries[i].Category, oldName) {
			c.entries[i].Category = newName
		}
	}
	return writeContent(path, c, listType)
}

// MergeCategory moves every entry of from into to and drops the empty header.
func MergeCategory(path, from, to string, listType EntryType) (int, error) {
	c, err := readContent(path)
	if err != nil {
		return 0, err
	}
	if from != Uncategorized && c.indexOfCategory(from) < 0 {
		return 0, ErrCategoryNotFound
	}
	if SameCategory(from, to) {
		return 0, nil
	}

	// The target may not exist yet: merging is also how a category is poured
	// into a brand new one made through "🆕 Новая категория".
	target := c.canonicalCategory(to)
	c.ensureCategory(target)
	moved := 0
	for i := range c.entries {
		if SameCategory(c.entries[i].Category, from) {
			c.entries[i].Category = target
			moved++
		}
	}
	if from != Uncategorized {
		c.dropCategory(from)
	}
	return moved, writeContent(path, c, listType)
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

// Misplaced returns the values that already exist but sit in a category other
// than target, mapped to the category they are actually in.
func Misplaced(classified []Classified, target string) map[string]string {
	out := make(map[string]string)
	for _, c := range classified {
		if c.Status == StatusNew {
			continue
		}
		if SameCategory(c.Category, target) {
			continue
		}
		out[c.Value] = c.Category
	}
	return out
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
