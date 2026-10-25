package ui

import (
	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/managed"
)

// installation is a package lazyflakes installed into a target.
type installation struct {
	target  string
	channel string
}

// loadInstalled reads every target's managed module so the package list can
// mark what is already installed. Without a config nothing is marked.
func (m *Model) loadInstalled() {
	m.installed = map[string][]installation{}
	cfg, err := config.Load(m.configPath)
	if err != nil {
		return
	}
	for _, t := range cfg.Ordered() {
		mod, _, err := managed.Read(t.File)
		if err != nil {
			continue
		}
		for _, e := range mod.Packages {
			m.installed[e.Attr] = append(m.installed[e.Attr], installation{target: t.Name, channel: e.Channel})
		}
	}
}

// listMark is the marker in front of package p: a filled dot for the current
// selection, a hollow one for a package lazyflakes installed, each in the
// color of its channel.
func (m Model) listMark(p int) string {
	if s := m.selectionOf(p); s >= 0 {
		return channelStyle(m.selected[s].channel).Bold(true).Render("● ")
	}
	if in := m.installed[m.pkgs[p].Attr]; len(in) > 0 {
		if c := m.channelIndex(in[0].channel); c >= 0 {
			return channelStyle(c).Render("○ ")
		}
		return markStyle.Render("○ ")
	}
	return "  "
}
