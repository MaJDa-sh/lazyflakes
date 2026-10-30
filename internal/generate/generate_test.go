package generate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testChannels = []Channel{
	{Name: "unstable", Input: "nixpkgs", Var: "pkgs", Ref: "github:NixOS/nixpkgs/nixpkgs-unstable"},
	{Name: "stable", Input: "nixpkgs-stable", Var: "pkgsStable", Ref: "github:NixOS/nixpkgs/nixos-26.05"},
}

func TestNixAttrPath(t *testing.T) {
	cases := []struct {
		attr string
		with bool
		want string
	}{
		{"ripgrep", true, "ripgrep"},
		{"python3Packages.requests", true, "python3Packages.requests"},
		{"_1password-gui", true, "_1password-gui"},
		{"gtk+3", true, `pkgsStable."gtk+3"`},
		{"haskellPackages.3d", true, `haskellPackages."3d"`},
		{"ripgrep", false, "pkgsStable.ripgrep"},
		// Quoted the Nix way: no interpolation, no Go-only escapes.
		{"a${b}", false, `pkgsStable."a\${b}"`},
		{"café", false, `pkgsStable."café"`},
	}
	for _, c := range cases {
		if got := nixAttrPath(c.attr, "pkgsStable", c.with); got != c.want {
			t.Errorf("nixAttrPath(%q, with=%v) = %q, want %q", c.attr, c.with, got, c.want)
		}
	}
}

func TestNixString(t *testing.T) {
	for in, want := range map[string]string{
		`plain`:         `"plain"`,
		`say "hi" \ ok`: `"say \"hi\" \\ ok"`,
		"${x} $y $${z}": `"\${x} $y $\${z}"`,
		"a\nb\tc":       `"a\nb\tc"`,
	} {
		if got := NixString(in); got != want {
			t.Errorf("NixString(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestStableBranch(t *testing.T) {
	cases := map[string]string{
		"2026-10-24": "nixos-26.05",
		"2026-06-01": "nixos-26.05",
		"2026-05-31": "nixos-25.11",
		"2026-12-01": "nixos-26.11",
		"2027-01-15": "nixos-26.11",
		"2100-02-01": "nixos-99.11",
	}
	for date, want := range cases {
		d, _ := time.Parse(time.DateOnly, date)
		if got := StableBranch(d); got != want {
			t.Errorf("StableBranch(%s) = %s, want %s", date, got, want)
		}
	}
}

func TestRenderSingleChannel(t *testing.T) {
	pkgs := []Package{{Attr: "hello", Channel: 0}}
	shell := Render(ShellNix, testChannels, pkgs, 0)
	want := `{ pkgs ? import (fetchTarball "https://github.com/NixOS/nixpkgs/archive/nixpkgs-unstable.tar.gz") { } }:

pkgs.mkShell {
  packages = with pkgs; [
    hello
  ];
}
`
	if shell != want {
		t.Errorf("shell.nix:\n%s\nwant:\n%s", shell, want)
	}

	flake := Render(Flake, testChannels, []Package{{Attr: "hello", Channel: 1}}, 0)
	for _, s := range []string{
		`nixpkgs-stable.url = "github:NixOS/nixpkgs/nixos-26.05";`,
		"outputs = { nixpkgs-stable, ... }:",
		"default = pkgsStable.mkShell {",
		"packages = with pkgsStable; [\n            hello\n          ];",
	} {
		if !strings.Contains(flake, s) {
			t.Errorf("flake.nix missing %q:\n%s", s, flake)
		}
	}
	if strings.Contains(flake, "nixpkgs.url") {
		t.Errorf("unused unstable input in flake.nix:\n%s", flake)
	}
}

func TestRenderMixedChannels(t *testing.T) {
	pkgs := []Package{{Attr: "jq", Channel: 1}, {Attr: "ripgrep", Channel: 0}}
	shell := Render(ShellNix, testChannels, pkgs, 0)
	for _, s := range []string{
		"{ pkgs ? import (fetchTarball \"https://github.com/NixOS/nixpkgs/archive/nixpkgs-unstable.tar.gz\") { }\n" +
			", pkgsStable ? import (fetchTarball \"https://github.com/NixOS/nixpkgs/archive/nixos-26.05.tar.gz\") { }\n}:",
		"pkgs.mkShell {",
		"packages = [\n    pkgsStable.jq\n    pkgs.ripgrep\n  ];",
	} {
		if !strings.Contains(shell, s) {
			t.Errorf("shell.nix missing %q:\n%s", s, shell)
		}
	}
	flake := Render(Flake, testChannels, pkgs, 0)
	for _, s := range []string{
		"outputs = { nixpkgs, nixpkgs-stable, ... }:",
		"forAllSystems = f: nixpkgs.lib.genAttrs",
		"pkgsStable = nixpkgs-stable.legacyPackages.${system};",
		"devShells = forAllSystems ({ pkgs, pkgsStable }: {",
	} {
		if !strings.Contains(flake, s) {
			t.Errorf("flake.nix missing %q:\n%s", s, flake)
		}
	}
}

func TestRenderNonGithubRef(t *testing.T) {
	chans := []Channel{{Name: "local", Input: "nixpkgs", Var: "pkgs", Ref: "nixpkgs"}}
	pkgs := []Package{{Attr: "hello"}}
	if s := Render(ShellNix, chans, pkgs, 0); !strings.Contains(s, "{ pkgs ? import <nixpkgs> { } }:") {
		t.Errorf("shell.nix should fall back to <nixpkgs>:\n%s", s)
	}
	if s := Render(Flake, chans, pkgs, 0); !strings.Contains(s, `nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";`) {
		t.Errorf("flake.nix should resolve the registry name:\n%s", s)
	}
}

func TestWriteRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	hello := []Package{{Attr: "hello"}}
	jq := []Package{{Attr: "jq"}}
	if _, err := Write(dir, Flake, testChannels, hello, 0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(dir, Flake, testChannels, jq, 0, false); !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
	if _, err := Write(dir, Flake, testChannels, jq, 0, true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "flake.nix"))
	if !strings.Contains(string(data), " jq\n") || strings.Contains(string(data), "hello") {
		t.Errorf("overwrite did not replace contents:\n%s", data)
	}
}

func TestWriteEnvrc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".envrc")
	read := func() string { b, _ := os.ReadFile(path); return string(b) }

	if _, changed, err := WriteEnvrc(dir, Flake); err != nil || !changed || read() != "use flake\n" {
		t.Fatalf("create: changed=%v err=%v content=%q", changed, err, read())
	}
	if _, changed, _ := WriteEnvrc(dir, Flake); changed {
		t.Fatal("same format should leave .envrc alone")
	}
	if _, changed, _ := WriteEnvrc(dir, ShellNix); !changed || read() != "use nix\n" {
		t.Fatalf("switch format: changed=%v content=%q", changed, read())
	}

	os.WriteFile(path, []byte("export FOO=1\nuse flake .#dev\nuse nix\n"), 0o644)
	if _, changed, _ := WriteEnvrc(dir, Flake); !changed || read() != "export FOO=1\nuse flake .#dev\n" {
		t.Fatalf("custom directive kept, duplicate dropped: content=%q", read())
	}

	os.WriteFile(path, []byte("export FOO=1"), 0o644)
	if _, _, _ = WriteEnvrc(dir, ShellNix); read() != "export FOO=1\nuse nix\n" {
		t.Fatalf("append: content=%q", read())
	}
}

func TestShellExpr(t *testing.T) {
	got := ShellExpr(testChannels, []Package{{Attr: "jq", Channel: 1}, {Attr: "gtk+3", Channel: 1}}, 0)
	want := `let pkgsStable = (builtins.getFlake "github:NixOS/nixpkgs/nixos-26.05").legacyPackages.${builtins.currentSystem}; ` +
		`in pkgsStable.mkShell { name = "lazyflakes"; packages = [ pkgsStable.jq pkgsStable."gtk+3" ]; }`
	if got != want {
		t.Errorf("ShellExpr:\n got %s\nwant %s", got, want)
	}
	mixed := ShellExpr(testChannels, []Package{{Attr: "jq", Channel: 1}, {Attr: "ripgrep", Channel: 0}}, 0)
	if !strings.Contains(mixed, "in pkgs.mkShell") || !strings.Contains(mixed, "[ pkgsStable.jq pkgs.ripgrep ]") {
		t.Errorf("mixed ShellExpr: %s", mixed)
	}
}

func TestOptions(t *testing.T) {
	opts, err := ParseOptions("unfree, insecure")
	if err != nil || !opts.Has(Unfree) || !opts.Has(Insecure) || opts.Has(Broken) {
		t.Fatalf("ParseOptions: %v %v", opts, err)
	}
	if _, err := ParseOptions("unfree,nope"); err == nil {
		t.Error("unknown option should fail")
	}
	if got := opts.Env(); strings.Join(got, " ") != "NIXPKGS_ALLOW_UNFREE=1 NIXPKGS_ALLOW_INSECURE=1" {
		t.Errorf("Env = %v", got)
	}
	if opts.Toggle(Unfree).Has(Unfree) {
		t.Error("Toggle should clear a set option")
	}

	pkgs := []Package{{Attr: "unrar", Channel: 0}, {Attr: "jq", Channel: 1}}
	shell := Render(ShellNix, testChannels, pkgs, opts)
	cfg := "{ config = { allowUnfree = true; allowInsecurePredicate = _: true; }; }"
	if strings.Count(shell, cfg) != 2 {
		t.Errorf("shell.nix should pass config to both channels:\n%s", shell)
	}

	flake := Render(Flake, testChannels, pkgs, opts)
	for _, s := range []string{
		"      config = { allowUnfree = true; allowInsecurePredicate = _: true; };\n",
		"pkgs = import nixpkgs { inherit system config; };",
		"pkgsStable = import nixpkgs-stable { inherit system config; };",
	} {
		if !strings.Contains(flake, s) {
			t.Errorf("flake.nix missing %q:\n%s", s, flake)
		}
	}
	if strings.Contains(flake, "legacyPackages") {
		t.Errorf("flake.nix with config must not use legacyPackages:\n%s", flake)
	}

	expr := ShellExpr(testChannels, pkgs[:1], opts)
	want := `let config = { allowUnfree = true; allowInsecurePredicate = _: true; }; ` +
		`pkgs = import (builtins.getFlake "github:NixOS/nixpkgs/nixpkgs-unstable").outPath { system = builtins.currentSystem; inherit config; }; ` +
		`in pkgs.mkShell { name = "lazyflakes"; packages = [ pkgs.unrar ]; }`
	if expr != want {
		t.Errorf("ShellExpr:\n got %s\nwant %s", expr, want)
	}
}
