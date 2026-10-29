package options

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
)

// Env says which release of the system the options are for.
type Env struct {
	NixOSRelease       string // e.g. "25.11"; "" for unstable
	HomeManagerRelease string
	// FallbackNixpkgs is a flake reference to use when the target's own
	// nixpkgs cannot be found.
	FallbackNixpkgs string
}

// Source says how to build an options.json for one target.
type Source struct {
	Label string
	// Args are the arguments of `nix build` that produce a directory
	// holding share/doc/<name>/options.json.
	Args []string
}

// nixBin is the nix command; tests replace it.
var nixBin = "nix"

// minimalNixOS is the smallest configuration that evaluates. The options
// document does not depend on it, so every user asks for the same
// derivation and gets it from the binary cache instead of building it.
const minimalNixOS = `{ fileSystems."/" = { device = "/dev/null"; fsType = "ext4"; }; boot.loader.grub.enable = false; system.stateVersion = "25.11"; }`

// SourceFor works out where the options of target t come from.
func SourceFor(t *config.Target, env Env) (Source, error) {
	if t.OptionsSource != "" {
		return Source{Label: t.OptionsSource, Args: []string{t.OptionsSource}}, nil
	}
	flake := flakeDir(t)
	switch t.Kind {
	case "nixos":
		return Source{Label: "NixOS options", Args: []string{"--expr", nixosExpr(flake, env)}}, nil
	case "home-manager":
		if flake != "" {
			return Source{Label: "home-manager options", Args: []string{"--expr",
				fmt.Sprintf(`(builtins.getFlake %q).inputs.home-manager.packages.${builtins.currentSystem}.docs-json`, "path:"+flake)}}, nil
		}
		ref := "github:nix-community/home-manager"
		if env.HomeManagerRelease != "" {
			ref += "/release-" + env.HomeManagerRelease
		}
		return Source{Label: "home-manager options", Args: []string{ref + "#docs-json"}}, nil
	case "nix-darwin":
		if flake != "" {
			return Source{Label: "nix-darwin options", Args: []string{"--expr",
				fmt.Sprintf(`let i = (builtins.getFlake %q).inputs; in (i.nix-darwin or i.darwin).packages.${builtins.currentSystem}.optionsJSON`, "path:"+flake)}}, nil
		}
		return Source{Label: "nix-darwin options", Args: []string{"github:nix-darwin/nix-darwin#optionsJSON"}}, nil
	}
	return Source{}, fmt.Errorf("target %q runs a script, so lazyflakes does not know which options it takes: set options_source in the config (a flake installable that builds an options.json)", t.Name)
}

// flakeDir is the directory of the target's configuration flake, or "".
func flakeDir(t *config.Target) string {
	dir, _, _ := strings.Cut(t.Flake, "#")
	return dir
}

// nixosExpr builds the options of the nixpkgs the target uses: the flake's
// own nixpkgs input when it has one, else <nixpkgs>, else the fallback.
func nixosExpr(flake string, env Env) string {
	fallback := env.FallbackNixpkgs
	if fallback == "" {
		fallback = "github:NixOS/nixpkgs/nixos-unstable"
	}
	var nixpkgs string
	if flake != "" {
		nixpkgs = fmt.Sprintf(`(builtins.getFlake %q).inputs.nixpkgs.outPath or (builtins.getFlake %q).outPath`, "path:"+flake, fallback)
	} else {
		nixpkgs = fmt.Sprintf(`(let t = builtins.tryEval <nixpkgs>; in if t.success then t.value else (builtins.getFlake %q).outPath)`, fallback)
	}
	return fmt.Sprintf(`let nixpkgs = %s; in (import (nixpkgs + "/nixos") { configuration = %s; }).config.system.build.manual.optionsJSON`, nixpkgs, minimalNixOS)
}

// Build builds the options file and returns the path of its options.json.
func (s Source) Build(ctx context.Context) (string, error) {
	args := []string{"--extra-experimental-features", "nix-command flakes",
		"build", "--impure", "--no-link", "--print-out-paths", "--no-write-lock-file"}
	cmd := exec.CommandContext(ctx, nixBin, append(args, s.Args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("building the %s: %w\n%s", s.Label, err, lastLines(stderr.String(), 4))
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		matches, _ := filepath.Glob(filepath.Join(strings.TrimSpace(line), "share", "doc", "*", "options.json"))
		if len(matches) > 0 {
			return matches[0], nil
		}
	}
	return "", errors.New("the build produced no options.json")
}

// Load builds the options and parses them.
func (s Source) Load(ctx context.Context) ([]Option, error) {
	path, err := s.Build(ctx)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Parse(f)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
