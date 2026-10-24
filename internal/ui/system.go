package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/generate"
	"github.com/MaJDa-sh/lazyflakes/internal/managed"
)

// sysItem is one row of the system popup: a package already in the managed
// module, or one about to be added.
type sysItem struct {
	entry   managed.Entry
	orig    string // channel in the module; "" for a package being added
	removed bool
}

// sysState is the system popup: the changes to one target's managed module.
type sysState struct {
	targets  []*config.Target
	target   int
	items    []sysItem
	cursor   int
	opts     generate.Options // options already set in the module
	exists   bool
	imported bool
	err      error
}

type appliedMsg struct {
	target  string
	summary string
	rebuilt bool
	err     error
}

type setupDoneMsg struct{ err error }

// openSystem (re)reads the config and shows the system popup.
func (m *Model) openSystem() {
	m.sys = sysState{}
	m.popup = popupSystem
	cfg, err := config.Load(m.configPath)
	if err != nil {
		m.sys.err = err
		return
	}
	m.sys.targets = cfg.Ordered()
	if len(m.sys.targets) == 0 {
		m.sys.err = config.ErrNoConfig
		return
	}
	m.loadTarget(0)
}

// loadTarget reads target i's module and adds the current selection to it.
func (m *Model) loadTarget(i int) {
	s := &m.sys
	s.target, s.cursor, s.items, s.err = i, 0, nil, nil
	t := s.targets[i]
	mod, exists, err := managed.Read(t.File)
	if err != nil {
		s.err = err
		return
	}
	s.exists, s.opts = exists, mod.Options
	s.imported = exists && managed.Imported(t)
	present := map[string]bool{}
	for _, e := range mod.Packages {
		s.items = append(s.items, sysItem{entry: e, orig: e.Channel})
		present[e.Attr] = true
	}
	for _, sel := range m.selected {
		attr := m.pkgs[sel.pkg].Attr
		if !present[attr] {
			s.items = append(s.items, sysItem{entry: managed.Entry{Channel: m.channels[sel.channel].Name, Attr: attr}})
			present[attr] = true
		}
	}
}

func (m Model) handleSystemKey(key string) (tea.Model, tea.Cmd) {
	s := &m.sys
	if s.err != nil {
		switch key {
		case "esc", "q":
			m.popup = popupNone
		case "enter":
			if errors.Is(s.err, config.ErrNoConfig) {
				return m, m.runSetup()
			}
		}
		return m, nil
	}

	switch key {
	case "esc", "q":
		m.popup = popupNone
	case "j", "down":
		s.cursor = min(s.cursor+1, max(0, len(s.items)-1))
	case "k", "up":
		s.cursor = max(s.cursor-1, 0)
	case "tab":
		m.loadTarget((s.target + 1) % len(s.targets))
	case "shift+tab":
		m.loadTarget((s.target + len(s.targets) - 1) % len(s.targets))
	case " ", "d", "x":
		if len(s.items) == 0 {
			break
		}
		if it := &s.items[s.cursor]; it.orig == "" {
			s.items = append(s.items[:s.cursor], s.items[s.cursor+1:]...)
			s.cursor = min(s.cursor, max(0, len(s.items)-1))
		} else {
			it.removed = !it.removed
		}
	case "c":
		if len(s.items) > 0 {
			m.cycleSystemChannel(&s.items[s.cursor])
		}
	case "enter":
		return m, m.applySystem(true)
	case "s":
		return m, m.applySystem(false)
	}
	return m, nil
}

// cycleSystemChannel moves an item to the next channel the target can
// install from.
func (m *Model) cycleSystemChannel(it *sysItem) {
	t := m.sys.targets[m.sys.target]
	cur := m.channelIndex(it.entry.Channel)
	for step := 1; step <= len(m.channels); step++ {
		c := m.channels[(cur+step)%len(m.channels)].Name
		if _, ok := t.Sets[c]; ok {
			it.entry.Channel = c
			return
		}
	}
}

func (m Model) channelIndex(name string) int {
	for i, c := range m.channels {
		if c.Name == name {
			return i
		}
	}
	return -1
}

// changes counts what applying the popup would do.
func (s sysState) changes() (added, removed, moved int) {
	for _, it := range s.items {
		switch {
		case it.orig == "" && !it.removed:
			added++
		case it.orig != "" && it.removed:
			removed++
		case it.orig != "" && it.entry.Channel != it.orig:
			moved++
		}
	}
	return
}

// applySystem writes the target's module and, if rebuild is set, runs its
// rebuild command, all in the terminal so sudo can ask for a password.
func (m *Model) applySystem(rebuild bool) tea.Cmd {
	s := m.sys
	t := s.targets[s.target]
	mod := managed.Module{Options: s.opts | m.opts}
	for _, it := range s.items {
		if it.removed {
			continue
		}
		if _, ok := t.Sets[it.entry.Channel]; !ok {
			m.status = fmt.Sprintf("error: %s has no %s package set; press c to switch channel or add it to the config", t.Name, it.entry.Channel)
			return nil
		}
		mod.Packages = append(mod.Packages, it.entry)
	}
	added, removed, moved := s.changes()
	if added+removed+moved == 0 && mod.Options == s.opts && s.exists && !rebuild {
		m.status = "nothing to save"
		return nil
	}

	content, err := managed.Render(t, mod)
	if err != nil {
		m.status = "error: " + err.Error()
		return nil
	}
	plan, err := managed.NewPlan(t, content, rebuild)
	if err != nil {
		m.status = "error: " + err.Error()
		return nil
	}
	m.popup = popupNone

	summary := fmt.Sprintf("+%d −%d", added, removed)
	if moved > 0 {
		summary += fmt.Sprintf(" ~%d", moved)
	}
	what := "saving " + t.File
	if plan.Rebuild {
		what = "updating " + t.Name
	}
	cmd := &termCmd{
		cmd:    exec.Command("sh", "-c", plan.Script()),
		banner: fmt.Sprintf("lazyflakes: %s (%s packages)\n", what, summary),
		pause:  true,
	}
	return tea.Exec(cmd, func(err error) tea.Msg {
		plan.Cleanup()
		return appliedMsg{target: t.Name, summary: summary, rebuilt: plan.Rebuild, err: err}
	})
}

// runSetup runs `lazyflakes setup` in the terminal and reopens the popup.
func (m *Model) runSetup() tea.Cmd {
	self, err := os.Executable()
	if err != nil {
		self = "lazyflakes"
	}
	cmd := &termCmd{cmd: exec.Command(self, "setup", "-config", m.configPath), pause: true}
	return tea.Exec(cmd, func(err error) tea.Msg { return setupDoneMsg{err: err} })
}

// systemView is the popup listing the target's packages and pending changes.
func (m Model) systemView() string {
	w := min(m.width-4, 96)
	inner := w - 2
	s := m.sys

	if s.err != nil {
		var lines []string
		if errors.Is(s.err, config.ErrNoConfig) {
			lines = []string{"",
				" No system targets are configured yet.", "",
				" Setup detects your NixOS, nix-darwin or home-manager configuration and",
				" writes " + displayPath(m.configPath) + ". After that, lazyflakes keeps",
				" one module (lazyflakes.nix) that you import once from your config.", "",
				" Press " + keyStyle.Render("enter") + " to run " + keyStyle.Render("lazyflakes setup") + " now.", ""}
		} else {
			lines = append([]string{""}, wrap(" "+errStyle.Render("error: ")+s.err.Error(), inner-2)...)
			lines = append(lines, "", dimStyle.Render(" fix the config and press a again"))
		}
		return panel("Install into system", "enter setup · esc close", lines, w, len(lines)+2, true)
	}

	t := s.targets[s.target]
	attrW := max(10, inner-46)
	var lines []string
	for i, it := range s.items {
		mark, note := "  ", ""
		switch {
		case it.removed:
			mark, note = "- ", "remove"
		case it.orig == "":
			mark, note = "+ ", "add"
		case it.entry.Channel != it.orig:
			mark, note = "~ ", it.orig+" → "+it.entry.Channel
		}
		channel := it.entry.Channel
		version := m.versionOf(it.entry.Attr, channel)
		if _, ok := t.Sets[channel]; !ok && !it.removed {
			note = "no " + channel + " set in config"
		}
		text := fmt.Sprintf("%s%s %-9s %-14s %s", mark, padTo(ansi.Truncate(it.entry.Attr, attrW, "…"), attrW), channel,
			ansi.Truncate(version, 14, "…"), note)
		switch {
		case i == s.cursor:
			lines = append(lines, cursorStyle.Render(padTo(" "+text, inner)))
		case strings.HasPrefix(note, "no "):
			lines = append(lines, " "+errStyle.Render(text))
		case it.removed:
			lines = append(lines, " "+errStyle.Render(text))
		case it.orig == "" || it.entry.Channel != it.orig:
			lines = append(lines, " "+markStyle.Render(text))
		default:
			lines = append(lines, " "+text)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render(" nothing installed through lazyflakes yet: select packages with space and press a"))
	}

	opts := s.opts | m.opts
	optText := dimStyle.Render("none")
	if keys := opts.Keys(); len(keys) > 0 {
		optText = strings.Join(keys, ", ")
	}
	file := displayPath(t.File)
	if !s.exists {
		file += dimStyle.Render(" (will be created)")
	}
	rebuild := t.Rebuild
	if rebuild == "" {
		rebuild = dimStyle.Render("none configured, s only writes the file")
	}
	lines = append(lines, "",
		" "+labelStyle.Render("options  ")+optText,
		" "+labelStyle.Render("file     ")+file,
		" "+labelStyle.Render("rebuild  ")+rebuild,
	)
	if !s.imported {
		lines = append(lines, "", " "+errStyle.Render("! ")+
			fmt.Sprintf("not imported yet: add ./%s to imports in your configuration", filepath.Base(t.File)))
	}

	title := "Install into " + t.Name
	if len(s.targets) > 1 {
		title += fmt.Sprintf(" (%d/%d, tab to switch)", s.target+1, len(s.targets))
	}
	h := min(m.height-2, len(lines)+2)
	return panel(title, "enter apply + rebuild · s save only · d remove · c channel · esc", lines, w, h, true)
}

// versionOf looks up attr's version in the named channel, "" when unknown.
func (m Model) versionOf(attr, channel string) string {
	c := m.channelIndex(channel)
	p, ok := m.attrIndex[attr]
	if c < 0 || !ok {
		return ""
	}
	return m.versions[p][c]
}
