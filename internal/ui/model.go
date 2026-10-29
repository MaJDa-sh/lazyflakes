// Package ui implements the lazyflakes terminal interface with Bubble Tea.
package ui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaJDa-sh/lazyflakes/internal/generate"
	"github.com/MaJDa-sh/lazyflakes/internal/nix"
	"github.com/MaJDa-sh/lazyflakes/internal/search"
)

// focus is the panel that currently receives key presses (lazygit calls these
// "contexts").
type focus int

const (
	focusPackages focus = iota
	focusSearch
	focusSelected
	focusInstalled
)

// popup is a modal drawn over the panels; while open it takes all keys.
type popup int

const (
	popupNone popup = iota
	popupGenerate
	popupConfirm
	popupOptions
	popupSystem
	popupValue // the value editor of the Options tab
)

type loadedMsg struct {
	channel int
	pkgs    []nix.Package
	cached  bool
	took    time.Duration
	err     error
}

// channelLoad tracks loading one channel's package list.
type channelLoad struct {
	done bool
	pkgs []nix.Package
	info string // "from cache in 300ms", shown in the status line
	err  error
}

// selection is a package marked for the shell, taken from one channel.
type selection struct {
	pkg     int // index into pkgs
	channel int // index into channels
}

type Model struct {
	channels []generate.Channel
	channel  int    // channel new selections are taken from
	refresh  bool   // ignore the cache on the first load
	dir      string // where generated files are written

	pkgs      []nix.Package // union of all channels, sorted by attr
	versions  [][]string    // versions[pkg][channel], "" when missing
	attrIndex map[string]int
	index     *search.Index
	filtered  []int // indices into pkgs, ranked by the current query
	cursor    int   // position in filtered
	offset    int   // first visible row of filtered

	selected  []selection // in the order they were marked
	selCursor int

	popup     popup
	genCursor int  // index into generate.Formats
	envrc     bool // also write .envrc for direnv

	opts      generate.Options // nixpkgs config for files and shells
	optCursor int              // index into generate.AllOptions
	optReturn popup            // popup to go back to when options close

	configPath string                    // config.toml with the system targets
	sys        sysState                  // pending changes, shared by the Installed panel and install popup
	instCursor int                       // position in the Installed panel
	installed  map[string][]installation // attr -> where lazyflakes installed it

	input   textinput.Model
	spinner spinner.Model
	focus   focus

	// The Options tab.
	tab        tab
	queries    [2]string              // the search text of each tab
	optData    map[string]*optionData // option lists by target name
	olFiltered []int                  // indices into the current target's options
	olCursor   int
	olOffset   int
	pendingSet []pendingSetting // options chosen and not applied yet
	pendCursor int
	setCursor  int
	edit       *valueEditor

	loads   []channelLoad
	loading bool
	status  string
	err     error

	width, height int
}

func New(channels []generate.Channel, channel int, refresh bool, dir string, format generate.Format, envrc bool, opts generate.Options, configPath string) Model {
	in := textinput.New()
	in.Prompt = " "
	in.Placeholder = "type / to search packages…"

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return Model{
		channels: channels, channel: channel, refresh: refresh, dir: dir,
		genCursor: int(format), envrc: envrc, opts: opts, configPath: configPath, input: in, spinner: sp,
	}.startLoading(refresh)
}

func (m Model) Init() tea.Cmd {
	return m.loadAll(m.refresh)
}

// startLoading resets the per-channel load state; loadAll starts the loads.
func (m Model) startLoading(refresh bool) Model {
	m.loading = true
	m.loads = make([]channelLoad, len(m.channels))
	m.status = "loading"
	if refresh {
		m.status = "refreshing"
	}
	return m
}

func (m Model) loadAll(refresh bool) tea.Cmd {
	cmds := []tea.Cmd{m.spinner.Tick}
	for i, c := range m.channels {
		cmds = append(cmds, load(i, c.Ref, refresh))
	}
	return tea.Batch(cmds...)
}

func load(channel int, flakeRef string, refresh bool) tea.Cmd {
	return func() tea.Msg {
		start := time.Now()
		pkgs, cached, err := nix.Load(context.Background(), flakeRef, refresh)
		return loadedMsg{channel: channel, pkgs: pkgs, cached: cached, took: time.Since(start), err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampScroll()
		return m, nil

	case optionsLoadedMsg:
		m.optionsLoaded(msg)
		return m, nil

	case spinner.TickMsg:
		if !m.loading && !m.optionsBusy() {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case loadedMsg:
		m.loadedChannel(msg)
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

	case shellExitedMsg:
		if msg.err != nil {
			m.status = "error: shell exited: " + msg.err.Error()
		} else {
			m.status = "back from shell with " + strings.Join(msg.labels, ", ")
		}
		return m, nil

	case appliedMsg:
		m.loadInstalled()
		m.syncSystem(m.sys.target, msg.err != nil, false)
		m.instCursor = 0
		if msg.err != nil {
			m.status = fmt.Sprintf("error: updating %s failed, nothing was changed (%v)", msg.target, msg.err)
		} else if msg.script {
			m.status = fmt.Sprintf("%s updated by its script: %s", msg.target, msg.summary)
		} else if msg.rebuilt {
			m.status = fmt.Sprintf("%s updated: %s", msg.target, msg.summary)
		} else {
			m.status = fmt.Sprintf("%s saved without rebuilding: %s", msg.target, msg.summary)
		}
		return m, nil

	case setupDoneMsg:
		m.loadInstalled()
		return m, m.openSystem()

	case importCheckedMsg:
		m.importChecked(msg)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// loadedChannel records one channel's result and builds the catalog once
// every channel has finished loading.
func (m *Model) loadedChannel(msg loadedMsg) {
	src := "evaluated"
	if msg.cached {
		src = "from cache"
	}
	m.loads[msg.channel] = channelLoad{
		done: true, pkgs: msg.pkgs, err: msg.err,
		info: fmt.Sprintf("%s %s in %s", m.channels[msg.channel].Name, src, msg.took.Round(time.Millisecond)),
	}

	lists := make([][]nix.Package, len(m.loads))
	var infos []string
	var errs []error
	for i, l := range m.loads {
		if !l.done {
			return
		}
		if l.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.channels[i].Name, l.err))
		}
		lists[i] = l.pkgs
		infos = append(infos, l.info)
	}

	m.loading = false
	m.err = errors.Join(errs...)
	if m.err != nil {
		return
	}
	m.pkgs, m.versions = merge(lists)
	m.attrIndex = make(map[string]int, len(m.pkgs))
	for i, p := range m.pkgs {
		m.attrIndex[p.Attr] = i
	}
	m.index = search.NewIndex(m.pkgs)
	m.selected = nil
	m.loadInstalled()
	m.syncSystem(m.sys.target, false, false)
	m.refilter()
	m.status = fmt.Sprintf("%d packages · %s", len(m.pkgs), strings.Join(infos, ", "))
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
	if key == "ctrl+r" && (m.err != nil || m.popup == popupNone && m.focus != focusSearch && m.tab == tabPackages) {
		m = m.startLoading(true)
		m.err = nil
		return m, m.loadAll(true)
	}
	if m.err != nil {
		if key == "q" {
			return m, tea.Quit
		}
		return m, nil
	}
	if m.popup == popupValue {
		return m.handleEditorKey(msg)
	}
	if m.popup != popupNone {
		return m.handlePopupKey(key)
	}
	// Number keys jump to a panel, like in lazygit.
	if m.focus != focusSearch {
		switch key {
		case "1":
			m.focus = focusPackages
			return m, nil
		case "2":
			m.focus = focusSelected
			return m, nil
		case "3":
			m.focus = focusInstalled
			return m, nil
		}
	}
	if m.tab == tabOptions {
		return m.handleOptionsKey(msg)
	}
	if m.focus == focusInstalled {
		return m.handleInstalledKey(key)
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
		case "t":
			return m, m.switchTab()
		case "tab":
			m.focus = focusInstalled
		case "shift+tab", "h", "left":
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
		case "c":
			if len(m.selected) > 0 {
				m.cycleSelectionChannel(m.selCursor)
			}
		case "/":
			return m.focusSearch()
		case "w":
			m.openGenerate()
		case "o":
			m.openOptions()
		case "a":
			return m, m.openSystem()
		case "enter":
			return m, m.enterShell()
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
	case "t":
		return m, m.switchTab()
	case "tab", "l", "right":
		m.focus = focusSelected
	case "shift+tab":
		m.focus = focusInstalled
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
	case "c":
		m.channel = (m.channel + 1) % len(m.channels)
		m.status = "new selections come from " + m.channels[m.channel].Name
	case "w":
		m.openGenerate()
	case "o":
		m.openOptions()
	case "a":
		return m, m.openSystem()
	case "enter":
		return m, m.enterShell()
	}
	return m, nil
}

func (m Model) handlePopupKey(key string) (tea.Model, tea.Cmd) {
	if m.popup == popupSystem {
		return m.handleSystemKey(key)
	}
	if m.popup == popupOptions {
		n := len(generate.AllOptions)
		switch key {
		case "esc", "q", "o", "enter":
			m.popup = m.optReturn
		case "j", "down", "tab":
			m.optCursor = (m.optCursor + 1) % n
		case "k", "up", "shift+tab":
			m.optCursor = (m.optCursor + n - 1) % n
		case " ", "x":
			m.opts = m.opts.Toggle(generate.AllOptions[m.optCursor])
		case "1", "2", "3", "4":
			if i := int(key[0] - '1'); i < n {
				m.optCursor = i
				m.opts = m.opts.Toggle(generate.AllOptions[i])
			}
		}
		return m, nil
	}
	if m.popup == popupConfirm {
		switch key {
		case "y", "Y", "enter":
			m.write(true)
		case "n", "N", "esc", "q":
			m.popup = popupGenerate
		}
		return m, nil
	}

	switch key {
	case "esc", "q":
		m.popup = popupNone
	case "j", "down", "tab":
		m.genCursor = (m.genCursor + 1) % len(generate.Formats)
	case "k", "up", "shift+tab":
		m.genCursor = (m.genCursor + len(generate.Formats) - 1) % len(generate.Formats)
	case "e":
		m.envrc = !m.envrc
	case "o":
		m.openOptions()
	case "s":
		m.genCursor = int(generate.ShellNix)
		m.write(false)
	case "f":
		m.genCursor = int(generate.Flake)
		m.write(false)
	case "enter":
		m.write(false)
	}
	return m, nil
}

// openOptions shows the options popup on top of the current one.
func (m *Model) openOptions() {
	m.optReturn = m.popup
	m.popup = popupOptions
}

func (m *Model) openGenerate() {
	if len(m.selected) == 0 {
		m.status = "select packages with space first"
		return
	}
	m.popup = popupGenerate
}

// write generates the chosen file in m.dir, asking before replacing one.
func (m *Model) write(overwrite bool) {
	f := generate.Formats[m.genCursor]
	path, err := generate.Write(m.dir, f, m.channels, m.selectedPackages(), m.opts, overwrite)
	switch {
	case errors.Is(err, generate.ErrExists):
		m.popup = popupConfirm
	case err != nil:
		m.popup = popupNone
		m.status = "error: " + err.Error()
	default:
		m.popup = popupNone
		m.status = fmt.Sprintf("wrote ./%s · %s", filepath.Base(path), f.Hint())
		if m.envrc {
			m.writeEnvrc(f)
		}
	}
}

func (m *Model) writeEnvrc(f generate.Format) {
	_, changed, err := generate.WriteEnvrc(m.dir, f)
	if err != nil {
		m.status = fmt.Sprintf("wrote ./%s, but .envrc failed: %v", f.Filename(), err)
		return
	}
	envrc := "+ .envrc"
	if !changed {
		envrc = "(.envrc already set)"
	}
	next := "run `direnv allow`"
	if _, err := exec.LookPath("direnv"); err != nil {
		next = "install direnv, then run `direnv allow`"
	}
	if f == generate.Flake {
		next += " (git add flake.nix first inside a git repo)"
	}
	m.status = fmt.Sprintf("wrote ./%s %s · %s", f.Filename(), envrc, next)
}

func (m Model) selectedPackages() []generate.Package {
	pkgs := make([]generate.Package, len(m.selected))
	for i, s := range m.selected {
		pkgs[i] = generate.Package{Attr: m.pkgs[s.pkg].Attr, Channel: s.channel}
	}
	return pkgs
}

// label names a package with its channel, e.g. "jq (stable)".
func (m Model) label(s selection) string {
	return fmt.Sprintf("%s (%s)", m.pkgs[s.pkg].Attr, m.channels[s.channel].Name)
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

// channelFor picks the channel to take package p from: the default channel
// when it has the package, otherwise the first channel that does.
func (m Model) channelFor(p int) int {
	if m.versions[p][m.channel] != "" {
		return m.channel
	}
	for c, v := range m.versions[p] {
		if v != "" {
			return c
		}
	}
	return m.channel
}

// selectionOf returns the position of p in m.selected, or -1.
func (m Model) selectionOf(p int) int {
	for i, s := range m.selected {
		if s.pkg == p {
			return i
		}
	}
	return -1
}

func (m *Model) toggle(p int) {
	if i := m.selectionOf(p); i >= 0 {
		m.selected = append(m.selected[:i], m.selected[i+1:]...)
		m.selCursor = min(m.selCursor, max(0, len(m.selected)-1))
		return
	}
	c := m.channelFor(p)
	if c != m.channel {
		m.status = fmt.Sprintf("%s is not in %s, added from %s", m.pkgs[p].Attr, m.channels[m.channel].Name, m.channels[c].Name)
	}
	m.selected = append(m.selected, selection{pkg: p, channel: c})
}

// cycleSelectionChannel moves selection i to the next channel that has it.
func (m *Model) cycleSelectionChannel(i int) {
	s := &m.selected[i]
	for step := 1; step < len(m.channels); step++ {
		c := (s.channel + step) % len(m.channels)
		if m.versions[s.pkg][c] != "" {
			s.channel = c
			m.status = m.label(*s)
			return
		}
	}
	m.status = fmt.Sprintf("%s is only in %s", m.pkgs[s.pkg].Attr, m.channels[s.channel].Name)
}
