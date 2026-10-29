package managed

import (
	"context"
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

// A module with options evaluates in a real NixOS configuration and changes
// the result, quoted attribute paths included.
func TestEvalSettingsNixOS(t *testing.T) {
	requireNixEval(t)
	dir := t.TempDir()
	tgt := &config.Target{Name: "system", Kind: "nixos", File: filepath.Join(dir, "lazyflakes.nix"),
		Attribute: "environment.systemPackages", Sets: map[string]string{"stable": "pkgs"}}
	writeModule(t, tgt, Module{
		Packages: []Entry{{"stable", "hello"}},
		Settings: []Setting{
			{Path: "services.openssh.enable", Value: "true"},
			{Path: "networking.hostName", Value: `"lazybox"`},
			{Path: "time.timeZone", Value: `"Europe/Warsaw"`},
			{Path: `systemd.services."odd-name".enable`, Value: "false"},
			{Path: "services.xserver.videoDrivers", Value: `[ "nvidia" "amdgpu" ]`},
		},
	})
	os.WriteFile(filepath.Join(dir, "configuration.nix"), []byte(`{ imports = [ ./lazyflakes.nix ]; `+minimalNixOS+` }`), 0o644)

	out := evalNames(t, "eval", "--impure", "--json", "--expr",
		`let c = (import <nixpkgs/nixos> { configuration = `+filepath.Join(dir, "configuration.nix")+`; }).config; in
		 { ssh = c.services.openssh.enable; host = c.networking.hostName; tz = c.time.timeZone; drivers = c.services.xserver.videoDrivers; }`)
	for _, want := range []string{`"ssh":true`, `"host":"lazybox"`, `"tz":"Europe/Warsaw"`, `"drivers":["nvidia","amdgpu"]`} {
		if !strings.Contains(out, want) {
			t.Errorf("evaluated config lacks %s: %s", want, out)
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

// localNixpkgs is the nixpkgs from NIX_PATH, so tests need no download.
func localNixpkgs(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("nix-instantiate", "--find-file", "nixpkgs").Output()
	if err != nil {
		t.Skip("no <nixpkgs> in NIX_PATH")
	}
	p, _ := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	return p
}

func TestCheckImportedFlake(t *testing.T) {
	requireNixEval(t)
	dir := t.TempDir()
	tgt := &config.Target{Name: "system", Kind: "nixos", File: filepath.Join(dir, "lazyflakes.nix"),
		Attribute: "environment.systemPackages", Flake: dir + "#test", Sets: map[string]string{"stable": "pkgs"}}
	writeModule(t, tgt, Module{Packages: []Entry{{"stable", "hello"}}})
	os.MkdirAll(filepath.Join(dir, "hosts", "test"), 0o755)
	host := filepath.Join(dir, "hosts", "test", "default.nix")
	os.WriteFile(filepath.Join(dir, "flake.nix"), []byte(`{
  inputs.nixpkgs.url = "path:`+localNixpkgs(t)+`";
  outputs = { nixpkgs, ... }: {
    nixosConfigurations.test = nixpkgs.lib.nixosSystem { system = "x86_64-linux"; modules = [ ./hosts/test ]; };
  };
}`), 0o644)
	exec.Command("git", "-C", dir, "init", "-q").Run()

	// Only mentioned in a comment: the text search is fooled, evaluation is not.
	os.WriteFile(host, []byte("# TODO import ../../lazyflakes.nix\n{ "+minimalNixOS+" }\n"), 0o644)
	exec.Command("git", "-C", dir, "add", ".").Run()
	if !Imported(tgt) {
		t.Fatal("expected the text search to match the comment")
	}
	if ok, err := CheckImported(context.Background(), tgt); err != nil || ok {
		t.Errorf("comment only: imported=%v err=%v, want false", ok, err)
	}

	os.WriteFile(host, []byte("{ imports = [ ../../lazyflakes.nix ]; "+minimalNixOS+" }\n"), 0o644)
	if ok, err := CheckImported(context.Background(), tgt); err != nil || !ok {
		t.Errorf("imported from hosts/test: imported=%v err=%v, want true", ok, err)
	}
}

func TestCheckImportedChannel(t *testing.T) {
	requireNixEval(t)
	dir := t.TempDir()
	tgt := &config.Target{Name: "system", Kind: "nixos", File: filepath.Join(dir, "lazyflakes.nix"),
		Attribute: "environment.systemPackages", Sets: map[string]string{"stable": "pkgs"}}
	writeModule(t, tgt, Module{Packages: []Entry{{"stable", "hello"}}})
	conf := filepath.Join(dir, "configuration.nix")
	t.Setenv("NIX_PATH", "nixpkgs="+localNixpkgs(t)+":nixos-config="+conf)

	os.WriteFile(conf, []byte("{ "+minimalNixOS+" }\n"), 0o644)
	if ok, err := CheckImported(context.Background(), tgt); err != nil || ok {
		t.Errorf("not imported: imported=%v err=%v", ok, err)
	}
	os.WriteFile(conf, []byte("{ imports = [ ./lazyflakes.nix ]; "+minimalNixOS+" }\n"), 0o644)
	if ok, err := CheckImported(context.Background(), tgt); err != nil || !ok {
		t.Errorf("imported: imported=%v err=%v", ok, err)
	}

	// A module outside the NIX_PATH configuration cannot be checked this way.
	other := *tgt
	other.File = filepath.Join(t.TempDir(), "lazyflakes.nix")
	os.WriteFile(other.File, []byte("{ }"), 0o644)
	if _, err := CheckImported(context.Background(), &other); err == nil {
		t.Error("expected an error for a module outside the configuration")
	}
}
