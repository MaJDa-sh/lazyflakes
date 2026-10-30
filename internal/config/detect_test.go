package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MaJDa-sh/lazyflakes/internal/generate"
)

var channels = generate.DefaultChannels(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))

// roundTrip checks that the rendered TOML loads back into the same targets.
func roundTrip(t *testing.T, d Detected) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(d.TOML()), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("generated config does not load: %v\n%s", err, d.TOML())
	}
	return c
}

func TestDetectChannelNixOS(t *testing.T) {
	d := Detect(System{
		OS: "linux", NixOSVersion: "25.11.9586.10e7ad5bbcb4 (Xantusia)",
		NixOSConfig: "/etc/nixos/configuration.nix", NixSystem: "x86_64-linux",
	}, channels, "")
	c := roundTrip(t, d)

	sys := c.Targets["system"]
	if c.Default != "system" || sys == nil {
		t.Fatalf("config = %+v", c)
	}
	if sys.File != "/etc/nixos/lazyflakes.nix" || sys.Rebuild != "sudo nixos-rebuild switch" || sys.Attribute != "environment.systemPackages" {
		t.Errorf("system target = %+v", sys)
	}
	if sys.Sets["stable"] != "pkgs" {
		t.Errorf("a 25.11 system should map stable to pkgs: %v", sys.Sets)
	}
	if !strings.Contains(sys.Sets["unstable"], `fetchTarball "https://github.com/NixOS/nixpkgs/archive/nixpkgs-unstable.tar.gz"`) {
		t.Errorf("channel configs can fetch unstable: %v", sys.Sets)
	}
	if !strings.Contains(d.TOML(), "nixos-25.11") {
		t.Errorf("notes should mention the system release:\n%s", d.TOML())
	}
}

func TestDetectFlakeNixOSAndHomeManager(t *testing.T) {
	d := Detect(System{
		OS: "linux", NixOSVersion: "26.11pre123.abc (Yarara)", Sudo: "sudo", NixSystem: "aarch64-linux",
		Flakes:         []*Flake{{Dir: "/home/me/nixos-config", GitRepo: true, NixOS: []FlakeHost{{Name: "laptop"}}}},
		HomeManagerDir: "/home/me/.config/home-manager", HomeManagerVer: "26.05",
	}, channels, "")
	c := roundTrip(t, d)

	sys := c.Targets["system"]
	if sys.Rebuild != "sudo nixos-rebuild switch --flake /home/me/nixos-config#laptop" || sys.File != "/home/me/nixos-config/lazyflakes.nix" || sys.Flake != "/home/me/nixos-config#laptop" {
		t.Errorf("system target = %+v", sys)
	}
	if len(sys.Sets) != 1 || sys.Sets["unstable"] != "pkgs" {
		t.Errorf("a pure flake on unstable only gets pkgs: %v", sys.Sets)
	}
	toml := d.TOML()
	for _, s := range []string{
		`# stable = "pkgs-stable"`,
		`inputs.nixpkgs-stable.url = "github:NixOS/nixpkgs/nixos-26.05";`,
		`system = "aarch64-linux"`,
		"git-added",
	} {
		if !strings.Contains(toml, s) {
			t.Errorf("missing %q:\n%s", s, toml)
		}
	}

	home := c.Targets["home"]
	if home == nil || home.Kind != "home-manager" || home.Attribute != "home.packages" || home.Rebuild != "home-manager switch" {
		t.Fatalf("home target = %+v", home)
	}
	if home.Sets["stable"] != "pkgs" || !strings.Contains(home.Sets["unstable"], "fetchTarball") {
		t.Errorf("home sets = %v", home.Sets)
	}
	if c.Default != "system" {
		t.Errorf("default = %q", c.Default)
	}
}

func TestDetectSystemAndHomeFlakes(t *testing.T) {
	d := Detect(System{
		OS: "linux", NixOSVersion: "25.11.1", Sudo: "doas", User: "me", Hostname: "laptop", HasHomeManager: true,
		Flakes: []*Flake{
			{Dir: "/etc/nixos", NixOS: []FlakeHost{{Name: "desktop"}, {Name: "laptop", PkgsSet: true}}},
			{Dir: "/home/me/.config/home-manager", Home: []FlakeHost{{Name: "other"}, {Name: "me@laptop"}}},
		},
	}, channels, "")
	c := roundTrip(t, d)

	if c.Sudo != "doas" {
		t.Errorf("sudo = %q", c.Sudo)
	}
	sys := c.Targets["system"]
	if sys.Flake != "/etc/nixos#laptop" || sys.File != "/etc/nixos/lazyflakes-laptop.nix" || sys.Rebuild != "doas nixos-rebuild switch --flake /etc/nixos#laptop" {
		t.Errorf("the host matching the hostname should be picked: %+v", sys)
	}
	if sys.WritesNixpkgsConfig() {
		t.Error("a host that sets nixpkgs.pkgs must get nixpkgs_config = false")
	}
	home := c.Targets["home"]
	if home == nil || home.Flake != "/home/me/.config/home-manager#me@laptop" || home.Rebuild != "home-manager switch --flake /home/me/.config/home-manager#me@laptop" || home.File != "/home/me/.config/home-manager/lazyflakes.nix" {
		t.Errorf("home target = %+v", home)
	}

	// -host overrides the hostname match; one flake holding both kinds
	// keeps the two modules apart.
	d = Detect(System{
		OS: "linux", NixOSVersion: "25.11.1", Sudo: "sudo", User: "me", Hostname: "laptop",
		Flakes: []*Flake{{Dir: "/cfg", NixOS: []FlakeHost{{Name: "desktop"}, {Name: "laptop"}}, Home: []FlakeHost{{Name: "me"}}}},
	}, channels, "desktop")
	c = roundTrip(t, d)
	if c.Targets["system"].Flake != "/cfg#desktop" {
		t.Errorf("-host desktop: %+v", c.Targets["system"])
	}
	if h := c.Targets["home"]; h.File != "/cfg/lazyflakes-home.nix" || !strings.HasPrefix(h.Rebuild, "nix run github:nix-community/home-manager -- switch --flake /cfg#me") {
		t.Errorf("home in the system flake: %+v", h)
	}
}

func TestDetectUnevaluatedFlake(t *testing.T) {
	d := Detect(System{
		OS: "linux", NixOSVersion: "25.11.1", Sudo: "sudo",
		Flakes: []*Flake{{Dir: "/etc/nixos", NixOS: []FlakeHost{{}}, EvalErr: "boom"}},
	}, channels, "")
	sys := roundTrip(t, d).Targets["system"]
	if sys.Flake != "" || sys.Rebuild != "sudo nixos-rebuild switch --flake /etc/nixos" {
		t.Errorf("unknown host names fall back to the bare flake: %+v", sys)
	}
	if !strings.Contains(d.TOML(), "could not evaluate the flake (boom)") {
		t.Errorf("the evaluation error should be explained:\n%s", d.TOML())
	}
}

func TestDetectDarwin(t *testing.T) {
	d := Detect(System{OS: "darwin", HasDarwinRebuild: true, Sudo: "sudo", NixSystem: "aarch64-darwin",
		Flakes: []*Flake{{Dir: "/Users/me/.config/nix-darwin", Darwin: []FlakeHost{{Name: "mac"}}}}}, channels, "")
	sys := roundTrip(t, d).Targets["system"]
	if sys.Kind != "nix-darwin" || sys.Rebuild != "sudo darwin-rebuild switch --flake /Users/me/.config/nix-darwin#mac" {
		t.Errorf("darwin target = %+v", sys)
	}
}

func TestDetectNothing(t *testing.T) {
	d := Detect(System{OS: "linux"}, channels, "")
	if len(d.Config.Targets) != 0 || !strings.Contains(d.TOML(), "# [targets.system]") {
		t.Errorf("expected an example config:\n%s", d.TOML())
	}
	if c := roundTrip(t, d); len(c.Targets) != 0 {
		t.Errorf("example should load as empty: %+v", c)
	}
}

func hostNames(hosts []FlakeHost) []string {
	var names []string
	for _, h := range hosts {
		names = append(names, h.Name)
	}
	return names
}

func TestScanHosts(t *testing.T) {
	cases := []struct {
		name, src, output string
		want              []string
	}{
		{"dotted, as in a bug report", `{
  inputs = { nixpkgs.url = "github:nixos/nixpkgs/nixos-26.05"; };
  outputs = { self, nixpkgs, ... }@inputs: {
    nixosConfigurations.shift = nixpkgs.lib.nixosSystem {
      system = "x86_64-linux";
      modules = [ ./hardware-configuration.nix ./configuration.nix ];
    };
  };
}`, "nixosConfigurations", []string{"shift"}},
		{"attribute block", `{ outputs = { nixpkgs, ... }: {
    nixosConfigurations = {
      laptop = nixpkgs.lib.nixosSystem { modules = [ { networking.hostName = "x"; } ]; };
      desktop = nixpkgs.lib.nixosSystem { modules = [ ./d.nix ]; };
      "odd-name" = mk ./o.nix;
    };
  }; }`, "nixosConfigurations", []string{"laptop", "desktop", "odd-name"}},
		{"quoted home-manager names", `{ outputs = _: {
    homeConfigurations."me@laptop" = hm.lib.homeManagerConfiguration { };
  }; }`, "homeConfigurations", []string{"me@laptop"}},
		{"comments are ignored", `{ outputs = _: {
    # nixosConfigurations.old = something;
    /* nixosConfigurations.older = other; */
    nixosConfigurations.current = x;
  }; }`, "nixosConfigurations", []string{"current"}},
		{"names computed by code", `{ outputs = _: {
    nixosConfigurations = nixpkgs.lib.genAttrs hosts mkHost;
  }; }`, "nixosConfigurations", []string{""}},
		{"output not present", `{ outputs = _: { packages = { }; }; }`, "nixosConfigurations", nil},
	}
	for _, c := range cases {
		if got := hostNames(scanHosts(c.src, c.output)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: scanHosts = %q, want %q", c.name, got, c.want)
		}
	}
}

// fakeNix replaces the nix command with a script.
func fakeNix(t *testing.T, body string) {
	t.Helper()
	script := filepath.Join(t.TempDir(), "nix")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	old := nixBin
	nixBin = script
	t.Cleanup(func() { nixBin = old })
}

func flakeDir(t *testing.T, flake string, git bool) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "flake.nix"), []byte(flake), 0o644)
	if git {
		if err := exec.Command("git", "-C", dir, "init", "-q").Run(); err != nil {
			t.Skip("git not available")
		}
	}
	return dir
}

const shiftFlake = `{ outputs = { nixpkgs, ... }: {
  nixosConfigurations.shift = nixpkgs.lib.nixosSystem { modules = [ ./configuration.nix ]; };
}; }`

func TestInspectFlakeCopiesTrackedFilesOnly(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	fakeNix(t, `printf '%s' "$*" >`+args+`
echo '{"nixos":[{"name":"shift","pkgsSet":false}],"darwin":[],"home":[]}'`)
	dir := flakeDir(t, shiftFlake, true)
	f := inspectFlake(dir)
	if f == nil || !reflect.DeepEqual(hostNames(f.NixOS), []string{"shift"}) {
		t.Fatalf("flake = %+v", f)
	}
	if f.EvalErr != "" || !f.GitRepo {
		t.Errorf("flake = %+v", f)
	}
	// Neither git+file (refused for repositories of another user) nor a
	// path: copy of the whole directory (ignored files would reach the store).
	got, _ := os.ReadFile(args)
	if strings.Contains(string(got), "git+file") || strings.Contains(string(got), `"path:`+dir) || !strings.Contains(string(got), "builtins.path") {
		t.Errorf("the flake should be copied with its tracked files only:\n%s", got)
	}
}

func TestInspectFlakeFallsBackToScanning(t *testing.T) {
	fakeNix(t, `echo "error: boom" >&2; exit 1`)
	dir := flakeDir(t, shiftFlake, true)
	f := inspectFlake(dir)
	if f == nil || !reflect.DeepEqual(hostNames(f.NixOS), []string{"shift"}) {
		t.Fatalf("host names should be read from flake.nix: %+v", f)
	}
	if !strings.Contains(f.EvalErr, "boom") {
		t.Errorf("EvalErr = %q", f.EvalErr)
	}

	// The rebuild command then names the host, which is what failed for the
	// user: nixos-rebuild guesses the machine's hostname otherwise.
	d := Detect(System{OS: "linux", NixOSVersion: "25.11.1", Sudo: "sudo", Hostname: "nixos", Flakes: []*Flake{f}}, channels, "")
	sys := roundTrip(t, d).Targets["system"]
	if sys.Flake != dir+"#shift" || !strings.HasSuffix(sys.Rebuild, "--flake "+dir+"#shift") {
		t.Errorf("rebuild must target the host: %+v", sys)
	}
}

func TestRebuildQuotesTheFlake(t *testing.T) {
	d := Detect(System{OS: "linux", NixOSVersion: "25.11.1", Sudo: "sudo",
		Flakes: []*Flake{{Dir: "/home/jane doe/nixos", NixOS: []FlakeHost{{Name: "it's"}}}}}, channels, "")
	sys := roundTrip(t, d).Targets["system"]
	if want := `sudo nixos-rebuild switch --flake '/home/jane doe/nixos#it'\''s'`; sys.Rebuild != want {
		t.Errorf("rebuild = %s, want %s", sys.Rebuild, want)
	}
	if sys.Flake != "/home/jane doe/nixos#it's" {
		t.Errorf("flake = %q is not a shell word", sys.Flake)
	}
	out, err := exec.Command("sh", "-c", "printf %s "+strings.TrimPrefix(sys.Rebuild, "sudo nixos-rebuild switch --flake ")).Output()
	if err != nil || string(out) != "/home/jane doe/nixos#it's" {
		t.Errorf("the shell reads %q, %v", out, err)
	}
}

func TestDetectWarnsWhenHostNameIsUnknown(t *testing.T) {
	d := Detect(System{OS: "linux", NixOSVersion: "25.11.1", Sudo: "sudo",
		Flakes: []*Flake{{Dir: "/etc/nixos", NixOS: []FlakeHost{{}}}}}, channels, "")
	if toml := d.TOML(); !strings.Contains(toml, "could not find the name of the configuration") || !strings.Contains(toml, "--flake /etc/nixos#NAME") {
		t.Errorf("the missing host name should be explained:\n%s", toml)
	}
}
