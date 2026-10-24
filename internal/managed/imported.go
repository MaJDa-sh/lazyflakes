package managed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
)

var flakeOutputs = map[string]string{
	"nixos":        "nixosConfigurations",
	"nix-darwin":   "darwinConfigurations",
	"home-manager": "homeConfigurations",
}

// CheckImported asks Nix which files define the target's package option in
// the evaluated configuration and reports whether the module is among them.
// It needs the target's flake output, or for channel-based NixOS the
// nixos-config from NIX_PATH to be the configuration the module lives in.
func CheckImported(ctx context.Context, t *config.Target) (bool, error) {
	if _, err := os.Stat(t.File); err != nil {
		return false, nil // a module that does not exist yet cannot be imported
	}

	var cmd *exec.Cmd
	var matches func(file string) bool
	switch {
	case t.Flake != "":
		dir, name, _ := strings.Cut(t.Flake, "#")
		rel, err := filepath.Rel(dir, t.File)
		if err != nil || strings.HasPrefix(rel, "..") {
			return false, fmt.Errorf("%s is outside the flake %s", t.File, dir)
		}
		installable := fmt.Sprintf("%s#%s.%q.options.%s.files", dir, flakeOutputs[t.Kind], name, t.Attribute)
		cmd = exec.CommandContext(ctx, "nix", "--extra-experimental-features", "nix-command flakes", "eval", "--json", installable)
		// Flakes are evaluated from a store copy: /nix/store/…-source/<rel>.
		matches = func(file string) bool { return strings.HasSuffix(file, "-source/"+rel) }
	case t.Kind == "nixos" && filepath.Dir(nixosConfig()) == filepath.Dir(t.File):
		expr := fmt.Sprintf("(import <nixpkgs/nixos> { }).options.%s.files", t.Attribute)
		cmd = exec.CommandContext(ctx, "nix-instantiate", "--eval", "--strict", "--json", "-E", expr)
		matches = func(file string) bool { return file == t.File }
	default:
		return false, errors.New("no way to evaluate this configuration")
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var files []string
	if err := json.Unmarshal(out, &files); err != nil {
		return false, err
	}
	for _, f := range files {
		if matches(f) {
			return true, nil
		}
	}
	return false, nil
}

// IsImported is CheckImported, falling back to the text search in Imported
// when the configuration cannot be evaluated.
func IsImported(ctx context.Context, t *config.Target) bool {
	ok, err := CheckImported(ctx, t)
	if err != nil {
		return Imported(t)
	}
	return ok
}

// nixosConfig is the configuration.nix NIX_PATH points nixos-rebuild at.
func nixosConfig() string {
	for _, entry := range strings.Split(os.Getenv("NIX_PATH"), ":") {
		if p, ok := strings.CutPrefix(entry, "nixos-config="); ok {
			return p
		}
	}
	return "/etc/nixos/configuration.nix"
}
