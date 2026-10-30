package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaJDa-sh/lazyflakes/internal/generate"
	"github.com/MaJDa-sh/lazyflakes/internal/managed"
	"github.com/MaJDa-sh/lazyflakes/internal/options"
)

func managedEntry(attr, channel string) managed.Entry {
	return managed.Entry{Attr: attr, Channel: channel}
}

// keyMsg builds the message for a key such as "j", "enter" or "ctrl+r".
func keyMsg(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "ctrl+r":
		return tea.KeyMsg{Type: tea.KeyCtrlR}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune(" ")}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

// press sends keys to the model one after the other.
func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		next, _ := m.Update(keyMsg(k))
		m = next.(Model)
	}
	return m
}

// typeText sends each rune of s as a key.
func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	return m
}

// optionsModel is a model on the Options tab with the fixture options loaded
// for a NixOS module target whose module holds settings.
func optionsModel(t *testing.T, settings string) Model {
	t.Helper()
	dir := t.TempDir()
	module := filepath.Join(dir, "lazyflakes.nix")
	os.WriteFile(module, []byte("{ pkgs, ... }: {\n"+settings+"  environment.systemPackages = [ ];\n}\n"), 0o644)
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("[targets.system]\nkind = \"nixos\"\nfile = '"+module+"'\nrebuild = \"true\"\nsets = { stable = \"pkgs\" }\n"), 0o644)

	f, err := os.Open("../options/testdata/options.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	all, err := options.Parse(f)
	if err != nil {
		t.Fatal(err)
	}

	m := New(generate.DefaultChannels(time.Now()), 0, false, dir, generate.Flake, false, 0, cfg)
	m.width, m.height = 150, 40
	m.loading = false
	m.syncSystem(0, false, false)
	data := &optionData{}
	data.fill(all)
	m.optData = map[string]*optionData{"system": data}
	m.tab = tabOptions
	m.refilterOptions()
	return m
}

func selectOption(t *testing.T, m Model, name string) Model {
	t.Helper()
	m.input.SetValue(name)
	m.refilterOptions()
	o, ok := m.currentOption()
	if !ok || o.Name != name {
		t.Fatalf("searching %q selected %+v", name, o)
	}
	return m
}

func TestOptionsTabListsOnlySettableOptions(t *testing.T) {
	m := optionsModel(t, "")
	d := m.curData()
	for _, o := range d.opts {
		if !o.Settable() {
			t.Errorf("%s should not be listed", o.Name)
		}
	}
	if d.hidden != 3 { // read-only, needs a name, and the module system's own
		t.Errorf("hidden = %d, want 3", d.hidden)
	}
	if len(m.olFiltered) != len(d.opts) {
		t.Errorf("an empty search shows everything: %d of %d", len(m.olFiltered), len(d.opts))
	}
	m.input.SetValue("openssh")
	m.refilterOptions()
	for _, i := range m.olFiltered {
		if !strings.Contains(d.opts[i].Name, "openssh") && !strings.Contains(strings.ToLower(d.opts[i].Description), "openssh") {
			t.Errorf("%s does not match the search", d.opts[i].Name)
		}
	}
	if len(m.olFiltered) < 4 {
		t.Errorf("expected the openssh options, got %d", len(m.olFiltered))
	}
	if v := m.View(); !strings.Contains(v, "[2] Pending") || !strings.Contains(v, "[3] Set") || !strings.Contains(v, "[Options]") {
		t.Errorf("the options layout is missing its panels:\n%s", v)
	}
}

func TestSetABooleanAndReviewIt(t *testing.T) {
	m := selectOption(t, optionsModel(t, ""), "services.openssh.enable")

	m = press(t, m, "enter")
	if m.popup != popupValue || m.edit == nil || len(m.edit.choices) != 2 {
		t.Fatalf("enter should open the editor with true and false: popup=%v edit=%+v", m.popup, m.edit)
	}
	if got := m.edit.choices[m.edit.choice]; got != "false" {
		t.Errorf("the editor starts on the default, false; got %q", got)
	}
	if v := m.View(); !strings.Contains(v, "Set services.openssh.enable") || !strings.Contains(v, "(•) false") {
		t.Errorf("editor view:\n%s", v)
	}
	m = press(t, m, "j", "enter") // pick true, confirm
	if m.popup != popupNone || len(m.pendingSet) != 1 || m.pendingSet[0].value != "true" || m.pendingSet[0].path != "services.openssh.enable" {
		t.Fatalf("pending = %+v, popup = %v", m.pendingSet, m.popup)
	}
	if !strings.Contains(m.status, "services.openssh.enable = true queued") {
		t.Errorf("status = %q", m.status)
	}
	if mark := m.optionMark(m.curDataOption(t)); !strings.Contains(mark, "●") {
		t.Errorf("a queued option is marked: %q", mark)
	}

	// The review popup lists it and applying runs the plan.
	next, _ := m.handleOptionsKey(keyMsg("a"))
	m = next.(Model)
	if added, removed, moved := m.sys.changes(); added != 1 || removed != 0 || moved != 0 {
		t.Fatalf("changes: +%d -%d ~%d, items %+v", added, removed, moved, m.sys.items)
	}
	if v := m.View(); !strings.Contains(v, "services.openssh.enable = true") || !strings.Contains(v, "set") {
		t.Errorf("review popup:\n%s", v)
	}
	if cmd := m.applySystem(false); cmd == nil || m.popup != popupNone {
		t.Errorf("applying should run a command: cmd=%v popup=%v", cmd != nil, m.popup)
	}
}

// curDataOption is the option under the cursor.
func (m Model) curDataOption(t *testing.T) options.Option {
	t.Helper()
	o, ok := m.currentOption()
	if !ok {
		t.Fatal("no option under the cursor")
	}
	return o
}

func TestEditorValidatesValues(t *testing.T) {
	m := selectOption(t, optionsModel(t, ""), "boot.loader.grub.timeout")
	m = press(t, m, "enter")
	if m.edit.choices != nil {
		t.Fatal("an integer is typed, not picked")
	}

	m = typeText(t, m, "five")
	m = press(t, m, "enter")
	if m.popup != popupValue || m.edit.err == "" || len(m.pendingSet) != 0 {
		t.Fatalf("a bad number must keep the editor open with an error: popup=%v err=%q", m.popup, m.edit.err)
	}
	if v := m.View(); !strings.Contains(v, "a whole number") {
		t.Errorf("the error should be shown:\n%s", v)
	}

	// Typing again clears the error; a number is accepted.
	for range "five" {
		m = press(t, m, "backspace")
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		m = next.(Model)
	}
	m = typeText(t, m, "10")
	if m.edit.err != "" {
		t.Errorf("error should clear while typing: %q", m.edit.err)
	}
	m = press(t, m, "enter")
	if m.popup != popupNone || len(m.pendingSet) != 1 || m.pendingSet[0].value != "10" {
		t.Errorf("pending = %+v popup=%v", m.pendingSet, m.popup)
	}

	// esc cancels without queueing.
	m = selectOption(t, m, "networking.hostName")
	m = press(t, m, "enter")
	m = typeText(t, m, "box")
	m = press(t, m, "esc")
	if m.popup != popupNone || len(m.pendingSet) != 1 {
		t.Errorf("esc must not queue: popup=%v pending=%+v", m.popup, m.pendingSet)
	}
}

func TestEditorTextRawAndExpressions(t *testing.T) {
	m := selectOption(t, optionsModel(t, ""), "networking.hostName")
	m = press(t, m, "enter")
	m = typeText(t, m, `my "box"`)
	m = press(t, m, "enter")
	if len(m.pendingSet) != 1 || m.pendingSet[0].value != `"my \"box\""` {
		t.Fatalf("text is quoted for Nix: %+v", m.pendingSet)
	}

	// Choices can be switched to a raw expression with ctrl+r.
	m = selectOption(t, m, "services.openssh.settings.PermitRootLogin")
	m = press(t, m, "enter")
	if m.edit.choices == nil || m.edit.raw {
		t.Fatalf("a choice is picked: %+v", m.edit)
	}
	next, _ := m.Update(keyMsg("ctrl+r"))
	m = next.(Model)
	if !m.edit.raw {
		t.Error("ctrl+r should switch to raw")
	}
}

// Raw expressions are checked with Nix's parser, so this needs nix-instantiate.
func TestEditorChecksRawExpressions(t *testing.T) {
	if _, err := exec.LookPath("nix-instantiate"); err != nil {
		t.Skip("nix-instantiate is not installed")
	}
	m := optionsModel(t, "")
	// An option of another type takes a raw expression.
	m = selectOption(t, m, "programs.git.config")
	m = press(t, m, "enter")
	if !m.edit.raw {
		t.Fatal("a type that is not simple is entered as Nix")
	}
	m = typeText(t, m, "{ a = ")
	m = press(t, m, "enter")
	if m.popup != popupValue || !strings.HasPrefix(m.edit.err, "not valid Nix") {
		t.Fatalf("broken Nix must be refused: popup=%v err=%q", m.popup, m.edit.err)
	}
	m = typeText(t, m, "1; }")
	m = press(t, m, "enter")
	if m.popup != popupNone || len(m.pendingSet) != 1 || m.pendingSet[0].value != "{ a = 1; }" {
		t.Errorf("pending = %+v popup=%v", m.pendingSet, m.popup)
	}
}

func TestEditAndUnsetWhatIsAlreadySet(t *testing.T) {
	m := optionsModel(t, "  services.openssh.enable = true; # lazyflakes:set services.openssh.enable = true\n"+
		"  networking.hostName = \"old\"; # lazyflakes:set networking.hostName = \"old\"\n")
	if rows := m.sys.installedRows(true); len(rows) != 2 {
		t.Fatalf("settings in the module: %+v", m.sys.items)
	}
	m = selectOption(t, m, "services.openssh.enable")
	if mark := m.optionMark(m.curDataOption(t)); !strings.Contains(mark, "○") {
		t.Errorf("an option that is set is marked: %q", mark)
	}
	if v := m.View(); !strings.Contains(v, "[3] Set 2") || !strings.Contains(v, "services.openssh.enable = true") {
		t.Errorf("the Set panel should list both:\n%s", v)
	}

	// Entering on an option that is set starts from its value.
	m = press(t, m, "enter")
	if m.edit.choices[m.edit.choice] != "true" {
		t.Errorf("the editor should start on the current value: %q", m.edit.choices[m.edit.choice])
	}
	m = press(t, m, "j", "enter") // change to false
	if len(m.pendingSet) != 1 || m.pendingSet[0].value != "false" {
		t.Fatalf("pending = %+v", m.pendingSet)
	}

	// Mark the other for removal in the Set panel.
	m = press(t, m, "3")
	m = press(t, m, "j", "d") // second row: networking.hostName
	if p := m.sys.pending(true); p != 1 {
		t.Fatalf("one setting is marked for removal, pending = %d", p)
	}
	m.openSystem()
	if added, removed, moved := m.sys.changes(); added != 0 || removed != 1 || moved != 1 {
		t.Errorf("changes +%d -%d ~%d: %+v", added, removed, moved, m.sys.items)
	}
	if v := m.View(); !strings.Contains(v, "unset") || !strings.Contains(v, "was true") {
		t.Errorf("review popup:\n%s", v)
	}

	// u undoes everything pending.
	m.popup = popupNone
	m = press(t, m, "3", "u")
	if m.sys.pending(true) != 0 || len(m.pendingSet) != 0 {
		t.Errorf("u should undo: pending=%d queued=%+v", m.sys.pending(true), m.pendingSet)
	}
}

func TestPendingPanelRemovesAndEdits(t *testing.T) {
	m := selectOption(t, optionsModel(t, ""), "services.openssh.enable")
	m = press(t, m, "enter", "j", "enter")
	m = selectOption(t, m, "boot.loader.grub.timeout")
	m = press(t, m, "enter")
	m = typeText(t, m, "7")
	m = press(t, m, "enter")
	if len(m.pendingSet) != 2 {
		t.Fatalf("pending = %+v", m.pendingSet)
	}

	m = press(t, m, "2", "d") // remove the first
	if len(m.pendingSet) != 1 || m.pendingSet[0].path != "boot.loader.grub.timeout" {
		t.Fatalf("after d: %+v", m.pendingSet)
	}
	m = press(t, m, "e") // edit the remaining
	if m.popup != popupValue || m.edit.opt.Name != "boot.loader.grub.timeout" || m.edit.input.Value() != "7" {
		t.Fatalf("e should reopen the editor with the value: popup=%v edit=%+v", m.popup, m.edit)
	}
	m = press(t, m, "esc")
}

func TestTabSwitchKeepsSearchTextAndNeedsATarget(t *testing.T) {
	m := optionsModel(t, "")
	m.input.SetValue("openssh")
	m.tab = tabOptions

	// T goes back a tab, t forward: Packages, Options, Generations.
	next, _ := m.handleOptionsKey(keyMsg("T"))
	m = next.(Model)
	if m.tab != tabPackages || m.input.Value() != "" {
		t.Errorf("packages tab has its own search text: tab=%v input=%q", m.tab, m.input.Value())
	}
	m.input.SetValue("ripgrep")
	next, _ = m.Update(keyMsg("t"))
	m = next.(Model)
	if m.tab != tabOptions || m.input.Value() != "openssh" {
		t.Errorf("the options search text comes back: tab=%v input=%q", m.tab, m.input.Value())
	}
	m = press(t, m, "t")
	if m.tab != tabGenerations {
		t.Errorf("after Options comes Generations: tab=%v", m.tab)
	}
	if m = press(t, m, "t"); m.tab != tabPackages || m.input.Value() != "ripgrep" {
		t.Errorf("and then Packages again: tab=%v input=%q", m.tab, m.input.Value())
	}

	// Without a configured target there is nothing to show options for.
	m = optionsModel(t, "")
	m.configPath = filepath.Join(t.TempDir(), "missing.toml")
	m.syncSystem(0, false, false)
	m.optData = nil
	if cmd := m.ensureOptions(); cmd != nil {
		t.Error("no target, no options to build")
	}
	if v := m.optionsListView(60, 12); !strings.Contains(v, "no target is configured") {
		t.Errorf("view without a target:\n%s", v)
	}
}

func TestOptionsLoadingAndErrors(t *testing.T) {
	m := optionsModel(t, "")
	m.optData = nil
	cmd := m.ensureOptions()
	if cmd == nil || !m.optionsBusy() {
		t.Fatalf("a missing option list is built: cmd=%v busy=%v", cmd != nil, m.optionsBusy())
	}
	if v := m.optionsListView(70, 12); !strings.Contains(v, "building the options of system") {
		t.Errorf("loading view:\n%s", v)
	}
	if cmd := m.ensureOptions(); cmd != nil {
		t.Error("it must not start a second build")
	}

	m.optionsLoaded(optionsLoadedMsg{target: "system", data: &optionData{err: os.ErrNotExist}})
	if m.optionsBusy() {
		t.Error("an error ends loading")
	}
	if v := m.optionsListView(70, 12); !strings.Contains(v, "could not load the options") || !strings.Contains(v, "ctrl+r") {
		t.Errorf("error view:\n%s", v)
	}
	if cmd := m.ensureOptions(); cmd == nil {
		t.Error("after an error, asking again retries")
	}
}

func TestDescribeChanges(t *testing.T) {
	var s sysState
	if got := s.describe(); got != "no changes" {
		t.Errorf("describe() = %q", got)
	}
	s.items = []sysItem{
		{entry: managedEntry("ripgrep", "stable")},                                                             // a package to add
		{entry: managedEntry("htop", "stable"), orig: "stable", removed: true},                                 // a package to remove
		{setting: true, entry: managedEntry("services.openssh.enable", ""), value: "true"},                     // an option to set
		{setting: true, entry: managedEntry("time.timeZone", ""), orig: "set", value: `"A"`, origValue: `"B"`}, // changed
	}
	if got, want := s.describe(), "packages +1 −1, options +1 −0 ~1"; got != want {
		t.Errorf("describe() = %q, want %q", got, want)
	}
}

// An option is chosen for one target: switching to another must not carry
// it along, or a NixOS option would be written into a home-manager module.
func TestQueuedOptionsStayWithTheirTarget(t *testing.T) {
	m := optionsModel(t, "")
	cfg, _ := os.ReadFile(m.configPath)
	home := filepath.Join(t.TempDir(), "home.nix")
	os.WriteFile(m.configPath, []byte("default = \"system\"\n"+string(cfg)+
		"\n[targets.home]\nkind = \"home-manager\"\nfile = '"+home+"'\nsets = { stable = \"pkgs\" }\n"), 0o644)
	m.syncSystem(0, false, false)
	m.queueSetting("services.openssh.enable", "true")

	m = press(t, m, "]")
	if name := m.curTarget().Name; name != "home" {
		t.Fatalf("] should switch to home, got %s", name)
	}
	if len(m.pendingSet) != 0 {
		t.Errorf("home has nothing queued: %+v", m.pendingSet)
	}
	for _, it := range m.sys.items {
		if it.setting {
			t.Errorf("system's option would be applied to home: %+v", it)
		}
	}

	m = press(t, m, "[")
	if len(m.pendingSet) != 1 || m.pendingSet[0].path != "services.openssh.enable" {
		t.Fatalf("system's queue should come back: %+v", m.pendingSet)
	}
	if n := len(m.sys.items); n != 1 || m.sys.items[0].value != "true" {
		t.Errorf("items = %+v", m.sys.items)
	}
}
