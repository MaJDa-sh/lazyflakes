package ui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
)

// installedRows are the indices of m.sys.items that are already installed:
// packages, or with settings set the options set through lazyflakes.
func (s sysState) installedRows(settings bool) []int {
	var rows []int
	for i, it := range s.items {
		if it.orig != "" && it.setting == settings {
			rows = append(rows, i)
		}
	}
	return rows
}

// changed reports whether an installed item moves to another channel, or
// for a setting gets another value.
func (it sysItem) changed() bool {
	if it.setting {
		return it.value != it.origValue
	}
	return it.entry.Channel != it.orig
}

// pending counts installed items marked for removal or a change.
func (s sysState) pending(settings bool) int {
	n := 0
	for _, i := range s.installedRows(settings) {
		if it := s.items[i]; it.removed || it.changed() {
			n++
		}
	}
	return n
}

// toggleRemoval marks an installed package for removal, or unmarks it.
func (m *Model) toggleRemoval(it *sysItem) {
	it.removed = !it.removed
	if it.removed {
		m.status = it.entry.Attr + " will be removed when you apply · enter to review and apply"
	} else {
		m.status = it.entry.Attr + " stays installed"
	}
}

func (m Model) handleInstalledKey(key string) (tea.Model, tea.Cmd) {
	s := &m.sys
	rows := s.installedRows(false)
	switch key {
	case "q":
		return m, tea.Quit
	case "t":
		return m, m.switchTab()
	case "T":
		return m, m.switchTab(true)
	case "tab":
		m.focus = focusPackages
	case "shift+tab":
		m.focus = focusSelected
	case "h", "left":
		m.focus = focusPackages
	case "/":
		return m.focusSearch()
	case "o":
		m.openOptions()
	case "j", "down":
		m.instCursor = min(m.instCursor+1, max(0, len(rows)-1))
	case "k", "up":
		m.instCursor = max(m.instCursor-1, 0)
	case " ", "d", "x":
		if m.instCursor < len(rows) {
			m.toggleRemoval(&s.items[rows[m.instCursor]])
		}
	case "c":
		if m.instCursor < len(rows) {
			it := &s.items[rows[m.instCursor]]
			m.cycleSystemChannel(it)
			if it.entry.Channel == it.orig {
				m.status = it.entry.Attr + " stays on " + it.orig
			} else {
				m.status = fmt.Sprintf("%s moves %s → %s when you apply · enter to review and apply", it.entry.Attr, it.orig, it.entry.Channel)
			}
		}
	case "u":
		for _, i := range rows {
			s.items[i].removed, s.items[i].entry.Channel = false, s.items[i].orig
		}
		m.status = "pending changes undone"
	case "[", "]":
		if s.err == nil && len(s.targets) > 1 {
			step := 1
			if key == "[" {
				step = len(s.targets) - 1
			}
			m.instCursor = 0
			return m, m.syncSystem((s.target+step)%len(s.targets), false, false)
		}
	case "enter", "a":
		return m, m.openSystem()
	}
	return m, nil
}

// installedView is the [3] Installed panel: what lazyflakes installed into
// the current target, with removals and channel changes marked.
func (m Model) installedView(w, h int) string {
	inner := w - 2
	s := m.sys
	active := m.focus == focusInstalled
	title := "[3] Installed"

	var lines []string
	switch {
	case errors.Is(s.err, config.ErrNoConfig):
		lines = append(lines, dimStyle.Render(" not set up yet: press a to install into your system"))
	case s.err != nil:
		lines = append(lines, errStyle.Render(" "+s.err.Error()))
	default:
		rows := s.installedRows(false)
		title = fmt.Sprintf("[3] Installed %d", len(rows))
		if len(s.targets) > 1 {
			title += " · " + s.targets[s.target].Name + " ([ ] switch)"
		}
		if len(rows) == 0 {
			lines = append(lines, dimStyle.Render(" nothing installed through lazyflakes yet"))
		}
		start := max(0, m.instCursor-(h-2)+1)
		for k := start; k < len(rows); k++ {
			it := s.items[rows[k]]
			mark, tag := "  ", it.entry.Channel+" "+m.versionOf(it.entry.Attr, it.entry.Channel)
			switch {
			case it.removed:
				mark, tag = "- ", "remove on apply"
			case it.entry.Channel != it.orig:
				mark, tag = "~ ", it.orig+" → "+it.entry.Channel+" on apply"
			}
			attr := ansi.Truncate(it.entry.Attr, max(1, inner-5-ansi.StringWidth(tag)), "…")
			gap := strings.Repeat(" ", max(1, inner-3-ansi.StringWidth(mark+attr+tag)))
			text := mark + attr + gap + tag
			switch {
			case active && k == m.instCursor && it.removed:
				lines = append(lines, " "+cursorRemoveStyle.Render(text)+" ")
			case active && k == m.instCursor:
				lines = append(lines, " "+cursorStyle.Render(text)+" ")
			case it.removed:
				lines = append(lines, " "+errStyle.Render(text))
			case mark != "  ":
				lines = append(lines, " "+labelStyle.Render(text))
			default:
				lines = append(lines, " "+mark+attr+gap+channelStyle(max(0, m.channelIndex(it.entry.Channel))).Render(tag))
			}
		}
	}

	footer := "d remove · c channel · enter apply"
	if p := s.pending(false); p > 0 {
		footer = fmt.Sprintf("%d pending · enter review & apply · u undo", p)
	}
	if !active && s.pending(false) == 0 {
		footer = "3 to focus"
	}
	return panel(title, footer, lines, w, h, active)
}
