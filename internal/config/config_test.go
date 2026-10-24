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
