package bot

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	pathpkg "path"
	"strings"

	"lst-signbox-lists-tgbot/internal/lists"
	"lst-signbox-lists-tgbot/internal/podkop"
)

// listTarget is the one file a screen works on. Nothing operates on "the"
// domain list any more: every list belongs to a podkop section, and a section
// can be fed from several files at once.
type listTarget struct {
	// Section is the podkop section name, empty for the fallback below.
	Section string
	Type    lists.EntryType
	Path    string
}

// fallbackSectionToken stands for the synthetic section the bot shows when
// podkop's config cannot be read — a router running plain sing-box still has
// the file pair from the bot's own settings, and hiding it would leave such a
// setup with no way in.
const fallbackSectionToken = "cfg"

// fallbackSectionName is what that synthetic section is called on screen.
const fallbackSectionName = "Файлы из настроек бота"

var (
	// errSectionGone: the config changed under a button pressed later.
	errSectionGone = errors.New("секции больше нет")
	// errFileGone: same, for a path that left the section's list.
	errFileGone = errors.New("файла больше нет в секции")
	// errNotBound: the section has no list of that type at all.
	errNotBound = errors.New("список не привязан к секции")
)

// shortToken derives a stable id that survives in a callback or a command,
// where the value itself would not fit.
func shortToken(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:4])
}

// sectionToken keys a podkop section. Section names are case-sensitive in UCI,
// so the token is too.
func sectionToken(name string) string {
	if name == "" {
		return fallbackSectionToken
	}
	return shortToken(name)
}

func pathToken(path string) string { return shortToken(path) }

func sectionDisplayName(name string) string {
	if name == "" {
		return fallbackSectionName
	}
	return name
}

// sections lists podkop's routing sections, falling back to the bot's own
// file pair when there is no podkop config to read.
func (a *App) sections(ctx context.Context) []podkop.Section {
	read := a.readSections
	if read == nil {
		read = podkop.Sections
	}
	secs, err := read(ctx)
	if err == nil && len(secs) > 0 {
		return secs
	}
	if err != nil {
		a.logf(0, "podkop_sections_error err=%v fallback=config", err)
	}
	return []podkop.Section{a.fallbackSection()}
}

// fallbackSection wraps the configured file pair as if it were a section, so
// every screen below can stay written against sections only.
func (a *App) fallbackSection() podkop.Section {
	var s podkop.Section
	if a.cfg.DomainList != "" {
		s.DomainLists = []string{a.cfg.DomainList}
	}
	if a.cfg.IPList != "" {
		s.SubnetLists = []string{a.cfg.IPList}
	}
	return s
}

// findSection resolves a token back to the section it was made from. A section
// renamed or deleted since the button was drawn simply resolves to nothing.
func (a *App) findSection(ctx context.Context, token string) (podkop.Section, error) {
	for _, s := range a.sections(ctx) {
		if sectionToken(s.Name) == token {
			return s, nil
		}
	}
	return podkop.Section{}, errSectionGone
}

// resolveTarget picks the file a callback points at. An empty fileToken means
// "the section's only file", which is what the screens send when there is
// nothing to choose between.
func (a *App) resolveTarget(ctx context.Context, secToken, fileToken string, t lists.EntryType) (listTarget, error) {
	s, err := a.findSection(ctx, secToken)
	if err != nil {
		return listTarget{}, err
	}
	paths := s.Lists(t)
	if len(paths) == 0 {
		return listTarget{}, errNotBound
	}
	if fileToken == "" {
		if len(paths) > 1 {
			return listTarget{}, errFileGone
		}
		return listTarget{Section: s.Name, Type: t, Path: paths[0]}, nil
	}
	for _, p := range paths {
		if pathToken(p) == fileToken {
			return listTarget{Section: s.Name, Type: t, Path: p}, nil
		}
	}
	return listTarget{}, errFileGone
}

// targetError turns a resolution failure into something a user can act on.
func targetError(err error) string {
	switch {
	case errors.Is(err, errSectionGone):
		return "🤷 Такой секции больше нет — откройте список секций заново."
	case errors.Is(err, errFileGone):
		return "🤷 Этого файла больше нет в секции — откройте секцию заново."
	case errors.Is(err, errNotBound):
		return "🔗 К этой секции не привязан такой список — привяжите файл в карточке секции."
	default:
		return "❌ Ошибка: " + err.Error()
	}
}

// allPaths is every list file the bot can reach, for the startup check.
func (a *App) allPaths(ctx context.Context) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, s := range a.sections(ctx) {
		for _, p := range append(append([]string{}, s.DomainLists...), s.SubnetLists...) {
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

// sectionsWith lists the sections that can take entries of this type, which is
// what the "where should this go?" picker offers.
func (a *App) sectionsWith(ctx context.Context, t lists.EntryType) []podkop.Section {
	var out []podkop.Section
	for _, s := range a.sections(ctx) {
		if len(s.Lists(t)) > 0 {
			out = append(out, s)
		}
	}
	return out
}

// sharedWith names the other sections fed from the same file, so an edit that
// reaches further than one section does not look local.
func (a *App) sharedWith(ctx context.Context, tgt listTarget) []string {
	var others []string
	for _, s := range a.sections(ctx) {
		if s.Name == tgt.Section {
			continue
		}
		for _, p := range s.Lists(tgt.Type) {
			if p == tgt.Path {
				others = append(others, sectionDisplayName(s.Name))
				break
			}
		}
	}
	return others
}

// fileLabel is how a file is named on a button: the file name alone, since the
// directory is the same for every list on a normal install.
func fileLabel(p string) string {
	base := pathpkg.Base(p)
	if base == "" || base == "." || base == "/" {
		return p
	}
	return base
}

// targetLine tells the user which file the screen is about to change.
func targetLine(tgt listTarget) string {
	return fmt.Sprintf("🗂 %s · %s", sectionDisplayName(tgt.Section), tgt.Path)
}

// sectionBindingLine describes one of a section's two list slots.
func sectionBindingLine(s podkop.Section, t lists.EntryType) string {
	title := "Домены"
	if t == lists.TypeIP {
		title = "IP/CIDR"
	}
	paths := s.Lists(t)
	if len(paths) == 0 {
		return fmt.Sprintf("📄 %s: не привязан", title)
	}
	return fmt.Sprintf("📄 %s: %s", title, strings.Join(paths, "\n     "))
}

// sectionButtonLabel keeps the section list readable: the name, what podkop
// does with the section, and which of the two lists it actually has.
func sectionButtonLabel(s podkop.Section) string {
	label := sectionDisplayName(s.Name)
	if s.ConnectionType != "" {
		label += " · " + s.ConnectionType
	}
	switch {
	case len(s.DomainLists) > 0 && len(s.SubnetLists) > 0:
		label += " · домены+IP"
	case len(s.DomainLists) > 0:
		label += " · домены"
	case len(s.SubnetLists) > 0:
		label += " · IP"
	default:
		label += " · нет списков"
	}
	return label
}
