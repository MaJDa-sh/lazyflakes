package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MaJDa-sh/lazyflakes/internal/generate"
	"github.com/MaJDa-sh/lazyflakes/internal/nix"
)

func TestInstalledMarks(t *testing.T) {
	dir := t.TempDir()
	module := filepath.Join(dir, "lazyflakes.nix")
	os.WriteFile(module, []byte("{ pkgs, ... }: { environment.systemPackages = [\n"+
		"  pkgs.cowsay # lazyflakes:pkg stable cowsay\n];}\n"), 0o644)
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("[targets.system]\nkind = \"nixos\"\nfile = '"+module+"'\n"), 0o644)

	m := New(generate.DefaultChannels(time.Now()), 0, false, dir, generate.Flake, false, 0, cfg)
	m.pkgs = []nix.Package{{Attr: "cowsay"}, {Attr: "hello"}}
	m.versions = [][]string{{"3.8", "3.8"}, {"2.12", "2.12"}}
	m.loadInstalled()

	if in := m.installed["cowsay"]; len(in) != 1 || in[0].target != "system" || in[0].channel != "stable" {
		t.Fatalf("installed = %+v", m.installed)
	}
	if mark := m.listMark(0); !strings.Contains(mark, "○") {
		t.Errorf("installed package should get a hollow mark, got %q", mark)
	}
	if mark := m.listMark(1); mark != "  " {
		t.Errorf("other packages get no mark, got %q", mark)
	}
	m.selected = []selection{{pkg: 0, channel: 1}}
	if mark := m.listMark(0); !strings.Contains(mark, "●") {
		t.Errorf("a selected package shows the selection mark, got %q", mark)
	}

	// Without a config nothing is marked and nothing fails.
	m.configPath = filepath.Join(dir, "missing.toml")
	m.loadInstalled()
	if len(m.installed) != 0 {
		t.Errorf("installed without config = %+v", m.installed)
	}
}

func TestPendingChangesAreShared(t *testing.T) {
	dir := t.TempDir()
	module := filepath.Join(dir, "lazyflakes.nix")
	os.WriteFile(module, []byte("{ pkgs, ... }: { environment.systemPackages = [\n"+
		"  pkgs.cowsay # lazyflakes:pkg stable cowsay\n"+
		"  pkgs.hello # lazyflakes:pkg stable hello\n];}\n"), 0o644)
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("[targets.system]\nkind = \"nixos\"\nfile = '"+module+"'\nsets = { stable = \"pkgs\", unstable = \"pkgs-unstable\" }\n"), 0o644)

	m := New(generate.DefaultChannels(time.Now()), 0, false, dir, generate.Flake, false, 0, cfg)
	m.pkgs = []nix.Package{{Attr: "cowsay"}, {Attr: "hello"}, {Attr: "jq"}}
	m.versions = [][]string{{"3.8", "3.8"}, {"2.12", "2.12"}, {"1.8", "1.8"}}
	m.attrIndex = map[string]int{"cowsay": 0, "hello": 1, "jq": 2}
	m.syncSystem(0, false, false)

	// Mark cowsay for removal in the Installed panel.
	m.focus = focusInstalled
	next, _ := m.handleInstalledKey("d")
	m = next.(Model)
	if m.sys.pending() != 1 || !strings.Contains(m.status, "cowsay will be removed") {
		t.Fatalf("pending = %d, status = %q", m.sys.pending(), m.status)
	}

	// Opening the review popup keeps the mark and adds the selection.
	m.selected = []selection{{pkg: 2, channel: 1}}
	m.openSystem()
	if added, removed, _ := m.sys.changes(); added != 1 || removed != 1 {
		t.Fatalf("popup changes: +%d −%d, items = %+v", added, removed, m.sys.items)
	}

	// Dropping the new package in the popup also unselects it.
	m.sys.cursor = len(m.sys.items) - 1
	next, _ = m.handleSystemKey("d")
	m = next.(Model)
	if len(m.selected) != 0 || len(m.sys.items) != 2 {
		t.Errorf("selected = %+v, items = %+v", m.selected, m.sys.items)
	}

	// u undoes the pending changes; a fresh sync after applying clears them.
	m.popup = popupNone
	next, _ = m.handleInstalledKey("u")
	m = next.(Model)
	if m.sys.pending() != 0 {
		t.Error("u should undo pending changes")
	}
	m.handleInstalledKey("d")
	m.syncSystem(0, false, false)
	if m.sys.pending() != 0 {
		t.Error("a sync without keepPending must start clean")
	}
}

func TestScriptTargetInThePopup(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "mine.json")
	os.WriteFile(state, []byte(`{"packages":[{"channel":"stable","attr":"cowsay"},{"channel":"unstable","attr":"htop"}]}`), 0o644)
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("[targets.mine]\nkind = \"script\"\nscript = \"true\"\nfile = '"+state+"'\n"), 0o644)

	m := New(generate.DefaultChannels(time.Now()), 0, false, dir, generate.Flake, false, 0, cfg)
	m.pkgs = []nix.Package{{Attr: "cowsay"}, {Attr: "htop"}, {Attr: "jq"}}
	m.versions = [][]string{{"3.8", "3.8"}, {"3.5", "3.5"}, {"1.8", "1.8"}}
	m.attrIndex = map[string]int{"cowsay": 0, "htop": 1, "jq": 2}

	// A script has no module to check, so no Nix evaluation is started.
	if cmd := m.syncSystem(0, false, true); cmd != nil {
		t.Error("script targets must not start an import check")
	}
	s := m.sys
	if s.err != nil || !s.imported || s.checking || len(s.items) != 2 {
		t.Fatalf("sys = %+v", s)
	}
	m.loadInstalled()
	if in := m.installed["htop"]; len(in) != 1 || in[0].channel != "unstable" {
		t.Errorf("installed marks come from the recorded state: %+v", m.installed)
	}

	// Any channel is fine for a script, so c switches without a package set.
	m.sys.cursor = 0
	m.cycleSystemChannel(&m.sys.items[0])
	if got := m.sys.items[0].entry.Channel; got != "unstable" {
		t.Errorf("cowsay switched to %q, want unstable", got)
	}

	// Nothing changed (back to the recorded state) means nothing to run.
	m.sys.items[0].entry.Channel = "stable"
	m.popup = popupSystem
	if cmd := m.applySystem(true); cmd != nil {
		t.Error("applying without changes must not run the script")
	}
	if !strings.Contains(m.status, "nothing to do") || m.popup != popupSystem {
		t.Errorf("status = %q, popup = %v", m.status, m.popup)
	}

	// A selected package is a pending addition that does run the script.
	m.selected = []selection{{pkg: 2, channel: 1}}
	m.syncSystem(0, false, false)
	if added, _, _ := m.sys.changes(); added != 1 {
		t.Fatalf("expected one addition, items = %+v", m.sys.items)
	}
	if cmd := m.applySystem(true); cmd == nil || m.popup != popupNone {
		t.Errorf("applying an addition should run the script: cmd=%v popup=%v", cmd != nil, m.popup)
	}
}
