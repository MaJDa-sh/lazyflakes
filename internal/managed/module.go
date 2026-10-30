// Package managed renders, parses and applies the Nix module that lazyflakes
// owns inside a user's system configuration. lazyflakes never edits any
// other file: the user imports this module once and the rest is generated.
package managed

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/generate"
)

// Entry is a package installed through the module.
type Entry struct {
	Channel string // channel name, e.g. "stable"
	Attr    string
}

// Setting is a module option set through lazyflakes, such as
// services.openssh.enable = true.
type Setting struct {
	Path  string // the attribute path, with quoted segments where needed
	Value string // a one-line Nix expression
}

// Module is the content of a managed file.
type Module struct {
	Packages []Entry
	Options  generate.Options // nixpkgs.config options such as allow unfree
	Settings []Setting
}

// Markers at the end of generated lines let Parse read the module back
// without evaluating any Nix.
const (
	pkgMarker     = "# lazyflakes:pkg "
	optionMarker  = "# lazyflakes:option "
	settingMarker = "# lazyflakes:set "
)

// Parse reads the packages, options and settings from a managed module.
func Parse(src string) Module {
	var m Module
	sc := bufio.NewScanner(strings.NewReader(src))
	for sc.Scan() {
		line := sc.Text()
		if s, ok := parseSetting(line); ok {
			m.Settings = append(m.Settings, s)
		} else if i := strings.Index(line, pkgMarker); i >= 0 {
			if f := strings.Fields(line[i+len(pkgMarker):]); len(f) == 2 {
				m.Packages = append(m.Packages, Entry{Channel: f[0], Attr: f[1]})
			}
		} else if i := strings.Index(line, optionMarker); i >= 0 {
			if o, err := generate.ParseOptions(strings.TrimSpace(line[i+len(optionMarker):])); err == nil {
				m.Options |= o
			}
		} else if i := strings.Index(line, settingMarker); i >= 0 {
			// The marker repeats the assignment: "path = value".
			if path, value, ok := strings.Cut(line[i+len(settingMarker):], " = "); ok {
				m.Settings = append(m.Settings, Setting{Path: path, Value: strings.TrimSpace(value)})
			}
		}
	}
	return m
}

// parseSetting reads a setting line exactly as Render writes it:
// "  X; # lazyflakes:set X" with X = "path = value". The assignment is there
// twice, so it splits in one place only, whatever the value holds (marker
// text included). Lines changed by hand fall back to the marker alone.
func parseSetting(line string) (Setting, bool) {
	body, ok := strings.CutPrefix(line, "  ")
	sep := "; " + settingMarker
	n := (len(body) - len(sep)) / 2
	if !ok || n <= 0 || 2*n+len(sep) != len(body) || body[n:] != sep+body[:n] {
		return Setting{}, false
	}
	path, value, ok := strings.Cut(body[:n], " = ")
	if !ok {
		return Setting{}, false
	}
	return Setting{Path: path, Value: strings.TrimSpace(value)}, true
}

// Read parses the module at path. A missing file is an empty module.
func Read(path string) (m Module, exists bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Module{}, false, nil
	}
	if err != nil {
		return Module{}, false, err
	}
	return Parse(string(data)), true, nil
}

var (
	identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_'-]*$`)
	// A package attribute is dot-separated names; whitespace, quotes and
	// the like would break its line or the marker it is read back from.
	attrRe = regexp.MustCompile(`^[^\s"#$\\;=.]+(\.[^\s"#$\\;=.]+)*$`)
	// A channel name also becomes part of a Nix variable name.
	channelRe = regexp.MustCompile(`^[A-Za-z0-9_'-]+$`)
)

// Render generates the module for target t.
func Render(t *config.Target, m Module) (string, error) {
	args := []string{"config", "lib", "pkgs"}
	var lets []string
	vars := map[string]string{} // channel -> Nix variable of its package set
	for _, e := range m.Packages {
		switch {
		case !attrRe.MatchString(e.Attr):
			return "", fmt.Errorf("%q is not a package attribute (no spaces, quotes, #, $, \\, ; or =, and no empty parts between dots)", e.Attr)
		case !channelRe.MatchString(e.Channel):
			return "", fmt.Errorf("channel name %q may only hold letters, digits, _, ' and -", e.Channel)
		}
		if _, ok := vars[e.Channel]; ok {
			continue
		}
		expr, ok := t.Sets[e.Channel]
		if !ok {
			return "", fmt.Errorf("target %q has no package set for the %s channel (add it under [targets.%s.sets] in config.toml)", t.Name, e.Channel, t.Name)
		}
		if identRe.MatchString(expr) {
			vars[e.Channel] = expr
			if !slices.Contains(args, expr) {
				args = append(args, expr)
			}
			continue
		}
		v := "lazyflakes-" + e.Channel
		vars[e.Channel] = v
		lets = append(lets, fmt.Sprintf("  %s = %s;", v, expr))
	}

	if err := checkSettings(m.Settings); err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, `# Managed by lazyflakes: this file is rewritten whenever packages or options
# are changed there, so changes made here by hand are lost.
# Import it once from your configuration:  imports = [ ./%s ];
{ %s, ... }:
`, filepath.Base(t.File), strings.Join(args, ", "))
	if len(lets) > 0 {
		fmt.Fprintf(&b, "\nlet\n%s\nin\n", strings.Join(lets, "\n"))
	}
	b.WriteString("{\n")
	if !t.WritesNixpkgsConfig() {
		m.Options = 0
	}
	for _, o := range m.Options.Enabled() {
		attr, value := o.Setting()
		fmt.Fprintf(&b, "  nixpkgs.config.%s = lib.mkDefault (%s); %s%s\n", attr, value, optionMarker, o.Key())
	}
	if m.Options != 0 {
		b.WriteString("\n")
	}
	// Plain assignments, not lib.mkDefault: if the same option is set
	// differently elsewhere, the rebuild reports the conflict (and is rolled
	// back) instead of silently ignoring what was chosen.
	for _, s := range m.Settings {
		fmt.Fprintf(&b, "  %s = %s; %s%s = %s\n", s.Path, s.Value, settingMarker, s.Path, s.Value)
	}
	if len(m.Settings) > 0 {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "  %s = [\n", t.Attribute)
	for _, e := range m.Packages {
		fmt.Fprintf(&b, "    %s %s%s %s\n", generate.AttrPath(vars[e.Channel], e.Attr), pkgMarker, e.Channel, e.Attr)
	}
	b.WriteString("  ];\n}\n")
	return b.String(), nil
}

// checkSettings rejects settings that would break the module or its
// markers.
func checkSettings(settings []Setting) error {
	seen := map[string]bool{}
	for _, s := range settings {
		switch {
		case strings.TrimSpace(s.Path) == "" || strings.TrimSpace(s.Value) == "":
			return fmt.Errorf("setting %q needs a name and a value", s.Path)
		case strings.ContainsAny(s.Path+s.Value, "\n\r"):
			return fmt.Errorf("setting %s: the value must be on one line", s.Path)
		case strings.Contains(s.Path, " = "):
			return fmt.Errorf("setting %q: bad name", s.Path)
		case seen[s.Path]:
			return fmt.Errorf("setting %s is given twice", s.Path)
		}
		seen[s.Path] = true
	}
	return nil
}

// Imported reports whether a .nix file in the module's directory tree
// refers to the module by file name, i.e. whether it is likely imported.
func Imported(t *config.Target) bool {
	dir, name := filepath.Dir(t.File), filepath.Base(t.File)
	found := false
	seen := 0
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipDir
		}
		if d.IsDir() {
			if path != dir && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules" || d.Name() == "result") {
				return filepath.SkipDir
			}
			return nil
		}
		if seen++; seen > 2000 {
			return filepath.SkipAll
		}
		if path == t.File || !strings.HasSuffix(path, ".nix") {
			return nil
		}
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), name) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
