package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/MaJDa-sh/lazyflakes/internal/generate"
)

// System is what Detect needs to know about the machine. Inspect fills it in;
// tests construct it directly.
type System struct {
	OS                string // runtime.GOOS
	Home              string
	NixOSVersion      string // `nixos-version` output, "" when not NixOS
	NixOSConfig       string // path of configuration.nix, "" when none
	FlakeDir          string // directory of a flake with nixos/darwinConfigurations
	HomeManagerDir    string // standalone home-manager config dir, "" when none
	HomeManagerFlake  bool   // HomeManagerDir contains a flake.nix
	HomeManagerVer    string // `home-manager --version` output
	HasDarwinRebuild  bool
	FlakeDirIsGitRepo bool
	NixSystem         string // e.g. "x86_64-linux"
}

// flakeDirs are the usual places people keep a system flake.
var flakeDirs = []string{"/etc/nixos", "~/nixos-config", "~/.dotfiles", "~/dotfiles", "~/nixos", "~/.config/nixos", "~/.nixos", "~/.config/nix-darwin"}

// Inspect looks at the running machine.
func Inspect() System {
	sys := System{OS: runtime.GOOS, NixSystem: nixSystem(runtime.GOOS, runtime.GOARCH)}
	sys.Home, _ = os.UserHomeDir()
	sys.NixOSVersion = output("nixos-version")
	_, err := exec.LookPath("darwin-rebuild")
	sys.HasDarwinRebuild = err == nil

	if sys.NixOSVersion != "" {
		sys.NixOSConfig = nixosConfigPath()
	}
	for _, d := range flakeDirs {
		d = ExpandHome(d)
		data, err := os.ReadFile(filepath.Join(d, "flake.nix"))
		if err == nil && (strings.Contains(string(data), "nixosConfigurations") || strings.Contains(string(data), "darwinConfigurations")) {
			sys.FlakeDir = d
			sys.FlakeDirIsGitRepo = exec.Command("git", "-C", d, "rev-parse").Run() == nil
			break
		}
	}
	if _, err := exec.LookPath("home-manager"); err == nil {
		d := ExpandHome("~/.config/home-manager")
		if _, err := os.Stat(d); err == nil {
			sys.HomeManagerDir = d
			_, err := os.Stat(filepath.Join(d, "flake.nix"))
			sys.HomeManagerFlake = err == nil
			sys.HomeManagerVer = output("home-manager", "--version")
		}
	}
	return sys
}

// nixosConfigPath finds configuration.nix via NIX_PATH or the default path.
func nixosConfigPath() string {
	for _, entry := range strings.Split(os.Getenv("NIX_PATH"), ":") {
		if p, ok := strings.CutPrefix(entry, "nixos-config="); ok {
			return p
		}
	}
	if _, err := os.Stat("/etc/nixos/configuration.nix"); err == nil {
		return "/etc/nixos/configuration.nix"
	}
	return ""
}

func nixSystem(goos, goarch string) string {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch]
	if arch == "" {
		arch = goarch
	}
	return arch + "-" + goos
}

func output(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Detected is a proposed config plus notes explaining it to the user.
type Detected struct {
	Config *Config
	Notes  []string // shown by `lazyflakes setup` and written as comments
	// Hints are comment lines per target name, for channels that need
	// extra wiring in the user's own config before they can be used.
	Hints map[string][]string
}

var versionRe = regexp.MustCompile(`^(\d\d\.\d\d)(pre)?`)

// releaseChannel maps a version like "25.11.9586…" or "26.05pre…" to the
// channel it follows.
func releaseChannel(version string) (channel, release string) {
	m := versionRe.FindStringSubmatch(version)
	if m == nil {
		return "unstable", ""
	}
	if m[2] != "" {
		return "unstable", m[1]
	}
	return "stable", m[1]
}

// Detect proposes targets for the machine. Channels define the refs that
// non-system package sets are fetched from.
func Detect(sys System, channels []generate.Channel) Detected {
	d := Detected{Config: &Config{Targets: map[string]*Target{}}, Hints: map[string][]string{}}

	switch {
	case sys.NixOSVersion != "" && sys.FlakeDir != "":
		own, release := releaseChannel(sys.NixOSVersion)
		t := &Target{
			Name: "system", Kind: "nixos", File: filepath.Join(sys.FlakeDir, "lazyflakes.nix"),
			Rebuild: "sudo nixos-rebuild switch --flake " + sys.FlakeDir,
		}
		d.addPureSets(t, own, channels, "nixpkgs.lib.nixosSystem", sys.NixSystem)
		d.add(t)
		d.Notes = append(d.Notes, fmt.Sprintf("NixOS %s with a flake in %s", release, sys.FlakeDir))
		d.noteRelease(own, release)
		if sys.FlakeDirIsGitRepo {
			d.Notes = append(d.Notes, "the flake is a git repo: lazyflakes.nix is git-added (intent-to-add) when first written")
		}
	case sys.NixOSVersion != "" && sys.NixOSConfig != "":
		own, release := releaseChannel(sys.NixOSVersion)
		t := &Target{
			Name: "system", Kind: "nixos", File: filepath.Join(filepath.Dir(sys.NixOSConfig), "lazyflakes.nix"),
			Rebuild: "sudo nixos-rebuild switch",
		}
		addImpureSets(t, own, channels)
		d.add(t)
		d.Notes = append(d.Notes, fmt.Sprintf("NixOS %s configured by %s (channels)", release, sys.NixOSConfig))
		d.noteRelease(own, release)
	case sys.OS == "darwin" && sys.HasDarwinRebuild && sys.FlakeDir != "":
		t := &Target{
			Name: "system", Kind: "nix-darwin", File: filepath.Join(sys.FlakeDir, "lazyflakes.nix"),
			Rebuild: "darwin-rebuild switch --flake " + sys.FlakeDir,
		}
		d.addPureSets(t, "unstable", channels, "nix-darwin.lib.darwinSystem", sys.NixSystem)
		d.add(t)
		d.Notes = append(d.Notes, "nix-darwin with a flake in "+sys.FlakeDir)
	}

	if sys.HomeManagerDir != "" {
		own, _ := releaseChannel(sys.HomeManagerVer)
		t := &Target{Name: "home", Kind: "home-manager", File: filepath.Join(sys.HomeManagerDir, "lazyflakes.nix"), Rebuild: "home-manager switch"}
		if sys.HomeManagerFlake {
			t.Rebuild += " --flake " + sys.HomeManagerDir
			d.addPureSets(t, own, channels, "home-manager.lib.homeManagerConfiguration", sys.NixSystem)
		} else {
			addImpureSets(t, own, channels)
		}
		d.add(t)
		d.Notes = append(d.Notes, "standalone home-manager in "+sys.HomeManagerDir)
	}

	if len(d.Config.Targets) == 0 {
		d.Notes = append(d.Notes, "no NixOS, nix-darwin or home-manager configuration found; edit the example below")
	}
	return d
}

func (d *Detected) add(t *Target) {
	if err := t.normalize(); err != nil {
		panic(err) // Detect only builds valid targets
	}
	d.Config.Targets[t.Name] = t
	if d.Config.Default == "" {
		d.Config.Default = t.Name
	}
}

// noteRelease explains that "stable" means the system's own release, which
// may be older than the stable channel lazyflakes browses.
func (d *Detected) noteRelease(own, release string) {
	if own == "stable" && release != "" {
		d.Notes = append(d.Notes, fmt.Sprintf(`"stable" packages are installed from your system's own nixos-%s`, release))
	}
}

// addImpureSets maps the config's own channel to pkgs and fetches the others
// with fetchTarball, which non-flake configurations allow.
func addImpureSets(t *Target, own string, channels []generate.Channel) {
	t.Sets = map[string]string{}
	for _, c := range channels {
		if c.Name == own {
			t.Sets[c.Name] = "pkgs"
			continue
		}
		if url, ok := generate.TarballURL(c.Ref); ok {
			t.Sets[c.Name] = fmt.Sprintf("import (fetchTarball %q) { inherit (pkgs) config; system = pkgs.stdenv.hostPlatform.system; }", url)
		}
	}
}

// addPureSets maps the config's own channel to pkgs. Pure flake evaluation
// cannot fetch other channels, so for those it leaves a hint on how to pass
// them in from the user's flake.
func (d *Detected) addPureSets(t *Target, own string, channels []generate.Channel, builder, system string) {
	t.Sets = map[string]string{own: "pkgs"}
	for _, c := range channels {
		if c.Name == own {
			continue
		}
		v := "pkgs-" + c.Name
		d.Hints[t.Name] = append(d.Hints[t.Name],
			fmt.Sprintf("To install from %s too, add an input to your flake and pass it to your modules:", c.Name),
			fmt.Sprintf("  inputs.nixpkgs-%s.url = %q;", c.Name, c.Ref),
			fmt.Sprintf("  %s { specialArgs = { %s = import nixpkgs-%s { system = %q; config.allowUnfree = true; }; }; … };", builder, v, c.Name, system),
			"then uncomment:",
			fmt.Sprintf("%s = %q", c.Name, v),
		)
	}
}

// TOML renders the detected config with comments explaining it.
func (d Detected) TOML() string {
	var b strings.Builder
	b.WriteString(`# lazyflakes configuration, generated by "lazyflakes setup".
#
# Each target is a Nix module that lazyflakes owns and rewrites ("file"),
# plus the command that applies it ("rebuild"). Import the file once from
# your own configuration; lazyflakes never edits any other file.
#
# "sets" says how each channel's packages are reached inside that module:
# a module argument such as pkgs (or one passed via specialArgs), or any
# Nix expression. Packages from a channel without a set cannot be added.
`)
	if len(d.Notes) > 0 {
		b.WriteString("#\n# Detected:\n")
		for _, n := range d.Notes {
			b.WriteString("#   " + n + "\n")
		}
	}

	if len(d.Config.Targets) == 0 {
		b.WriteString(`
# Example:
#
# default = "system"
#
# [targets.system]
# kind = "nixos"                       # nixos, nix-darwin or home-manager
# file = "/etc/nixos/lazyflakes.nix"
# rebuild = "sudo nixos-rebuild switch"
#
# [targets.system.sets]
# stable = "pkgs"
`)
		return b.String()
	}

	fmt.Fprintf(&b, "\ndefault = %s\n", tomlString(d.Config.Default))
	for _, t := range d.Config.Ordered() {
		fmt.Fprintf(&b, "\n[targets.%s]\n", t.Name)
		fmt.Fprintf(&b, "kind = %s\n", tomlString(t.Kind))
		fmt.Fprintf(&b, "file = %s\n", tomlString(t.File))
		fmt.Fprintf(&b, "rebuild = %s\n", tomlString(t.Rebuild))
		fmt.Fprintf(&b, "# attribute = %s\n", tomlString(t.Attribute))
		fmt.Fprintf(&b, "\n[targets.%s.sets]\n", t.Name)
		names := make([]string, 0, len(t.Sets))
		for n := range t.Sets {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(&b, "%s = %s\n", n, tomlString(t.Sets[n]))
		}
		for _, h := range d.Hints[t.Name] {
			b.WriteString("# " + h + "\n")
		}
	}
	return b.String()
}

// tomlString quotes s as a TOML literal string when possible, which keeps
// Nix expressions with double quotes readable.
func tomlString(s string) string {
	if !strings.ContainsAny(s, "'\n") {
		return "'" + s + "'"
	}
	return strconv.Quote(s)
}
