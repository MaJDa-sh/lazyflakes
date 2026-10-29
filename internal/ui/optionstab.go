package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/nix"
	"github.com/MaJDa-sh/lazyflakes/internal/options"
	"github.com/MaJDa-sh/lazyflakes/internal/search"
)

// The Options tab browses the options of the current target (NixOS,
// home-manager or nix-darwin) the way the Packages tab browses packages:
// search, details, a pending list and the installed list. It reuses the same
// three panels and the same review popup; options chosen here end up as
// settings in the target's managed module.

type tab int

const (
	tabPackages tab = iota
	tabOptions
)

// optionData is the option list of one target.
type optionData struct {
	opts    []options.Option // the ones a user can set
	hidden  int              // read-only options and ones that need a name first
	index   *search.Index
	byName  map[string]int
	byPath  map[string]int // by assignable path, to find the option of a setting
	err     error
	loading bool
}

func (d *optionData) fill(all []options.Option) {
	for _, o := range all {
		if o.Settable() {
			d.opts = append(d.opts, o)
		} else {
			d.hidden++
		}
	}
	pkgs := make([]nix.Package, len(d.opts))
	d.byName = make(map[string]int, len(d.opts))
	d.byPath = make(map[string]int, len(d.opts))
	for i, o := range d.opts {
		leaf := o.Name[strings.LastIndexByte(o.Name, '.')+1:]
		pkgs[i] = nix.Package{Attr: o.Name, Pname: leaf, Description: o.Description}
		d.byName[o.Name] = i
		d.byPath[o.Path()] = i
	}
	d.index = search.NewIndex(pkgs)
}

type optionsLoadedMsg struct {
	target string
	data   *optionData
}

// pendingSetting is an option chosen in the Options tab and not applied yet.
type pendingSetting struct {
	path, value string
}

// valueEditor is the popup for entering the value of one option.
type valueEditor struct {
	opt     options.Option
	input   textinput.Model
	raw     bool     // the input is a Nix expression, taken as typed
	choices []string // booleans and choices are picked, not typed
	choice  int
	err     string
}

// curTarget is the target whose options and settings are shown.
func (m Model) curTarget() *config.Target {
	if m.sys.err != nil || len(m.sys.targets) == 0 || m.sys.target >= len(m.sys.targets) {
		return nil
	}
	return m.sys.targets[m.sys.target]
}

func (m Model) curData() *optionData {
	if t := m.curTarget(); t != nil {
		return m.optData[t.Name]
	}
	return nil
}

// optionsBusy reports whether options are being built, which keeps the
// spinner going.
func (m Model) optionsBusy() bool {
	for _, d := range m.optData {
		if d.loading {
			return true
		}
	}
	return false
}

// switchTab toggles between the Packages and Options tabs. Each keeps its
// own search text.
func (m *Model) switchTab() tea.Cmd {
	m.queries[m.tab] = m.input.Value()
	m.tab = tabOptions - m.tab
	m.input.SetValue(m.queries[m.tab])
	m.focus = focusPackages
	if m.tab == tabPackages {
		m.input.Placeholder = "type / to search packages…"
		m.refilter()
		return nil
	}
	m.input.Placeholder = "type / to search options…"
	return m.ensureOptions()
}

// ensureOptions starts building the current target's options unless they are
// loaded or loading already.
func (m *Model) ensureOptions() tea.Cmd {
	t := m.curTarget()
	if t == nil {
		return nil
	}
	if m.optData == nil {
		m.optData = map[string]*optionData{}
	}
	if d := m.optData[t.Name]; d != nil && (d.loading || d.err == nil) {
		m.refilterOptions()
		return nil
	}
	m.optData[t.Name] = &optionData{loading: true}
	m.olFiltered, m.olCursor, m.olOffset = nil, 0, 0
	return tea.Batch(m.spinner.Tick, loadOptions(t, m.fallbackNixpkgs()))
}

// fallbackNixpkgs is the nixpkgs to take options from when a target's own
// cannot be found: the stable channel.
func (m Model) fallbackNixpkgs() string {
	for _, c := range m.channels {
		if c.Name == "stable" {
			return c.Ref
		}
	}
	return m.channels[len(m.channels)-1].Ref
}

func loadOptions(t *config.Target, fallback string) tea.Cmd {
	target := *t // the command runs on another goroutine
	return func() tea.Msg {
		data := &optionData{}
		src, err := options.SourceFor(&target, options.DetectEnv(fallback))
		if err != nil {
			data.err = err
			return optionsLoadedMsg{target: target.Name, data: data}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		all, err := src.Load(ctx)
		if err != nil {
			data.err = err
			return optionsLoadedMsg{target: target.Name, data: data}
		}
		data.fill(all)
		return optionsLoadedMsg{target: target.Name, data: data}
	}
}

func (m *Model) optionsLoaded(msg optionsLoadedMsg) {
	if m.optData == nil {
		m.optData = map[string]*optionData{}
	}
	m.optData[msg.target] = msg.data
	if t := m.curTarget(); t != nil && t.Name == msg.target && m.tab == tabOptions {
		m.refilterOptions()
	}
}

func (m *Model) refilterOptions() {
	d := m.curData()
	if d == nil || d.index == nil {
		m.olFiltered = nil
		return
	}
	m.olFiltered = d.index.Filter(m.input.Value())
	m.olCursor, m.olOffset = 0, 0
}

func (m *Model) moveOption(delta int) {
	m.olCursor = max(0, min(m.olCursor+delta, len(m.olFiltered)-1))
	if h := m.listHeight(); h > 0 {
		if m.olCursor < m.olOffset {
			m.olOffset = m.olCursor
		}
		if m.olCursor >= m.olOffset+h {
			m.olOffset = m.olCursor - h + 1
		}
	}
}

func (m Model) currentOption() (options.Option, bool) {
	d := m.curData()
	if d == nil || d.index == nil || m.olCursor < 0 || m.olCursor >= len(m.olFiltered) {
		return options.Option{}, false
	}
	return d.opts[m.olFiltered[m.olCursor]], true
}

// settingOf finds the item of option o among the target's settings: set in
// the module already, and the value queued for it.
func (m Model) settingOf(o options.Option) (set *sysItem, queued *pendingSetting) {
	path := o.Path()
	for i := range m.sys.items {
		if it := &m.sys.items[i]; it.setting && it.orig != "" && it.entry.Attr == path {
			set = it
		}
	}
	for i := range m.pendingSet {
		if m.pendingSet[i].path == path {
			queued = &m.pendingSet[i]
		}
	}
	return
}

func (m Model) optionMark(o options.Option) string {
	set, queued := m.settingOf(o)
	switch {
	case queued != nil:
		return markStyle.Render("● ")
	case set != nil:
		return markStyle.Render("○ ")
	}
	return "  "
}

// ---- keys ----

func (m Model) handleOptionsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.focus == focusSearch {
		switch key {
		case "esc":
			m.input.SetValue("")
			m.refilterOptions()
			return m.blurSearch(), nil
		case "enter":
			return m.blurSearch(), nil
		case "up", "ctrl+k", "ctrl+p":
			m.moveOption(-1)
			return m, nil
		case "down", "ctrl+j", "ctrl+n":
			m.moveOption(1)
			return m, nil
		}
		before := m.input.Value()
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if m.input.Value() != before {
			m.refilterOptions()
		}
		return m, cmd
	}

	// Keys that work in every panel of the tab.
	switch key {
	case "q":
		return m, tea.Quit
	case "t":
		return m, m.switchTab()
	case "a":
		return m, m.openSystem()
	case "o":
		m.openOptions()
		return m, nil
	case "/":
		return m.focusSearch()
	case "ctrl+r":
		if t := m.curTarget(); t != nil {
			delete(m.optData, t.Name)
			return m, m.ensureOptions()
		}
		return m, nil
	case "[", "]":
		if len(m.sys.targets) > 1 {
			step := 1
			if key == "[" {
				step = len(m.sys.targets) - 1
			}
			m.olCursor = 0
			cmd := m.syncSystem((m.sys.target+step)%len(m.sys.targets), false, false)
			return m, tea.Batch(cmd, m.ensureOptions())
		}
		return m, nil
	}

	switch m.focus {
	case focusSelected:
		return m.handlePendingKey(key)
	case focusInstalled:
		return m.handleSetKey(key)
	}
	return m.handleOptionListKey(key)
}

func (m Model) handleOptionListKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		if m.input.Value() != "" {
			m.input.SetValue("")
			m.refilterOptions()
		}
	case "tab", "l", "right":
		m.focus = focusSelected
	case "shift+tab":
		m.focus = focusInstalled
	case "j", "down":
		m.moveOption(1)
	case "k", "up":
		m.moveOption(-1)
	case "ctrl+d", "pgdown":
		m.moveOption(m.listHeight() / 2)
	case "ctrl+u", "pgup":
		m.moveOption(-m.listHeight() / 2)
	case "g", "home":
		m.moveOption(-len(m.olFiltered))
	case "G", "end":
		m.moveOption(len(m.olFiltered))
	case "enter", " ":
		if o, ok := m.currentOption(); ok {
			current := ""
			if _, q := m.settingOf(o); q != nil {
				current = q.value
			} else if set, _ := m.settingOf(o); set != nil {
				current = set.value
			}
			m.openEditor(o, current)
		}
	}
	return m, nil
}

func (m Model) handlePendingKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "tab":
		m.focus = focusInstalled
	case "shift+tab", "h", "left":
		m.focus = focusPackages
	case "j", "down":
		m.pendCursor = min(m.pendCursor+1, max(0, len(m.pendingSet)-1))
	case "k", "up":
		m.pendCursor = max(m.pendCursor-1, 0)
	case "d", "x":
		if m.pendCursor < len(m.pendingSet) {
			m.status = m.pendingSet[m.pendCursor].path + " will not be set"
			m.pendingSet = append(m.pendingSet[:m.pendCursor], m.pendingSet[m.pendCursor+1:]...)
			m.pendCursor = min(m.pendCursor, max(0, len(m.pendingSet)-1))
		}
	case "e", "enter", " ":
		if m.pendCursor < len(m.pendingSet) {
			m.editSetting(m.pendingSet[m.pendCursor].path, m.pendingSet[m.pendCursor].value)
		}
	}
	return m, nil
}

func (m Model) handleSetKey(key string) (tea.Model, tea.Cmd) {
	rows := m.sys.installedRows(true)
	s := &m.sys
	switch key {
	case "tab":
		m.focus = focusPackages
	case "shift+tab", "h", "left":
		m.focus = focusSelected
	case "j", "down":
		m.setCursor = min(m.setCursor+1, max(0, len(rows)-1))
	case "k", "up":
		m.setCursor = max(m.setCursor-1, 0)
	case "d", "x", " ":
		if m.setCursor < len(rows) {
			it := &s.items[rows[m.setCursor]]
			it.removed = !it.removed
			if it.removed {
				m.status = it.entry.Attr + " will be unset when you apply · enter to review and apply"
			} else {
				m.status = it.entry.Attr + " stays set"
			}
		}
	case "e":
		if m.setCursor < len(rows) {
			it := s.items[rows[m.setCursor]]
			m.editSetting(it.entry.Attr, it.value)
		}
	case "u":
		// Undo the settings only: unmark removals, restore changed values and
		// drop the ones that were queued and not applied.
		kept := s.items[:0]
		for _, it := range s.items {
			if it.setting {
				if it.orig == "" {
					continue
				}
				it.removed, it.value = false, it.origValue
			}
			kept = append(kept, it)
		}
		s.items = kept
		s.cursor = min(s.cursor, max(0, len(s.items)-1))
		m.setCursor = min(m.setCursor, max(0, len(s.installedRows(true))-1))
		m.pendingSet = nil
		m.status = "pending option changes undone"
	case "enter":
		return m, m.openSystem()
	}
	return m, nil
}

// editSetting opens the editor for a setting by its path.
func (m *Model) editSetting(path, value string) {
	d := m.curData()
	if d == nil {
		return
	}
	i, ok := d.byPath[path]
	if !ok {
		m.status = path + " is not in the option list, so it cannot be edited here"
		return
	}
	m.openEditor(d.opts[i], value)
}

// ---- the value editor ----

func kindHint(o options.Option) string {
	switch o.Kind {
	case options.Bool:
		return "true or false"
	case options.Enum:
		return "one of: " + strings.Join(o.Choices, ", ")
	case options.Int:
		return "a whole number"
	case options.String:
		return "text"
	case options.Path:
		return "an absolute path, e.g. /var/lib/foo"
	case options.StringList:
		return "values separated by commas"
	case options.Package:
		return "a package name, e.g. htop"
	case options.PackageList:
		return "package names separated by commas"
	}
	return "a Nix expression"
}

// openEditor shows the value popup for o. current is the Nix expression it
// already has (set or queued), or "".
func (m *Model) openEditor(o options.Option, current string) {
	e := &valueEditor{opt: o}
	in := textinput.New()
	in.Prompt = " "
	in.Width = 60
	in.Placeholder = kindHint(o)

	switch o.Kind {
	case options.Bool:
		e.choices = []string{"true", "false"}
	case options.Enum:
		e.choices = append([]string(nil), o.Choices...)
	}
	if e.choices != nil && o.Nullable {
		e.choices = append(e.choices, "null")
	}

	if e.choices != nil {
		start := current
		if start == "" {
			start = o.Default
		}
		e.choice = -1
		for i, c := range e.choices {
			if c == strings.Trim(start, `"`) {
				e.choice = i
			}
		}
		if e.choice < 0 {
			e.choice = 0
			if current != "" {
				e.raw = true // an unusual value: edit it as Nix
				in.SetValue(current)
			}
		}
	} else if current != "" {
		e.raw = true
		in.SetValue(current)
	}
	if o.Kind == options.Other {
		e.raw = true
	}
	in.CursorEnd()
	in.Focus()
	e.input = in
	m.edit = e
	m.popup = popupValue
}

func (m Model) handleEditorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	e := m.edit
	switch key := msg.String(); key {
	case "esc":
		m.popup, m.edit = popupNone, nil
		return m, nil
	case "enter":
		m.confirmEditor()
		return m, nil
	case "ctrl+r":
		if e.opt.Kind != options.Other {
			if !e.raw && e.choices != nil {
				// Carry the picked choice over as an expression to edit.
				if v, err := e.opt.Format(e.choices[e.choice], false); err == nil {
					e.input.SetValue(v)
					e.input.CursorEnd()
				}
			}
			e.raw, e.err = !e.raw, ""
		}
		return m, nil
	case "j", "down", "tab", "k", "up", "shift+tab", " ":
		if e.choices != nil && !e.raw {
			n := len(e.choices)
			if key == "k" || key == "up" || key == "shift+tab" {
				e.choice = (e.choice + n - 1) % n
			} else {
				e.choice = (e.choice + 1) % n
			}
			return m, nil
		}
	}
	if e.choices != nil && !e.raw {
		return m, nil // picking, not typing
	}
	var cmd tea.Cmd
	e.input, cmd = e.input.Update(msg)
	e.err = ""
	return m, cmd
}

// confirmEditor checks the value and queues it.
func (m *Model) confirmEditor() {
	e := m.edit
	var value string
	var err error
	if e.choices != nil && !e.raw {
		value, err = e.opt.Format(e.choices[e.choice], false)
	} else {
		value, err = e.opt.Format(e.input.Value(), e.raw)
		if err == nil && (e.raw || e.opt.Kind == options.Other) {
			err = options.CheckSyntax(value)
		}
	}
	if err != nil {
		e.err = err.Error()
		return
	}
	m.queueSetting(e.opt.Path(), value)
	m.popup, m.edit = popupNone, nil
}

// queueSetting adds or replaces a pending setting.
func (m *Model) queueSetting(path, value string) {
	for i := range m.pendingSet {
		if m.pendingSet[i].path == path {
			m.pendingSet[i].value = value
			m.status = fmt.Sprintf("%s = %s queued · a to review and apply", path, value)
			return
		}
	}
	m.pendingSet = append(m.pendingSet, pendingSetting{path: path, value: value})
	m.status = fmt.Sprintf("%s = %s queued · a to review and apply", path, value)
}

// ---- views ----

func (m Model) searchTitle() string {
	if m.tab == tabOptions {
		return "Search  Packages [Options]  t switches"
	}
	return "Search  [Packages] Options  t switches"
}

func shortKind(k options.Kind) string {
	return [...]string{"expr", "bool", "choice", "int", "text", "path", "list", "package", "packages"}[k]
}

// optionsMessage is a panel body for when there is no list to show.
func (m Model) optionsMessage() []string {
	t := m.curTarget()
	d := m.curData()
	switch {
	case t == nil:
		return []string{"", dimStyle.Render("  no target is configured yet"), "",
			dimStyle.Render("  press a, then enter, to run lazyflakes setup")}
	case d == nil || d.loading:
		return []string{"", "  " + m.spinner.View() + " building the options of " + t.Name + "…", "",
			dimStyle.Render("  the first time this downloads a few MB; after that it is quick")}
	case d.err != nil:
		lines := []string{"", errStyle.Render("  could not load the options")}
		for _, l := range wrap(d.err.Error(), 70) {
			lines = append(lines, "  "+l)
		}
		return append(lines, "", dimStyle.Render("  ctrl+r retries"))
	}
	return nil
}

func (m Model) optionsListView(w, h int) string {
	inner := w - 2
	active := m.focus == focusPackages || m.focus == focusSearch
	if msg := m.optionsMessage(); msg != nil {
		return panel("[1] Options", "", msg, w, h, active)
	}
	d := m.curData()

	const kindW, defW = 8, 14
	attrW := max(10, inner-2-kindW-defW-2)
	header := "  " + padTo("option", attrW) + " " + padTo("type", kindW) + " " + "default"
	lines := []string{dimStyle.Render(ansi.Truncate(header, inner, ""))}

	for i := m.olOffset; i < len(m.olFiltered) && len(lines) < h-2; i++ {
		o := d.opts[m.olFiltered[i]]
		text := padTo(ansi.Truncate(o.Name, attrW, "…"), attrW) + " " + padTo(shortKind(o.Kind), kindW) + " " +
			ansi.Truncate(strings.ReplaceAll(o.Default, "\n", " "), defW, "…")
		if i == m.olCursor {
			style := cursorDimStyle
			if active {
				style = cursorStyle
			}
			lines = append(lines, m.optionMark(o)+style.Render(padTo(text, inner-2)))
			continue
		}
		row := padTo(ansi.Truncate(o.Name, attrW, "…"), attrW)
		lines = append(lines, m.optionMark(o)+attrStyle.Render(row)+" "+dimStyle.Render(padTo(shortKind(o.Kind), kindW)+" "+
			ansi.Truncate(strings.ReplaceAll(o.Default, "\n", " "), defW, "…")))
	}
	if len(m.olFiltered) == 0 {
		lines = append(lines, dimStyle.Render("  no options match"))
	}

	title := fmt.Sprintf("[1] Options %d", len(d.opts))
	if m.input.Value() != "" {
		title = fmt.Sprintf("[1] Options %d of %d", len(m.olFiltered), len(d.opts))
	}
	footer := ""
	if len(m.olFiltered) > 0 {
		footer = fmt.Sprintf("%d of %d", m.olCursor+1, len(m.olFiltered))
	}
	if d.hidden > 0 {
		footer += fmt.Sprintf(" · %d read-only or named options hidden", d.hidden)
	}
	return panel(title, strings.TrimPrefix(footer, " · "), lines, w, h, active)
}

func (m Model) optionDetailsView(w, h int) string {
	o, ok := m.currentOption()
	if !ok {
		return panel("Details", "", nil, w, h, false)
	}
	inner := w - 4
	field := func(label, value string) string {
		if value == "" {
			value = dimStyle.Render("—")
		}
		return labelStyle.Render(fmt.Sprintf("%-9s", label)) + value
	}
	lines := []string{
		field("option", attrStyle.Render(o.Name)),
		field("type", o.Type),
		field("enter as", dimStyle.Render(kindHint(o))),
		field("default", strings.ReplaceAll(o.Default, "\n", " ")),
	}
	if o.Example != "" {
		lines = append(lines, field("example", strings.ReplaceAll(o.Example, "\n", " ")))
	}
	if o.Declared != "" {
		lines = append(lines, field("from", dimStyle.Render(o.Declared)))
	}
	set, queued := m.settingOf(o)
	switch {
	case queued != nil:
		lines = append(lines, field("queued", markStyle.Render(queued.value)))
	case set != nil:
		lines = append(lines, field("set", markStyle.Render(set.value)+dimStyle.Render("  (in "+m.curTarget().Name+")")))
	}
	lines = append(lines, "", labelStyle.Render("description"))
	desc := o.Description
	if desc == "" {
		desc = dimStyle.Render("no description")
	}
	for _, para := range strings.Split(desc, "\n\n") {
		lines = append(lines, wrap(strings.Join(strings.Fields(para), " "), inner)...)
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = " " + lines[i]
	}
	return panel("Details", "", lines, w, h, false)
}

func (m Model) pendingView(w, h int) string {
	inner := w - 2
	active := m.focus == focusSelected
	var lines []string
	start := max(0, m.pendCursor-(h-2)+1)
	for i := start; i < len(m.pendingSet); i++ {
		text := ansi.Truncate(m.pendingSet[i].path+" = "+m.pendingSet[i].value, inner-3, "…")
		if active && i == m.pendCursor {
			lines = append(lines, " "+cursorStyle.Render(padTo(text, inner-3))+" ")
		} else {
			lines = append(lines, " "+markStyle.Render(text))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render(" press enter on an option to set a value"))
	}
	footer := ""
	if active && len(m.pendingSet) > 0 {
		footer = "d remove · e edit"
	}
	return panel(fmt.Sprintf("[2] Pending %d", len(m.pendingSet)), footer, lines, w, h, active)
}

func (m Model) setView(w, h int) string {
	inner := w - 2
	s := m.sys
	active := m.focus == focusInstalled
	title := "[3] Set"
	var lines []string
	switch {
	case m.curTarget() == nil:
		lines = append(lines, dimStyle.Render(" not set up yet: press a to install into your system"))
	default:
		rows := s.installedRows(true)
		title = fmt.Sprintf("[3] Set %d", len(rows))
		if len(s.targets) > 1 {
			title += " · " + s.targets[s.target].Name + " ([ ] switch)"
		}
		if len(rows) == 0 {
			lines = append(lines, dimStyle.Render(" no options set through lazyflakes yet"))
		}
		start := max(0, m.setCursor-(h-2)+1)
		for k := start; k < len(rows); k++ {
			it := s.items[rows[k]]
			mark, text := "  ", it.entry.Attr+" = "+it.value
			switch {
			case it.removed:
				mark = "- "
				text = it.entry.Attr + " (unset on apply)"
			case it.value != it.origValue:
				mark = "~ "
			}
			text = mark + ansi.Truncate(text, inner-5, "…")
			switch {
			case active && k == m.setCursor && it.removed:
				lines = append(lines, " "+cursorRemoveStyle.Render(padTo(text, inner-3))+" ")
			case active && k == m.setCursor:
				lines = append(lines, " "+cursorStyle.Render(padTo(text, inner-3))+" ")
			case it.removed:
				lines = append(lines, " "+errStyle.Render(text))
			case mark != "  ":
				lines = append(lines, " "+labelStyle.Render(text))
			default:
				lines = append(lines, " "+text)
			}
		}
	}
	footer := "3 to focus"
	if active {
		footer = "d unset · e edit · enter apply"
	}
	if p := s.pending(true); p > 0 {
		footer = fmt.Sprintf("%d pending · enter review & apply · u undo", p)
	}
	return panel(title, footer, lines, w, h, active)
}

func (m Model) editorView() string {
	e := m.edit
	if e == nil {
		return ""
	}
	w := min(m.width-4, 84)
	inner := w - 4
	o := e.opt

	var lines []string
	add := func(l string) { lines = append(lines, " "+l) }
	field := func(label, value string) {
		add(labelStyle.Render(fmt.Sprintf("%-8s", label)) + value)
	}
	oneLine := func(s string) string { return ansi.Truncate(strings.ReplaceAll(s, "\n", " "), inner-8, "…") }
	field("type", oneLine(o.Type))
	if o.Default != "" {
		field("default", oneLine(o.Default))
	}
	if o.Example != "" {
		field("example", oneLine(o.Example))
	}
	add("")

	if e.choices != nil && !e.raw {
		for i, c := range e.choices {
			if i == e.choice {
				lines = append(lines, cursorStyle.Render(padTo(" (•) "+c, inner+1)))
			} else {
				add("( ) " + c)
			}
		}
	} else {
		mode := "value"
		if e.raw {
			mode = "Nix expression"
		}
		add(labelStyle.Render("enter") + " " + dimStyle.Render(mode))
		lines = append(lines, e.input.View())
	}
	if e.err != "" {
		add("")
		add(errStyle.Render(e.err))
	}

	if o.Description != "" {
		add("")
		desc := strings.Join(strings.Fields(strings.SplitN(o.Description, "\n\n", 2)[0]), " ")
		wrapped := wrap(desc, inner)
		if len(wrapped) > 5 {
			wrapped = append(wrapped[:4], "…")
		}
		for _, l := range wrapped {
			add(dimStyle.Render(l))
		}
	}
	footer := "enter confirm · esc cancel"
	if o.Kind != options.Other {
		footer += " · ctrl+r typed/raw"
	}
	return panel("Set "+o.Name, footer, lines, w, min(m.height-2, len(lines)+2), true)
}

func (m Model) optionsFooterKeys() [][2]string {
	switch m.focus {
	case focusSearch:
		return [][2]string{{"enter", "done"}, {"esc", "clear"}, {"↑/↓", "move"}}
	case focusSelected:
		return [][2]string{{"j/k", "move"}, {"d", "remove"}, {"e", "edit"}, {"a", "review"}, {"t", "packages"}, {"1-3", "panels"}, {"q", "quit"}}
	case focusInstalled:
		return [][2]string{{"j/k", "move"}, {"d", "unset"}, {"e", "edit"}, {"u", "undo"}, {"enter", "review & apply"}, {"t", "packages"}, {"1-3", "panels"}, {"q", "quit"}}
	}
	return [][2]string{{"/", "search"}, {"j/k", "move"}, {"enter", "set a value"}, {"a", "review & apply"}, {"[ ]", "target"}, {"t", "packages"}, {"1-3", "panels"}, {"ctrl+r", "reload"}, {"q", "quit"}}
}
