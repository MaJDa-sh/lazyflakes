package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MaJDa-sh/lazyflakes/internal/generate"
)

// genModel is a model on the Generations tab of a NixOS target whose
// profile has three generations: 1 without a record, 2 with htop, and the
// current 3 with htop and jq.
func genModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	profile := filepath.Join(dir, "profiles", "system")
	os.MkdirAll(filepath.Dir(profile), 0o755)
	records := []string{"", `{"packages":[{"channel":"stable","attr":"htop"}]}`,
		`{"packages":[{"channel":"stable","attr":"htop"},{"channel":"stable","attr":"jq"}]}`}
	for i, rec := range records {
		n := string(rune('1' + i))
		gen := filepath.Join(dir, "store", "gen"+n)
		os.MkdirAll(filepath.Join(gen, "etc", "lazyflakes"), 0o755)
		os.WriteFile(filepath.Join(gen, "nixos-version"), []byte("25.11.1\n"), 0o644)
		if rec != "" {
			os.WriteFile(filepath.Join(gen, "etc", "lazyflakes", "system.json"), []byte(rec), 0o644)
		}
		os.Symlink(gen, profile+"-"+n+"-link")
	}
	os.Symlink("system-3-link", profile)

	module := filepath.Join(dir, "lazyflakes.nix")
	os.WriteFile(module, []byte("{ pkgs, ... }: {\n  environment.systemPackages = [\n    pkgs.htop # lazyflakes:pkg stable htop\n    pkgs.jq # lazyflakes:pkg stable jq\n  ];\n}\n"), 0o644)
	cfg := filepath.Join(dir, "config.toml")
	os.WriteFile(cfg, []byte("[targets.system]\nkind = \"nixos\"\nfile = '"+module+"'\nprofile = '"+profile+"'\nrebuild = \"true\"\nsets = { stable = \"pkgs\" }\n"), 0o644)

	m := New(generate.DefaultChannels(time.Now()), 0, false, dir, generate.Flake, false, 0, cfg)
	m.width, m.height = 150, 40
	m.loading = false
	m.syncSystem(0, false, false)
	m.tab = tabOptions
	m = press(t, m, "t")
	if m.tab != tabGenerations {
		t.Fatalf("tab = %v", m.tab)
	}
	return m
}

func TestGenerationsTabLists(t *testing.T) {
	m := genModel(t)
	if len(m.gens) != 3 || m.gens[0].Number != 3 || !m.gens[0].Current {
		t.Fatalf("gens = %+v", m.gens)
	}
	v := m.View()
	for _, want := range []string{"Generations", "[1] Generations 3 · system", "● ", "2 pkgs, 0 opts", "no record", "Current generation", "jq"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	// The current generation cannot be rolled back to.
	if m = press(t, m, "enter"); m.popup != popupNone || !strings.Contains(m.status, "current one") {
		t.Errorf("popup=%v status=%q", m.popup, m.status)
	}
}

func TestGenerationsTabShowsWhatRollingBackChanges(t *testing.T) {
	m := genModel(t)
	next, cmd := m.Update(keyMsg("j"))
	m = next.(Model)
	if cmd == nil {
		t.Fatal("moving to another generation compares it in the background")
	}
	v := m.View()
	for _, want := range []string{"Rolling back: 3 → 2", "−jq", "comparing the generations"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
	cur, sel := m.gens[0], m.gens[1]
	next, _ = m.Update(genDiffMsg{key: diffKey(cur, sel), lines: []string{"jq: 1.7.1 → ∅, -1.2 MiB"}})
	m = next.(Model)
	if v := m.View(); !strings.Contains(v, "jq: 1.7.1 → ∅") || strings.Contains(v, "comparing") {
		t.Errorf("the comparison should be shown:\n%s", v)
	}

	// Generation 1 has no record, so lazyflakes.nix would stay.
	m = press(t, m, "j")
	if v := m.View(); !strings.Contains(v, "no record: generation 1") {
		t.Errorf("view:\n%s", v)
	}
}

func TestGenerationsTabConfirmsRollback(t *testing.T) {
	m := genModel(t)
	m = press(t, m, "j", "enter")
	if m.popup != popupRollback || m.rollback == nil || m.rollback.To.Number != 2 {
		t.Fatalf("popup=%v rollback=%+v", m.popup, m.rollback)
	}
	if v := m.View(); !strings.Contains(v, "Roll system back from generation 3 to 2") || !strings.Contains(v, "−jq") || !strings.Contains(v, "then lazyflakes.nix is written") {
		t.Errorf("the popup should say what happens:\n%s", v)
	}
	if m = press(t, m, "esc"); m.popup != popupNone || m.rollback != nil {
		t.Errorf("esc cancels: popup=%v", m.popup)
	}

	m = press(t, m, "enter")
	next, cmd := m.Update(keyMsg("enter"))
	m = next.(Model)
	if cmd == nil || m.popup != popupNone {
		t.Errorf("enter runs the rollback in the terminal: popup=%v", m.popup)
	}

	// Afterwards everything is read again.
	next, _ = m.Update(rolledBackMsg{target: "system", number: 2})
	m = next.(Model)
	if !strings.Contains(m.status, "generation 2 now") || len(m.gens) != 3 {
		t.Errorf("status=%q gens=%d", m.status, len(m.gens))
	}
}

// Without a record nothing is written, and the popup does not claim so.
func TestGenerationsTabRollbackWithoutRecord(t *testing.T) {
	m := press(t, genModel(t), "j", "j", "enter")
	if m.rollback == nil || m.rollback.To.Number != 1 {
		t.Fatalf("rollback = %+v", m.rollback)
	}
	if v := m.View(); strings.Contains(v, "lazyflakes.nix is written") || !strings.Contains(v, "no record") {
		t.Errorf("popup:\n%s", v)
	}
	press(t, m, "esc")
}

func TestGenerationsTabWithoutTarget(t *testing.T) {
	m := genModel(t)
	m.configPath = filepath.Join(t.TempDir(), "missing.toml")
	m.syncSystem(0, false, false)
	m.loadGenerations()
	if v := m.View(); !strings.Contains(v, "no target is configured") {
		t.Errorf("view:\n%s", v)
	}
}
