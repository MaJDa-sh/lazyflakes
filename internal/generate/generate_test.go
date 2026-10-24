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
