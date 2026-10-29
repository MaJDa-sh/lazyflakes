// Package config loads the user's description of where packages are
// installed system-wide: one or more targets, each either a Nix module file
// that lazyflakes manages and the command that applies it, or a script of
// the user's own that decides what installing means.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// KindScript is a target that runs the user's script instead of writing a
// module, for workflows lazyflakes cannot know.
const KindScript = "script"

// Kinds of module targets and the package option each one installs into.
var kindAttributes = map[string]string{
	"nixos":        "environment.systemPackages",
	"nix-darwin":   "environment.systemPackages",
	"home-manager": "home.packages",
}

// Target is a configuration lazyflakes can add packages to.
type Target struct {
	Name      string            `toml:"-"`
	Kind      string            `toml:"kind"`      // nixos, nix-darwin, home-manager or script
	File      string            `toml:"file"`      // the managed module; ~ is expanded. For scripts: where lazyflakes records what the script installed
	Script    string            `toml:"script"`    // script targets: command called as `script add|remove channel:attr...`
	Attribute string            `toml:"attribute"` // defaults to the kind's package list
	Rebuild   string            `toml:"rebuild"`   // shell command run after writing; empty to only write
	Sets      map[string]string `toml:"sets"`      // channel name -> Nix expression for its package set
	// Flake is the configuration's flake output, "dir#name", used to ask
	// Nix whether the module is imported. Empty for non-flake setups.
	Flake string `toml:"flake"`
	// NixpkgsConfig controls whether options such as allow unfree are
	// written as nixpkgs.config.*; turn it off when the configuration gets
	// its pkgs from elsewhere (nixpkgs.pkgs, readOnlyPkgs, useGlobalPkgs).
	NixpkgsConfig *bool `toml:"nixpkgs_config"`
	// OptionsSource overrides where the Options tab gets this target's
	// option list: a flake installable whose output holds an options.json,
	// e.g. "/etc/nixos#nixosConfigurations.laptop.config.system.build.manual.optionsJSON".
	OptionsSource string `toml:"options_source"`

	Sudo string `toml:"-"` // copied from Config.Sudo
}

// Config is the parsed config.toml.
type Config struct {
	Default string `toml:"default"`
	// Sudo is the command that writes files the user cannot, e.g. "doas".
	Sudo    string             `toml:"sudo"`
	Targets map[string]*Target `toml:"targets"`
}

// DefaultSudo is used when the config does not name a command.
const DefaultSudo = "sudo"

// IsScript reports whether the target runs a script instead of a module.
func (t *Target) IsScript() bool { return t.Kind == KindScript }

// HasChannel reports whether packages can be installed from the named
// channel: a module target needs a package set for it, a script decides
// for itself.
func (t *Target) HasChannel(name string) bool {
	if t.IsScript() {
		return true
	}
	_, ok := t.Sets[name]
	return ok
}

// StateDir is where lazyflakes records what scripts installed:
// $XDG_STATE_HOME/lazyflakes, or ~/.local/state/lazyflakes.
func StateDir() string {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "lazyflakes")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "lazyflakes")
	}
	return filepath.Join(home, ".local", "state", "lazyflakes")
}

// WritesNixpkgsConfig reports whether options go into nixpkgs.config.
func (t *Target) WritesNixpkgsConfig() bool {
	return t.NixpkgsConfig == nil || *t.NixpkgsConfig
}

// ErrNoConfig is returned by Load when the config file does not exist.
var ErrNoConfig = errors.New("no config file")

// Path is the default config location, ~/.config/lazyflakes/config.toml.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lazyflakes", "config.toml"), nil
}

// Load reads and validates the config at path.
func Load(path string) (*Config, error) {
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoConfig
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Sudo == "" {
		c.Sudo = DefaultSudo
	}
	for name, t := range c.Targets {
		t.Name = name
		t.Sudo = c.Sudo
		if err := t.normalize(); err != nil {
			return nil, fmt.Errorf("%s: target %q: %w", path, name, err)
		}
	}
	if c.Default != "" && c.Targets[c.Default] == nil {
		return nil, fmt.Errorf("%s: default target %q is not defined", path, c.Default)
	}
	return &c, nil
}

func (t *Target) normalize() error {
	if t.IsScript() {
		return t.normalizeScript()
	}
	attr, ok := kindAttributes[t.Kind]
	if !ok {
		return fmt.Errorf("unknown kind %q (want nixos, nix-darwin, home-manager or script)", t.Kind)
	}
	if t.Attribute == "" {
		t.Attribute = attr
	}
	if t.File == "" {
		return errors.New("file is required")
	}
	if t.Flake != "" && !strings.Contains(t.Flake, "#") {
		return fmt.Errorf("flake %q must name the configuration, e.g. %q", t.Flake, t.Flake+"#"+t.Name)
	}
	t.File = ExpandHome(t.File)
	if !filepath.IsAbs(t.File) {
		return fmt.Errorf("file %q must be an absolute path", t.File)
	}
	return nil
}

func (t *Target) normalizeScript() error {
	if strings.TrimSpace(t.Script) == "" {
		return errors.New(`kind "script" needs script = "…", the command to run`)
	}
	t.Script = ExpandHome(strings.TrimSpace(t.Script))
	if t.File == "" {
		t.File = filepath.Join(StateDir(), t.Name+".json")
	}
	t.File = ExpandHome(t.File)
	if !filepath.IsAbs(t.File) {
		return fmt.Errorf("file %q must be an absolute path", t.File)
	}
	return nil
}

// Ordered returns the targets with the default first, then by name.
func (c *Config) Ordered() []*Target {
	targets := make([]*Target, 0, len(c.Targets))
	for _, t := range c.Targets {
		targets = append(targets, t)
	}
	sort.Slice(targets, func(i, j int) bool {
		a, b := targets[i], targets[j]
		if (a.Name == c.Default) != (b.Name == c.Default) {
			return a.Name == c.Default
		}
		return a.Name < b.Name
	})
	return targets
}

// ExpandHome replaces a leading ~ with the home directory.
func ExpandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}
