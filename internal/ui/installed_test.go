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
