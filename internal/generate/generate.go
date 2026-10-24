// Package generate renders shell.nix and flake.nix files for a set of packages.
package generate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Format is the kind of file to generate.
type Format int

const (
	ShellNix Format = iota
	Flake
)

// Formats lists every format in menu order.
var Formats = []Format{ShellNix, Flake}

// ParseFormat accepts "shell", "shell.nix", "flake" or "flake.nix".
func ParseFormat(s string) (Format, error) {
	switch strings.TrimSuffix(strings.ToLower(s), ".nix") {
	case "shell":
		return ShellNix, nil
	case "flake":
		return Flake, nil
	}
	return 0, fmt.Errorf("unknown format %q (want shell or flake)", s)
}

// Filename is the file the format is written to.
func (f Format) Filename() string {
	if f == Flake {
		return "flake.nix"
	}
	return "shell.nix"
}

// Hint tells the user how to enter the generated shell.
func (f Format) Hint() string {
	if f == Flake {
		return "run `nix develop` (git add flake.nix first inside a git repo)"
	}
	return "run `nix-shell`"
}

// Render returns the file contents for attrs, pinned to flakeRef where possible.
func Render(f Format, attrs []string, flakeRef string) string {
	if f == Flake {
		return renderFlake(attrs, flakeRef)
	}
	return renderShell(attrs, flakeRef)
}

// ErrExists is returned by Write when the file exists and overwrite is false.
var ErrExists = errors.New("file already exists")

// Write renders the format into dir and returns the written path.
func Write(dir string, f Format, attrs []string, flakeRef string, overwrite bool) (string, error) {
	path := filepath.Join(dir, f.Filename())
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !overwrite {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o644)
	if errors.Is(err, os.ErrExist) {
		return path, ErrExists
	}
	if err != nil {
		return path, err
	}
	if _, err := file.WriteString(Render(f, attrs, flakeRef)); err != nil {
		file.Close()
		return path, err
	}
	return path, file.Close()
}

func renderShell(attrs []string, flakeRef string) string {
	src := "import <nixpkgs> { }"
	if url, ok := tarballURL(flakeRef); ok {
		src = fmt.Sprintf("import (fetchTarball %q) { }", url)
	}
	return fmt.Sprintf(`{ pkgs ? %s }:

pkgs.mkShell {
  packages = with pkgs; [
%s  ];
}
`, src, packageLines(attrs, "    "))
}

func renderFlake(attrs []string, flakeRef string) string {
	return fmt.Sprintf(`{
  description = "Development shell";

  inputs.nixpkgs.url = %q;

  outputs = { nixpkgs, ... }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
%s          ];
        };
      });
    };
}
`, flakeInputURL(flakeRef), packageLines(attrs, "            "))
}

func packageLines(attrs []string, indent string) string {
	var b strings.Builder
	for _, a := range attrs {
		b.WriteString(indent + nixAttrPath(a) + "\n")
	}
	return b.String()
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_'-]*$`)

// nixAttrPath quotes segments that are not valid Nix identifiers. A quoted
// first segment cannot be resolved through `with pkgs;`, so it is prefixed.
func nixAttrPath(attr string) string {
	segs := strings.Split(attr, ".")
	quoted := false
	for i, s := range segs {
		if !identRe.MatchString(s) {
			segs[i] = fmt.Sprintf("%q", s)
			quoted = quoted || i == 0
		}
	}
	path := strings.Join(segs, ".")
	if quoted {
		return "pkgs." + path
	}
	return path
}

// flakeInputURL turns the browsed flake ref into a flake input URL. The bare
// registry name "nixpkgs" resolves to nixpkgs-unstable by default.
func flakeInputURL(ref string) string {
	if ref == "" || ref == "nixpkgs" {
		return "github:NixOS/nixpkgs/nixpkgs-unstable"
	}
	return ref
}

var githubRef = regexp.MustCompile(`^github:([^/]+)/([^/?]+)(?:/([^?]+))?$`)

// tarballURL maps github:owner/repo[/ref] to a tarball for non-flake shell.nix.
// Other refs (including "nixpkgs") fall back to the <nixpkgs> channel.
func tarballURL(ref string) (string, bool) {
	m := githubRef.FindStringSubmatch(ref)
	if m == nil {
		return "", false
	}
	rev := m[3]
	if rev == "" {
		rev = "HEAD"
	}
	return fmt.Sprintf("https://github.com/%s/%s/archive/%s.tar.gz", m[1], m[2], rev), true
}

// EnvrcLine is the direnv directive that loads the format's shell.
func (f Format) EnvrcLine() string {
	if f == Flake {
		return "use flake"
	}
	return "use nix"
}

var useLine = regexp.MustCompile(`^\s*use\s+(nix|flake)\b`)

// WriteEnvrc makes dir/.envrc load the format's shell. Existing content is
// kept: a `use nix`/`use flake` line for the other format is swapped, one for
// the same format is left alone, and otherwise the directive is appended.
// changed reports whether the file was modified.
func WriteEnvrc(dir string, f Format) (path string, changed bool, err error) {
	path = filepath.Join(dir, ".envrc")
	want := f.EnvrcLine()

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, true, os.WriteFile(path, []byte(want+"\n"), 0o644)
	}
	if err != nil {
		return path, false, err
	}

	lines := strings.Split(string(data), "\n")
	out := make([]string, 0, len(lines)+1)
	found := false
	for _, l := range lines {
		m := useLine.FindStringSubmatch(l)
		switch {
		case m == nil:
			out = append(out, l)
		case found:
			changed = true // drop duplicate directives
		case "use "+m[1] == want:
			found = true
			out = append(out, l)
		default:
			found, changed = true, true
			out = append(out, want)
		}
	}
	if !found {
		if n := len(out); n > 0 && out[n-1] == "" {
			out = out[:n-1]
		}
		out = append(out, want, "")
		changed = true
	}
	if !changed {
		return path, false, nil
	}
	return path, true, os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644)
}

// ShellExpr is a one-line mkShell expression for `nix develop --impure --expr`,
// built from the same flake the packages were browsed from.
func ShellExpr(attrs []string, flakeRef string) string {
	segs := make([]string, len(attrs))
	for i, a := range attrs {
		segs[i] = nixAttrPath(a)
	}
	return fmt.Sprintf(`let pkgs = (builtins.getFlake %q).legacyPackages.${builtins.currentSystem}; `+
		`in pkgs.mkShell { name = "lazyflakes"; packages = with pkgs; [ %s ]; }`,
		flakeRef, strings.Join(segs, " "))
}
