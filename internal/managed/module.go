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

// Module is the content of a managed file.
type Module struct {
	Packages []Entry
	Options  generate.Options
}

// Markers at the end of generated lines let Parse read the module back
// without evaluating any Nix.
const (
	pkgMarker    = "# lazyflakes:pkg "
	optionMarker = "# lazyflakes:option "
)

// Parse reads the packages and options from a managed module.
func Parse(src string) Module {
	var m Module
	sc := bufio.NewScanner(strings.NewReader(src))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, pkgMarker); i >= 0 {
			if f := strings.Fields(line[i+len(pkgMarker):]); len(f) == 2 {
				m.Packages = append(m.Packages, Entry{Channel: f[0], Attr: f[1]})
			}
		} else if i := strings.Index(line, optionMarker); i >= 0 {
			if o, err := generate.ParseOptions(strings.TrimSpace(line[i+len(optionMarker):])); err == nil {
				m.Options |= o
			}
		}
	}
	return m
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

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_'-]*$`)

// Render generates the module for target t.
func Render(t *config.Target, m Module) (string, error) {
	args := []string{"config", "lib", "pkgs"}
	var lets []string
	vars := map[string]string{} // channel -> Nix variable of its package set
	for _, e := range m.Packages {
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

	var b strings.Builder
	fmt.Fprintf(&b, `# Managed by lazyflakes: this file is rewritten whenever packages are added
# or removed there, so changes made here by hand are lost.
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
	fmt.Fprintf(&b, "  %s = [\n", t.Attribute)
	for _, e := range m.Packages {
		fmt.Fprintf(&b, "    %s %s%s %s\n", generate.AttrPath(vars[e.Channel], e.Attr), pkgMarker, e.Channel, e.Attr)
	}
	b.WriteString("  ];\n}\n")
	return b.String(), nil
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
