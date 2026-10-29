package options

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
)

func TestSourceFor(t *testing.T) {
	env := Env{HomeManagerRelease: "25.11", FallbackNixpkgs: "github:NixOS/nixpkgs/nixos-26.05"}
	joined := func(target *config.Target, env Env) string {
		t.Helper()
		src, err := SourceFor(target, env)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(src.Args, " ")
	}

	channels := joined(&config.Target{Kind: "nixos"}, env)
	for _, want := range []string{"builtins.tryEval <nixpkgs>", `getFlake "github:NixOS/nixpkgs/nixos-26.05"`, "config.system.build.manual.optionsJSON"} {
		if !strings.Contains(channels, want) {
			t.Errorf("channel-based NixOS lacks %q:\n%s", want, channels)
		}
	}

	flake := joined(&config.Target{Kind: "nixos", Flake: "/etc/nixos#laptop"}, env)
	if !strings.Contains(flake, `(builtins.getFlake "path:/etc/nixos").inputs.nixpkgs.outPath or`) || strings.Contains(flake, "tryEval") {
		t.Errorf("flake NixOS should use the flake's own nixpkgs:\n%s", flake)
	}

	if got := joined(&config.Target{Kind: "home-manager"}, env); got != "github:nix-community/home-manager/release-25.11#docs-json" {
		t.Errorf("home-manager = %q", got)
	}
	if got := joined(&config.Target{Kind: "home-manager"}, Env{}); got != "github:nix-community/home-manager#docs-json" {
		t.Errorf("unreleased home-manager = %q", got)
	}
	if got := joined(&config.Target{Kind: "home-manager", Flake: "/h#me"}, env); !strings.Contains(got, `inputs.home-manager.packages.${builtins.currentSystem}.docs-json`) {
		t.Errorf("home-manager flake = %q", got)
	}
	if got := joined(&config.Target{Kind: "nix-darwin"}, env); got != "github:nix-darwin/nix-darwin#optionsJSON" {
		t.Errorf("nix-darwin = %q", got)
	}
	if got := joined(&config.Target{Kind: "nix-darwin", Flake: "/d#mac"}, env); !strings.Contains(got, "i.nix-darwin or i.darwin") {
		t.Errorf("nix-darwin flake = %q", got)
	}

	// An override wins over everything, and scripts need one.
	if got := joined(&config.Target{Kind: "nixos", OptionsSource: "/x#y.optionsJSON"}, env); got != "/x#y.optionsJSON" {
		t.Errorf("override = %q", got)
	}
	if _, err := SourceFor(&config.Target{Name: "mine", Kind: config.KindScript}, env); err == nil || !strings.Contains(err.Error(), "options_source") {
		t.Errorf("a script target without options_source: err = %v", err)
	}
}

// fakeNix replaces nix with a script and restores it afterwards.
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

func TestBuildAndLoad(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	os.MkdirAll(filepath.Join(out, "share", "doc", "nixos"), 0o755)
	fixture, _ := os.ReadFile("testdata/options.json")
	os.WriteFile(filepath.Join(out, "share", "doc", "nixos", "options.json"), fixture, 0o644)
	fakeNix(t, `printf '%s\n' "`+out+`"`)

	opts, err := Source{Label: "test", Args: []string{"x"}}.Load(context.Background())
	if err != nil || len(opts) == 0 {
		t.Fatalf("Load: %d options, %v", len(opts), err)
	}

	fakeNix(t, `echo "error: boom" >&2; exit 1`)
	if _, err := (Source{Label: "test"}).Build(context.Background()); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("a failing build should explain itself: %v", err)
	}
	fakeNix(t, `printf '/nonexistent\n'`)
	if _, err := (Source{Label: "test"}).Build(context.Background()); err == nil || !strings.Contains(err.Error(), "no options.json") {
		t.Errorf("err = %v", err)
	}
}

// With LAZYFLAKES_NIX_EVAL=1 the real thing: the options of this machine's
// nixpkgs, which come from the binary cache.
func TestRealNixOSOptions(t *testing.T) {
	if os.Getenv("LAZYFLAKES_NIX_EVAL") == "" {
		t.Skip("set LAZYFLAKES_NIX_EVAL=1 to build the NixOS options with nix")
	}
	src, err := SourceFor(&config.Target{Kind: "nixos"}, Env{})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := src.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Option{}
	for _, o := range opts {
		by[o.Name] = o
	}
	if len(opts) < 10000 {
		t.Errorf("only %d NixOS options", len(opts))
	}
	if o := by["services.openssh.enable"]; o.Kind != Bool || !o.Settable() || o.Description == "" {
		t.Errorf("services.openssh.enable = %+v", o)
	}
	if o := by["services.openssh.settings.PermitRootLogin"]; o.Kind != Enum || !o.Nullable || len(o.Choices) < 4 {
		t.Errorf("PermitRootLogin = %+v", o)
	}
	for name, o := range by {
		if o.Settable() && o.Kind == Enum && len(o.Choices) == 0 {
			t.Errorf("%s is a choice without choices: %q", name, o.Type)
		}
	}
}
