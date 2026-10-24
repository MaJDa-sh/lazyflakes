package main

// End-to-end tests: they build the lazyflakes binary and drive it like a
// user would against real configurations, which nix then builds. They use
// the nixpkgs from NIX_PATH (and download home-manager), so they only run
// with LAZYFLAKES_E2E=1; the VM test also needs LAZYFLAKES_VM=1 and KVM.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// container keeps the test systems small: no kernel, initrd or bootloader.
const container = `boot.isContainer = true; system.stateVersion = "25.11";`

type e2e struct {
	t       *testing.T
	bin     string
	dir     string
	nixpkgs string
}

func newE2E(t *testing.T) *e2e {
	if os.Getenv("LAZYFLAKES_E2E") == "" {
		t.Skip("set LAZYFLAKES_E2E=1 to run end-to-end tests")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "lazyflakes")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command("nix-instantiate", "--find-file", "nixpkgs").Output()
	if err != nil {
		t.Skip("no <nixpkgs> in NIX_PATH")
	}
	nixpkgs, _ := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	return &e2e{t: t, bin: bin, dir: dir, nixpkgs: nixpkgs}
}

func (e *e2e) write(path, content string) {
	e.t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// run runs lazyflakes and returns its output; wantErr says whether it
// should fail.
func (e *e2e) run(wantErr bool, args ...string) string {
	e.t.Helper()
	cmd := exec.Command(e.bin, args...)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	e.t.Logf("$ lazyflakes %s\n%s", strings.Join(args, " "), out)
	if (err != nil) != wantErr {
		e.t.Fatalf("lazyflakes %s: err = %v, wantErr %v", strings.Join(args, " "), err, wantErr)
	}
	return string(out)
}

func (e *e2e) git(dir string, args ...string) {
	e.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestE2EChannelNixOS(t *testing.T) {
	e := newE2E(t)
	sys := filepath.Join(e.dir, "nixos")
	e.write(filepath.Join(sys, "configuration.nix"), "{ imports = [ ./lazyflakes.nix ]; "+container+" }\n")
	cfg := filepath.Join(e.dir, "config.toml")
	e.write(cfg, `[targets.system]
kind = "nixos"
file = '`+filepath.Join(sys, "lazyflakes.nix")+`'
rebuild = 'cd `+sys+` && nixos-rebuild build -I nixos-config=`+sys+`/configuration.nix -I nixpkgs=`+e.nixpkgs+`'
sets = { stable = "pkgs" }
`)

	e.run(false, "add", "-config", cfg, "hello")
	if !exists(filepath.Join(sys, "result", "sw", "bin", "hello")) {
		t.Fatal("hello is not in the built system")
	}
	if out := e.run(false, "list", "-config", cfg); !strings.Contains(out, "hello") {
		t.Errorf("list: %s", out)
	}

	before, _ := os.ReadFile(filepath.Join(sys, "lazyflakes.nix"))
	e.run(true, "add", "-config", cfg, "this-package-does-not-exist")
	after, _ := os.ReadFile(filepath.Join(sys, "lazyflakes.nix"))
	if string(before) != string(after) {
		t.Errorf("a failed rebuild must restore the module:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	e.run(false, "remove", "-config", cfg, "hello")
	if exists(filepath.Join(sys, "result", "sw", "bin", "hello")) {
		t.Error("hello is still in the built system after remove")
	}
}

func TestE2EFlakeNixOS(t *testing.T) {
	e := newE2E(t)
	flake := filepath.Join(e.dir, "config")
	e.write(filepath.Join(flake, "flake.nix"), `{
  inputs.nixpkgs.url = "path:`+e.nixpkgs+`";
  outputs = { nixpkgs, ... }: {
    nixosConfigurations.box = nixpkgs.lib.nixosSystem { system = "x86_64-linux"; modules = [ ./hosts/box.nix ]; };
  };
}
`)
	host := filepath.Join(flake, "hosts", "box.nix")
	e.write(host, "{ "+container+" }\n")
	e.git(flake, "init", "-q")
	e.git(flake, "add", ".")
	e.git(flake, "commit", "-qm", "init")

	// setup evaluates the flake, writes the config and creates the module.
	cfg := filepath.Join(e.dir, "config.toml")
	out := e.run(false, "setup", "-yes", "-config", cfg, "-flake", flake, "-host", "box")
	if !strings.Contains(out, "add ./lazyflakes.nix to the imports") {
		t.Errorf("setup should ask for the import: %s", out)
	}
	module := filepath.Join(flake, "lazyflakes.nix")
	if !exists(module) {
		t.Fatal("setup did not create the module")
	}
	if out, _ := exec.Command("git", "-C", flake, "ls-files", "lazyflakes.nix").Output(); strings.TrimSpace(string(out)) != "lazyflakes.nix" {
		t.Error("setup did not git-add the module")
	}
	data, _ := os.ReadFile(cfg)
	if !strings.Contains(string(data), "flake = '"+flake+"#box'") {
		t.Fatalf("config misses the flake output:\n%s", data)
	}
	if out := e.run(false, "list", "-config", cfg); !strings.Contains(out, "not imported") {
		t.Errorf("list should warn before the import is added: %s", out)
	}

	// The user adds the import; the test builds instead of switching.
	e.write(host, "{ imports = [ ../lazyflakes.nix ]; "+container+" }\n")
	build := "cd " + flake + " && nixos-rebuild build --flake " + flake + "#box"
	e.write(cfg, strings.Replace(string(data), "rebuild = 'sudo nixos-rebuild switch --flake "+flake+"#box'", "rebuild = '"+build+"'", 1))

	e.run(false, "add", "-config", cfg, "hello")
	if !exists(filepath.Join(flake, "result", "sw", "bin", "hello")) {
		t.Fatal("hello is not in the built system")
	}
	if out := e.run(false, "list", "-config", cfg); strings.Contains(out, "not imported") {
		t.Errorf("the module is imported now: %s", out)
	}
}

func TestE2EHomeManagerFlake(t *testing.T) {
	e := newE2E(t)
	flake := filepath.Join(e.dir, "home")
	e.write(filepath.Join(flake, "flake.nix"), `{
  inputs.nixpkgs.url = "path:`+e.nixpkgs+`";
  inputs.home-manager = { url = "github:nix-community/home-manager/release-25.11"; inputs.nixpkgs.follows = "nixpkgs"; };
  outputs = { nixpkgs, home-manager, ... }: {
    homeConfigurations.me = home-manager.lib.homeManagerConfiguration {
      pkgs = nixpkgs.legacyPackages.x86_64-linux;
      modules = [ ./lazyflakes.nix { home.username = "me"; home.homeDirectory = "/home/me"; home.stateVersion = "25.11"; } ];
    };
  };
}
`)
	module := filepath.Join(flake, "lazyflakes.nix")
	e.write(module, "{ ... }: { }\n")
	e.git(flake, "init", "-q")
	e.git(flake, "add", ".")

	cfg := filepath.Join(e.dir, "config.toml")
	e.write(cfg, `[targets.home]
kind = "home-manager"
file = '`+module+`'
flake = '`+flake+`#me'
rebuild = 'nix --extra-experimental-features "nix-command flakes" build `+flake+`#homeConfigurations.me.activationPackage -o `+flake+`/result'
sets = { stable = "pkgs" }
`)
	e.run(false, "add", "-config", cfg, "-allow", "unfree", "hello", "unrar")
	for _, bin := range []string{"hello", "unrar"} {
		if !exists(filepath.Join(flake, "result", "home-path", "bin", bin)) {
			t.Errorf("%s is not in the home-manager generation", bin)
		}
	}
	if out := e.run(false, "list", "-config", cfg); strings.Contains(out, "not imported") || !strings.Contains(out, "options: unfree") {
		t.Errorf("list: %s", out)
	}
}

func TestE2EVirtualMachine(t *testing.T) {
	e := newE2E(t)
	if os.Getenv("LAZYFLAKES_VM") == "" {
		t.Skip("set LAZYFLAKES_VM=1 to boot a NixOS VM")
	}
	if !exists("/dev/kvm") {
		t.Skip("no /dev/kvm")
	}
	module := filepath.Join(e.dir, "lazyflakes.nix")
	cfg := filepath.Join(e.dir, "config.toml")
	e.write(cfg, `[targets.vm]
kind = "nixos"
file = '`+module+`'
sets = { stable = "pkgs" }
`)
	e.run(false, "add", "-config", cfg, "-allow", "unfree", "-no-rebuild", "hello", "unrar")

	wd, _ := os.Getwd()
	cmd := exec.Command("nix-build", filepath.Join(wd, "tests", "vm.nix"),
		"-I", "nixpkgs="+e.nixpkgs, "--arg", "module", module, "-o", filepath.Join(e.dir, "result"))
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("VM test failed: %v", err)
	}
}
