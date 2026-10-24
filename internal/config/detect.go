package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MaJDa-sh/lazyflakes/internal/generate"
)

// System is what Detect needs to know about the machine. Inspect fills it in;
// tests construct it directly.
type System struct {
	OS               string // runtime.GOOS
	Home             string
	User             string
	Hostname         string
	Sudo             string   // first available of sudo, doas and run0
	NixOSVersion     string   // `nixos-version` output, "" when not NixOS
	NixOSConfig      string   // path of configuration.nix, "" when none
	Flakes           []*Flake // configuration flakes, in FlakeDirs order
	HomeManagerDir   string   // standalone, non-flake home-manager config dir
	HomeManagerVer   string   // `home-manager --version` output
	HasHomeManager   bool     // the home-manager command is installed
	HasDarwinRebuild bool
	NixSystem        string // e.g. "x86_64-linux"
}

// Flake is a flake that defines system or home configurations.
type Flake struct {
	Dir     string
	GitRepo bool
	// Hosts per kind of configuration. A host with an empty name means the
	// flake could not be evaluated and was only recognised by its text.
	NixOS, Darwin, Home []FlakeHost
	EvalErr             string // why evaluation failed, when it did
}

// FlakeHost is one configuration in a flake.
type FlakeHost struct {
	Name string
	// PkgsSet means the configuration sets nixpkgs.pkgs (or uses
	// readOnlyPkgs), so nixpkgs.config would be ignored or rejected.
	PkgsSet bool
}

// FlakeDirs are the usual places people keep a configuration flake.
var FlakeDirs = []string{"/etc/nixos", "~/nixos-config", "~/.dotfiles", "~/dotfiles", "~/nixos", "~/.config/nixos", "~/.nixos", "~/.config/nix-darwin", "~/.config/home-manager"}

// Inspect looks at the running machine. flakeDir, when set, is the only
// directory searched for a configuration flake.
func Inspect(flakeDir string) System {
	sys := System{OS: runtime.GOOS, NixSystem: nixSystem(runtime.GOOS, runtime.GOARCH), Sudo: DefaultSudo}
	sys.Home, _ = os.UserHomeDir()
	sys.User = os.Getenv("USER")
	sys.Hostname, _ = os.Hostname()
	sys.NixOSVersion = output("nixos-version")
	sys.HasDarwinRebuild = installed("darwin-rebuild")
	sys.HasHomeManager = installed("home-manager")
	for _, s := range []string{"sudo", "doas", "run0"} {
		if installed(s) {
			sys.Sudo = s
			break
		}
	}
	if sys.NixOSVersion != "" {
		sys.NixOSConfig = nixosConfigPath()
	}

	dirs := FlakeDirs
	if flakeDir != "" {
		dirs = []string{flakeDir}
	}
	for _, d := range dirs {
		if f := inspectFlake(ExpandHome(d)); f != nil {
			sys.Flakes = append(sys.Flakes, f)
		}
	}

	if sys.HasHomeManager {
		d := ExpandHome("~/.config/home-manager")
		_, noFlake := os.Stat(filepath.Join(d, "flake.nix"))
		if _, err := os.Stat(d); err == nil && noFlake != nil {
			sys.HomeManagerDir = d
		}
		sys.HomeManagerVer = output("home-manager", "--version")
	}
	return sys
}

// inspectFlake reads the configurations a flake in dir defines, or returns
// nil when there is no such flake.
func inspectFlake(dir string) *Flake {
	src, err := os.ReadFile(filepath.Join(dir, "flake.nix"))
	if err != nil {
		return nil
	}
	f := &Flake{Dir: dir}
	ref := "path:" + dir
	if exec.Command("git", "-c", "safe.directory="+dir, "-C", dir, "rev-parse").Run() == nil {
		f.GitRepo = true
		ref = "git+file://" + dir
	}
	if err := evalFlake(f, ref); err != nil {
		// Fall back to recognising the outputs by name in flake.nix.
		f.EvalErr = err.Error()
		unknown := []FlakeHost{{}}
		if strings.Contains(string(src), "nixosConfigurations") {
			f.NixOS = unknown
		}
		if strings.Contains(string(src), "darwinConfigurations") {
			f.Darwin = unknown
		}
		if strings.Contains(string(src), "homeConfigurations") {
			f.Home = unknown
		}
	}
	if len(f.NixOS)+len(f.Darwin)+len(f.Home) == 0 {
		return nil
	}
	return f
}

// flakeExpr lists a flake's configurations. nixpkgs.pkgs counts as set when
// it is defined above option-default priority (1500).
const flakeExpr = `let
  f = builtins.getFlake %q;
  hosts = out: pkgsSet: if f ? ${out} then map (n: { name = n; pkgsSet = pkgsSet f.${out}.${n}; }) (builtins.attrNames f.${out}) else [ ];
  nixosPkgsSet = c: (c.options.nixpkgs.pkgs.highestPrio or 1500) < 1500;
in {
  nixos = hosts "nixosConfigurations" nixosPkgsSet;
  darwin = hosts "darwinConfigurations" (c: false);
  home = hosts "homeConfigurations" (c: false);
}`

func evalFlake(f *Flake, ref string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "nix", "--extra-experimental-features", "nix-command flakes",
		"eval", "--impure", "--json", "--expr", fmt.Sprintf(flakeExpr, ref))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("%w: %s", err, lastLine(stderr.String()))
	}
	var res struct{ NixOS, Darwin, Home []FlakeHost }
	if err := json.Unmarshal(out, &res); err != nil {
		return err
	}
	f.NixOS, f.Darwin, f.Home = res.NixOS, res.Darwin, res.Home
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func installed(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
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
// non-system package sets are fetched from; host, when set, picks the
// flake configuration to use.
func Detect(sys System, channels []generate.Channel, host string) Detected {
	d := Detected{Config: &Config{Targets: map[string]*Target{}, Sudo: sys.Sudo}, Hints: map[string][]string{}}
	if d.Config.Sudo == "" {
		d.Config.Sudo = DefaultSudo
	}
	sudo := d.Config.Sudo + " "
	systemFile := "lazyflakes.nix"

	switch f := sys.flakeWith(func(f *Flake) []FlakeHost { return f.NixOS }); {
	case sys.NixOSVersion != "" && f != nil:
		own, release := releaseChannel(sys.NixOSVersion)
		h := d.pickHost(f.NixOS, host, "NixOS", sys.Hostname)
		if len(f.NixOS) > 1 {
			systemFile = "lazyflakes-" + h.Name + ".nix"
		}
		t := &Target{Name: "system", Kind: "nixos", File: filepath.Join(f.Dir, systemFile)}
		t.Flake, t.Rebuild = flakeOutput(f.Dir, h.Name), sudo+"nixos-rebuild switch --flake "+flakeRef(f.Dir, h.Name)
		d.addPureSets(t, own, channels, "nixpkgs.lib.nixosSystem", sys.NixSystem)
		if h.PkgsSet {
			off := false
			t.NixpkgsConfig = &off
			d.Notes = append(d.Notes, fmt.Sprintf("%s sets nixpkgs.pkgs itself, so options like allow unfree are not written (nixpkgs_config = false)", h.Name))
		}
		d.add(t)
		d.Notes = append(d.Notes, fmt.Sprintf("NixOS %s with a flake in %s", release, f.Dir))
		d.noteRelease(own, release)
		d.noteFlake(f)
	case sys.NixOSVersion != "" && sys.NixOSConfig != "":
		own, release := releaseChannel(sys.NixOSVersion)
		t := &Target{
			Name: "system", Kind: "nixos", File: filepath.Join(filepath.Dir(sys.NixOSConfig), "lazyflakes.nix"),
			Rebuild: sudo + "nixos-rebuild switch",
		}
		addImpureSets(t, own, channels)
		d.add(t)
		d.Notes = append(d.Notes, fmt.Sprintf("NixOS %s configured by %s (channels)", release, sys.NixOSConfig))
		d.noteRelease(own, release)
	case sys.OS == "darwin" && sys.HasDarwinRebuild && sys.flakeWith(func(f *Flake) []FlakeHost { return f.Darwin }) != nil:
		f := sys.flakeWith(func(f *Flake) []FlakeHost { return f.Darwin })
		h := d.pickHost(f.Darwin, host, "nix-darwin", sys.Hostname)
		if len(f.Darwin) > 1 {
			systemFile = "lazyflakes-" + h.Name + ".nix"
		}
		t := &Target{Name: "system", Kind: "nix-darwin", File: filepath.Join(f.Dir, systemFile)}
		// Recent nix-darwin activates as root.
		t.Flake, t.Rebuild = flakeOutput(f.Dir, h.Name), sudo+"darwin-rebuild switch --flake "+flakeRef(f.Dir, h.Name)
		d.addPureSets(t, "unstable", channels, "nix-darwin.lib.darwinSystem", sys.NixSystem)
		d.add(t)
		d.Notes = append(d.Notes, "nix-darwin with a flake in "+f.Dir)
		d.noteFlake(f)
	}

	switch f := sys.flakeWith(func(f *Flake) []FlakeHost { return f.Home }); {
	case f != nil:
		own, _ := releaseChannel(sys.HomeManagerVer)
		h := d.pickHost(f.Home, "", "home-manager", sys.User+"@"+sys.Hostname, sys.User)
		file := "lazyflakes.nix"
		if d.Config.Targets["system"] != nil && filepath.Dir(d.Config.Targets["system"].File) == f.Dir {
			file = "lazyflakes-home.nix" // the system module lives next to it
		}
		hm := "home-manager"
		if !sys.HasHomeManager {
			hm = "nix run github:nix-community/home-manager --"
		}
		t := &Target{Name: "home", Kind: "home-manager", File: filepath.Join(f.Dir, file)}
		t.Flake, t.Rebuild = flakeOutput(f.Dir, h.Name), hm+" switch --flake "+flakeRef(f.Dir, h.Name)
		d.addPureSets(t, own, channels, "home-manager.lib.homeManagerConfiguration", sys.NixSystem)
		d.add(t)
		d.Notes = append(d.Notes, "home-manager configurations in the flake in "+f.Dir)
		if d.Config.Targets["system"] == nil {
			d.noteFlake(f)
		}
	case sys.HomeManagerDir != "":
		own, _ := releaseChannel(sys.HomeManagerVer)
		t := &Target{Name: "home", Kind: "home-manager", File: filepath.Join(sys.HomeManagerDir, "lazyflakes.nix"), Rebuild: "home-manager switch"}
		addImpureSets(t, own, channels)
		d.add(t)
		d.Notes = append(d.Notes, "standalone home-manager in "+sys.HomeManagerDir)
	}

	if len(d.Config.Targets) == 0 {
		d.Notes = append(d.Notes, "no NixOS, nix-darwin or home-manager configuration found; edit the example below")
	}
	return d
}

// flakeWith returns the first flake that has hosts of the given kind.
func (sys System) flakeWith(hosts func(*Flake) []FlakeHost) *Flake {
	for _, f := range sys.Flakes {
		if len(hosts(f)) > 0 {
			return f
		}
	}
	return nil
}

// pickHost chooses the configuration to manage: the one named by want,
// else the first matching one of the preferred names, else the only one,
// else the first.
func (d *Detected) pickHost(hosts []FlakeHost, want, kind string, preferred ...string) FlakeHost {
	names := make([]string, len(hosts))
	for i, h := range hosts {
		names[i] = h.Name
	}
	for _, name := range append([]string{want}, preferred...) {
		for _, h := range hosts {
			if name != "" && h.Name == name {
				return h
			}
		}
	}
	if want != "" {
		d.Notes = append(d.Notes, fmt.Sprintf("%s configuration %q not found (have %s)", kind, want, strings.Join(names, ", ")))
	}
	if len(hosts) > 1 {
		d.Notes = append(d.Notes, fmt.Sprintf("several %s configurations (%s): using %q, run setup -host NAME for another", kind, strings.Join(names, ", "), hosts[0].Name))
	}
	return hosts[0]
}

// flakeRef is dir#name, or just dir when the name is unknown.
func flakeRef(dir, name string) string {
	if name == "" {
		return dir
	}
	return dir + "#" + name
}

// flakeOutput is the Target.Flake value: dir#name, or "" when unknown.
func flakeOutput(dir, name string) string {
	if name == "" {
		return ""
	}
	return dir + "#" + name
}

func (d *Detected) noteFlake(f *Flake) {
	if f.EvalErr != "" {
		d.Notes = append(d.Notes, "could not evaluate the flake ("+f.EvalErr+"); recognised it by its text instead")
	}
	if f.GitRepo {
		d.Notes = append(d.Notes, "the flake is a git repo: the module is git-added (intent-to-add) when first written")
	}
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
	fmt.Fprintf(&b, "sudo = %s # used to write root-owned files: sudo, doas, run0, …\n", tomlString(d.Config.Sudo))
	for _, t := range d.Config.Ordered() {
		fmt.Fprintf(&b, "\n[targets.%s]\n", t.Name)
		fmt.Fprintf(&b, "kind = %s\n", tomlString(t.Kind))
		fmt.Fprintf(&b, "file = %s\n", tomlString(t.File))
		fmt.Fprintf(&b, "rebuild = %s\n", tomlString(t.Rebuild))
		if t.Flake != "" {
			fmt.Fprintf(&b, "flake = %s\n", tomlString(t.Flake))
		}
		if !t.WritesNixpkgsConfig() {
			b.WriteString("nixpkgs_config = false # the configuration sets its own pkgs\n")
		}
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
