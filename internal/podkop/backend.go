package podkop

import (
	"errors"
	"os"
)

// Backend is the routing package the bot works with. podkop and forkop share
// the UCI layout of list files but differ in how a section names its action
// and keeps its proxy links.
type Backend struct {
	// Name is the package name: the UCI config, the CLI and the init script.
	Name  string
	Title string
}

var (
	Podkop = Backend{Name: "podkop", Title: "Podkop"}
	Forkop = Backend{Name: "forkop", Title: "Forkop"}
)

var (
	ErrBothInstalled = errors.New("на роутере установлены и podkop, и forkop — бот работает только с одним из них, удалите лишний")
	ErrNotInstalled  = errors.New("не установлен ни podkop, ни forkop")
)

func (b Backend) InitScript() string { return "/etc/init.d/" + b.Name }

func (b Backend) ConfigPath() string { return "/etc/config/" + b.Name }

func (b Backend) IsForkop() bool { return b.Name == Forkop.Name }

// groupTag is the outbound the section's links are grouped under, as the
// backend's clash_api expects it.
func (b Backend) groupTag(section string) string {
	if b.IsForkop() {
		return section + "-out"
	}
	return section
}

var installed = func(b Backend) bool {
	st, err := os.Stat(b.InitScript())
	return err == nil && !st.IsDir()
}

// Detect finds the one backend installed on the router.
func Detect() (Backend, error) {
	hasPodkop, hasForkop := installed(Podkop), installed(Forkop)
	switch {
	case hasPodkop && hasForkop:
		return Backend{}, ErrBothInstalled
	case hasForkop:
		return Forkop, nil
	case hasPodkop:
		return Podkop, nil
	}
	return Backend{}, ErrNotInstalled
}

var detect = Detect
