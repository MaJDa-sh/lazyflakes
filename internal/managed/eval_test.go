package managed

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/generate"
)

// These tests evaluate real NixOS configurations that import a generated
// module. They download nixpkgs, so they only run with LAZYFLAKES_NIX_EVAL=1.

const minimalNixOS = `fileSystems."/" = { device = "/dev/null"; fsType = "ext4"; }; boot.loader.grub.enable = false; system.stateVersion = "25.11";`

func requireNixEval(t *testing.T) {
	if os.Getenv("LAZYFLAKES_NIX_EVAL") == "" {
		t.Skip("set LAZYFLAKES_NIX_EVAL=1 to evaluate generated modules with nix")
	}
}

func evalNames(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("nix", append([]string{"--extra-experimental-features", "nix-command flakes"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("nix %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeModule(t *testing.T, tgt *config.Target, m Module) {
	t.Helper()
	src, err := Render(tgt, m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tgt.File, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

var unfreeMixed = Module{
	Packages: []Entry{{"stable", "hello"}, {"unstable", "unrar"}},
	Options:  generate.Options(0).With(generate.Unfree),
}

func TestEvalChannelNixOS(t *testing.T) {
	requireNixEval(t)
	dir := t.TempDir()
	tgt := &config.Target{
		Name: "system", Kind: "nixos", File: filepath.Join(dir, "lazyflakes.nix"), Attribute: "environment.systemPackages",
		Sets: map[string]string{
			"stable":   "pkgs",
			"unstable": `import (fetchTarball "https://github.com/NixOS/nixpkgs/archive/nixpkgs-unstable.tar.gz") { inherit (pkgs) config; system = pkgs.stdenv.hostPlatform.system; }`,
		},
	}
	writeModule(t, tgt, unfreeMixed)
	os.WriteFile(filepath.Join(dir, "configuration.nix"), []byte(`{ imports = [ ./lazyflakes.nix ]; `+minimalNixOS+` }`), 0o644)

	out := evalNames(t, "eval", "--impure", "--json", "--expr",
		`map (p: p.name) (import <nixpkgs/nixos> { configuration = `+filepath.Join(dir, "configuration.nix")+`; }).config.environment.systemPackages`)
	for _, want := range []string{`"hello-`, `"unrar-`} {
		if !strings.Contains(out, want) {
			t.Errorf("systemPackages missing %s: %s", want, out)
		}
	}
}

func TestEvalFlakeNixOS(t *testing.T) {
	requireNixEval(t)
	dir := t.TempDir()
	tgt := &config.Target{
		Name: "system", Kind: "nixos", File: filepath.Join(dir, "lazyflakes.nix"), Attribute: "environment.systemPackages",
		Sets: map[string]string{"stable": "pkgs", "unstable": "pkgs-unstable"},
	}
	writeModule(t, tgt, unfreeMixed)
	os.WriteFile(filepath.Join(dir, "flake.nix"), []byte(`{
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/`+generate.StableBranch(time.Now())+`";
  inputs.nixpkgs-unstable.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  outputs = { nixpkgs, nixpkgs-unstable, ... }: {
    nixosConfigurations.test = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      specialArgs.pkgs-unstable = import nixpkgs-unstable { system = "x86_64-linux"; config.allowUnfree = true; };
      modules = [ ./lazyflakes.nix { `+minimalNixOS+` } ];
    };
  };
}`), 0o644)

	out := evalNames(t, "eval", "--json", dir+"#nixosConfigurations.test.config.environment.systemPackages", "--apply", "map (p: p.name)")
	for _, want := range []string{`"hello-`, `"unrar-`} {
		if !strings.Contains(out, want) {
			t.Errorf("systemPackages missing %s: %s", want, out)
		}
	}
}
