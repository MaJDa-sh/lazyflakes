package generate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNixAttrPath(t *testing.T) {
	cases := map[string]string{
		"ripgrep":                  "ripgrep",
		"python3Packages.requests": "python3Packages.requests",
		"_1password-gui":           "_1password-gui",
		"perlPackages.Foo2":        "perlPackages.Foo2",
		"gtk+3":                    `pkgs."gtk+3"`,
		"haskellPackages.3d":       `haskellPackages."3d"`,
	}
	for in, want := range cases {
		if got := nixAttrPath(in); got != want {
			t.Errorf("nixAttrPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderRefs(t *testing.T) {
	shell := Render(ShellNix, []string{"hello"}, "nixpkgs")
	if !strings.Contains(shell, "import <nixpkgs> { }") || !strings.Contains(shell, "    hello\n") {
		t.Errorf("unexpected shell.nix:\n%s", shell)
	}
	shell = Render(ShellNix, nil, "github:NixOS/nixpkgs/nixos-25.11")
	if !strings.Contains(shell, `fetchTarball "https://github.com/NixOS/nixpkgs/archive/nixos-25.11.tar.gz"`) {
		t.Errorf("shell.nix not pinned:\n%s", shell)
	}
	flake := Render(Flake, []string{"hello"}, "nixpkgs")
	if !strings.Contains(flake, `inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";`) {
		t.Errorf("unexpected flake.nix:\n%s", flake)
	}
}

func TestWriteRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	if _, err := Write(dir, Flake, []string{"hello"}, "nixpkgs", false); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(dir, Flake, []string{"jq"}, "nixpkgs", false); !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists, got %v", err)
	}
	if _, err := Write(dir, Flake, []string{"jq"}, "nixpkgs", true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "flake.nix"))
	if !strings.Contains(string(data), " jq\n") || strings.Contains(string(data), "hello") {
		t.Errorf("overwrite did not replace contents:\n%s", data)
	}
}
