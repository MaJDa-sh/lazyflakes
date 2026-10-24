// Package ui implements the lazyflakes terminal interface with Bubble Tea.
package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/kacper/lazyflakes/internal/nix"
	"github.com/kacper/lazyflakes/internal/search"
)

// focus is the panel that currently receives key presses (lazygit calls these
// "contexts").
type focus int

const (
	focusPackages focus = iota
	focusSearch
	focusSelected
)

type loadedMsg struct {
	pkgs   []nix.Package
	cached bool
	took   time.Duration
	err    error
}

type Model struct {
	flakeRef string
	refresh  bool // ignore the cache on the first load

	pkgs     []nix.Package
	index    *search.Index
	filtered []int // indices into pkgs, ranked by the current query
	cursor   int   // position in filtered
	offset   int   // first visible row of filtered

	selected  []int // indices into pkgs, in the order they were marked
	selCursor int

	input   textinput.Model
	spinner spinner.Model
	focus   focus

	loading bool
	status  string
	err     error

	width, height int
}

func New(flakeRef string, refresh bool) Model {
	in := textinput.New()
	in.Prompt = " "
	in.Placeholder = "type / to search packages…"

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	m := Model{flakeRef: flakeRef, refresh: refresh, input: in, spinner: sp, loading: true}
	m.status = "loading " + flakeRef
	if refresh {
		m.status = "refreshing " + flakeRef
	}
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.spinner.Tick, load(m.flakeRef, m.refresh))
}

func load(flakeRef string, refresh bool) tea.Cmd {
	return func() tea.Msg {
		start := time.Now()
		pkgs, cached, err := nix.Load(context.Background(), flakeRef, refresh)
		return loadedMsg{pkgs: pkgs, cached: cached, took: time.Since(start), err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampScroll()
		return m, nil

	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case loadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.pkgs = msg.pkgs
		m.index = search.NewIndex(m.pkgs)
		m.selected = nil
		m.refilter()
		src := "evaluated"
		if msg.cached {
			src = "from cache"
		}
		m.status = fmt.Sprintf("%d packages %s in %s", len(m.pkgs), src, msg.took.Round(time.Millisecond))
		return m, nil

	case tea.MouseMsg:
		if m.focus != focusSearch && !m.loading {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.move(-3)
			case tea.MouseButtonWheelDown:
				m.move(3)
			}
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if m.loading {
		if key == "q" {
			return m, tea.Quit
		}
		return m, nil
	}

	switch m.focus {
	case focusSearch:
		switch key {
		case "esc":
			m.input.SetValue("")
			m.refilter()
			return m.blurSearch(), nil
		case "enter":
			return m.blurSearch(), nil
		case "up", "ctrl+k", "ctrl+p":
			m.move(-1)
			return m, nil
		case "down", "ctrl+j", "ctrl+n":
			m.move(1)
			return m, nil
		}
		before := m.input.Value()
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if m.input.Value() != before {
			m.refilter()
		}
		return m, cmd

	case focusSelected:
		switch key {
		case "q":
			return m, tea.Quit
		case "tab", "shift+tab", "h", "left":
			m.focus = focusPackages
		case "j", "down":
			m.selCursor = min(m.selCursor+1, max(0, len(m.selected)-1))
		case "k", "up":
			m.selCursor = max(m.selCursor-1, 0)
		case " ", "d", "x":
			if len(m.selected) > 0 {
				m.selected = append(m.selected[:m.selCursor], m.selected[m.selCursor+1:]...)
				m.selCursor = min(m.selCursor, max(0, len(m.selected)-1))
			}
		case "/":
			return m.focusSearch()
		}
		return m, nil
	}

	// focusPackages
	switch key {
	case "q":
		return m, tea.Quit
	case "/":
		return m.focusSearch()
	case "esc":
		if m.input.Value() != "" {
			m.input.SetValue("")
			m.refilter()
		}
	case "tab", "shift+tab", "l", "right":
		m.focus = focusSelected
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)
	case "ctrl+d", "pgdown":
		m.move(m.listHeight() / 2)
	case "ctrl+u", "pgup":
		m.move(-m.listHeight() / 2)
	case "g", "home":
		m.move(-len(m.filtered))
	case "G", "end":
		m.move(len(m.filtered))
	case " ":
		if p, ok := m.current(); ok {
			m.toggle(p)
			m.move(1)
		}
	case "ctrl+r":
		m.loading = true
		m.status = "refreshing " + m.flakeRef
		return m, tea.Batch(m.spinner.Tick, load(m.flakeRef, true))
	}
	return m, nil
}

func (m Model) focusSearch() (tea.Model, tea.Cmd) {
	m.focus = focusSearch
	return m, m.input.Focus()
}

func (m Model) blurSearch() Model {
	m.focus = focusPackages
	m.input.Blur()
	return m
}

func (m *Model) refilter() {
	if m.index == nil {
		return
	}
	m.filtered = m.index.Filter(m.input.Value())
	m.cursor, m.offset = 0, 0
}

func (m *Model) move(delta int) {
	m.cursor = max(0, min(m.cursor+delta, len(m.filtered)-1))
	m.clampScroll()
}

// clampScroll keeps the cursor inside the visible window of the list.
func (m *Model) clampScroll() {
	h := m.listHeight()
	if h <= 0 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
}

// current returns the pkgs index under the cursor.
func (m Model) current() (int, bool) {
	if m.cursor < 0 || m.cursor >= len(m.filtered) {
		return 0, false
	}
	return m.filtered[m.cursor], true
}

func (m Model) isSelected(p int) bool {
	for _, s := range m.selected {
		if s == p {
			return true
		}
	}
	return false
}

func (m *Model) toggle(p int) {
	for i, s := range m.selected {
		if s == p {
			m.selected = append(m.selected[:i], m.selected[i+1:]...)
			m.selCursor = min(m.selCursor, max(0, len(m.selected)-1))
			return
		}
	}
	m.selected = append(m.selected, p)
}

// Selected returns the packages the user marked, for later flake generation.
func (m Model) Selected() []nix.Package {
	out := make([]nix.Package, len(m.selected))
	for i, p := range m.selected {
		out[i] = m.pkgs[p]
	}
	return out
}
