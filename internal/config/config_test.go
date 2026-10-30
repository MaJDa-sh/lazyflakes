package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	path := writeConfig(t, `
default = "system"

[targets.system]
kind = "nixos"
file = "/etc/nixos/lazyflakes.nix"
rebuild = "sudo nixos-rebuild switch"
sets = { stable = "pkgs", unstable = "pkgs-unstable" }

[targets.home]
kind = "home-manager"
file = "~/.config/home-manager/lazyflakes.nix"
`)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	targets := c.Ordered()
	if len(targets) != 2 || targets[0].Name != "system" || targets[1].Name != "home" {
		t.Fatalf("Ordered = %+v", targets)
	}
	sys, home := targets[0], targets[1]
	if sys.Attribute != "environment.systemPackages" || sys.Sets["unstable"] != "pkgs-unstable" {
		t.Errorf("system target = %+v", sys)
	}
	if home.Attribute != "home.packages" || !filepath.IsAbs(home.File) || strings.Contains(home.File, "~") {
		t.Errorf("home target = %+v", home)
	}
	if sys.Sudo != "sudo" || !sys.WritesNixpkgsConfig() {
		t.Errorf("defaults: sudo=%q nixpkgs_config=%v", sys.Sudo, sys.WritesNixpkgsConfig())
	}
}

func TestLoadSudoFlakeAndNixpkgsConfig(t *testing.T) {
	c, err := Load(writeConfig(t, `
sudo = "doas"
[targets.laptop]
kind = "nixos"
file = "/etc/nixos/lazyflakes.nix"
flake = "/etc/nixos#laptop"
nixpkgs_config = false
`))
	if err != nil {
		t.Fatal(err)
	}
	l := c.Targets["laptop"]
	if l.Sudo != "doas" || l.Flake != "/etc/nixos#laptop" || l.WritesNixpkgsConfig() {
		t.Errorf("laptop = %+v", l)
	}
	if _, err := Load(writeConfig(t, "[targets.x]\nkind = \"nixos\"\nfile = \"/x.nix\"\nflake = \"/etc/nixos\"\n")); err == nil {
		t.Error("a flake without #name should be rejected")
	}
}

func TestLoadScriptTarget(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/var/state")
	c, err := Load(writeConfig(t, `
[targets.mine]
kind = "script"
script = "~/bin/add-package --verbose"

[targets.custom]
kind = "script"
script = "/usr/bin/true"
file = "/tmp/custom-state.json"
`))
	if err != nil {
		t.Fatal(err)
	}
	mine, custom := c.Targets["mine"], c.Targets["custom"]
	if !mine.IsScript() || strings.Contains(mine.Script, "~") || !strings.HasSuffix(mine.Script, "/bin/add-package --verbose") {
		t.Errorf("script = %q", mine.Script)
	}
	if mine.File != "/var/state/lazyflakes/mine.json" || custom.File != "/tmp/custom-state.json" {
		t.Errorf("state files: %q, %q", mine.File, custom.File)
	}
	if !mine.HasChannel("stable") || !mine.HasChannel("anything") {
		t.Error("a script decides which channels it supports")
	}
	module := &Target{Kind: "nixos", Sets: map[string]string{"stable": "pkgs"}}
	if !module.HasChannel("stable") || module.HasChannel("unstable") {
		t.Error("a module target needs a package set per channel")
	}
	if _, err := Load(writeConfig(t, "[targets.x]\nkind = \"script\"\n")); err == nil {
		t.Error("a script target without a script should be rejected")
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml")); !errors.Is(err, ErrNoConfig) {
		t.Errorf("missing file: got %v, want ErrNoConfig", err)
	}
	cases := map[string]string{
		"unknown kind":  "[targets.x]\nkind = \"guix\"\nfile = \"/x.nix\"\n",
		"missing file":  "[targets.x]\nkind = \"nixos\"\n",
		"relative file": "[targets.x]\nkind = \"nixos\"\nfile = \"x.nix\"\n",
		"bad default":   "default = \"nope\"\n[targets.x]\nkind = \"nixos\"\nfile = \"/x.nix\"\n",
		"invalid toml":  "[targets.x\n",
	}
	for name, content := range cases {
		if _, err := Load(writeConfig(t, content)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestScriptTargetWithoutStateDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	if dir := StateDir(); dir != "" {
		t.Fatalf("StateDir = %q, want none rather than a shared directory", dir)
	}
	tgt := &Target{Name: "mine", Kind: KindScript, Script: "true"}
	if err := tgt.normalize(); err == nil || !strings.Contains(err.Error(), "file =") {
		t.Errorf("err = %v", err)
	}
	tgt = &Target{Name: "mine", Kind: KindScript, Script: "true", File: "/var/lib/me/state.json"}
	if err := tgt.normalize(); err != nil {
		t.Errorf("an explicit file needs no state directory: %v", err)
	}
}
