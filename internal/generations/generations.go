// Package generations lists the generations of a target's system or home
// configuration, compares them, and rolls back to one: the profile, the
// running system and lazyflakes.nix together.
package generations

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/managed"
)

// Generation is one generation of a profile.
type Generation struct {
	Number  int
	Link    string // the profile's link to it, e.g. /nix/var/nix/profiles/system-66-link
	Date    time.Time
	Version string // the NixOS or nix-darwin version, "" when unknown
	Current bool
	// Record is what lazyflakes had installed and set in it, nil for a
	// generation built before lazyflakes kept records.
	Record *managed.Module
}

// Where the profiles are; tests replace them.
var (
	systemProfile = "/nix/var/nix/profiles/system"
	homeProfiles  = func() []string {
		var dirs []string
		if state := os.Getenv("XDG_STATE_HOME"); state != "" {
			dirs = append(dirs, filepath.Join(state, "nix", "profiles", "home-manager"))
		}
		if home, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, filepath.Join(home, ".local", "state", "nix", "profiles", "home-manager"))
		}
		return append(dirs, filepath.Join("/nix/var/nix/profiles/per-user", os.Getenv("USER"), "home-manager"))
	}
)

// Profile is the Nix profile holding target t's generations.
func Profile(t *config.Target) (string, error) {
	switch t.Kind {
	case "nixos", "nix-darwin":
		return systemProfile, nil
	case "home-manager":
		for _, p := range homeProfiles() {
			if _, err := os.Lstat(p); err == nil {
				return p, nil
			}
		}
		return "", errors.New("no home-manager profile found: switch to a configuration once first")
	}
	return "", fmt.Errorf("target %q runs a script, which has no generations", t.Name)
}

// List returns the generations of target t, newest first.
func List(t *config.Target) ([]Generation, error) {
	profile, err := Profile(t)
	if err != nil {
		return nil, err
	}
	current, _ := os.Readlink(profile)
	links, err := filepath.Glob(profile + "-*-link")
	if err != nil {
		return nil, err
	}
	var gens []Generation
	for _, link := range links {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(link, profile+"-"), "-link"))
		if err != nil {
			continue // another profile whose name starts the same, e.g. system-foo
		}
		info, err := os.Lstat(link)
		if err != nil {
			continue
		}
		g := Generation{Number: n, Link: link, Date: info.ModTime(), Current: filepath.Base(link) == filepath.Base(current)}
		for _, f := range []string{"nixos-version", "darwin-version"} {
			if v, err := os.ReadFile(filepath.Join(link, f)); err == nil {
				g.Version = strings.TrimSpace(string(v))
				break
			}
		}
		if rec, ok, err := managed.ReadRecord(t, link); err == nil && ok {
			g.Record = &rec
		}
		gens = append(gens, g)
	}
	slices.SortFunc(gens, func(a, b Generation) int { return b.Number - a.Number })
	return gens, nil
}

// CurrentOf is the current generation in gens.
func CurrentOf(gens []Generation) (Generation, bool) {
	i := slices.IndexFunc(gens, func(g Generation) bool { return g.Current })
	if i < 0 {
		return Generation{}, false
	}
	return gens[i], true
}

// Diff lists what changes from one generation to another, one line per
// package, as `nix store diff-closures` reports it, e.g.
// "firefox: 131.0 → 132.0, +2.1 MiB".
func Diff(ctx context.Context, from, to string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "nix", "--extra-experimental-features", "nix-command", "store", "diff-closures", from, to)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("nix store diff-closures: %w: %s", err, strings.TrimSpace(ansi.Strip(string(out))))
	}
	var lines []string
	sc := bufio.NewScanner(strings.NewReader(ansi.Strip(string(out))))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

// Changes describes how lazyflakes' packages, options and settings differ
// from one record to another, e.g. "+htop", "−jq",
// "services.openssh.enable = false (was true)".
func Changes(from, to managed.Module) []string {
	var out []string
	for _, e := range to.Packages {
		i := slices.IndexFunc(from.Packages, func(f managed.Entry) bool { return f.Attr == e.Attr })
		switch {
		case i < 0:
			out = append(out, "+"+e.Attr)
		case from.Packages[i].Channel != e.Channel:
			out = append(out, fmt.Sprintf("~%s (%s → %s)", e.Attr, from.Packages[i].Channel, e.Channel))
		}
	}
	for _, e := range from.Packages {
		if !slices.ContainsFunc(to.Packages, func(t managed.Entry) bool { return t.Attr == e.Attr }) {
			out = append(out, "−"+e.Attr)
		}
	}
	for _, s := range to.Settings {
		i := slices.IndexFunc(from.Settings, func(f managed.Setting) bool { return f.Path == s.Path })
		switch {
		case i < 0:
			out = append(out, fmt.Sprintf("+%s = %s", s.Path, s.Value))
		case from.Settings[i].Value != s.Value:
			out = append(out, fmt.Sprintf("~%s = %s (was %s)", s.Path, s.Value, from.Settings[i].Value))
		}
	}
	for _, s := range from.Settings {
		if !slices.ContainsFunc(to.Settings, func(t managed.Setting) bool { return t.Path == s.Path }) {
			out = append(out, "−"+s.Path)
		}
	}
	if from.Options != to.Options {
		keys := strings.Join(to.Options.Keys(), ", ")
		if keys == "" {
			keys = "none"
		}
		out = append(out, "allow: "+keys)
	}
	return out
}

var numberRe = regexp.MustCompile(`^\d+$`)

// Find picks the generation to roll back to: the one numbered arg, or with
// arg empty the newest one older than the current generation.
func Find(gens []Generation, arg string) (Generation, error) {
	cur, ok := CurrentOf(gens)
	if !ok {
		return Generation{}, errors.New("cannot tell which generation is the current one")
	}
	if arg == "" {
		for _, g := range gens {
			if g.Number < cur.Number {
				return g, nil
			}
		}
		return Generation{}, fmt.Errorf("generation %d is the oldest there is", cur.Number)
	}
	if !numberRe.MatchString(arg) {
		return Generation{}, fmt.Errorf("%q is not a generation number", arg)
	}
	n, _ := strconv.Atoi(arg)
	i := slices.IndexFunc(gens, func(g Generation) bool { return g.Number == n })
	switch {
	case i < 0:
		return Generation{}, fmt.Errorf("there is no generation %d (it may have been garbage-collected)", n)
	case gens[i].Current:
		return Generation{}, fmt.Errorf("generation %d is the current one already", n)
	}
	return gens[i], nil
}
