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

// Render returns the file contents for pkgs, each taken from its channel,
// with opts applied to the nixpkgs config of every channel.
func Render(f Format, channels []Channel, pkgs []Package, opts Options) string {
	if f == Flake {
		return renderFlake(channels, pkgs, opts)
	}
	return renderShell(channels, pkgs, opts)
}

// ErrExists is returned by Write when the file exists and overwrite is false.
var ErrExists = errors.New("file already exists")

// Write renders the format into dir and returns the written path.
func Write(dir string, f Format, channels []Channel, pkgs []Package, opts Options, overwrite bool) (string, error) {
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
	if _, err := file.WriteString(Render(f, channels, pkgs, opts)); err != nil {
		file.Close()
		return path, err
	}
	return path, file.Close()
}

func renderShell(channels []Channel, pkgs []Package, opts Options) string {
	used := usedChannels(channels, pkgs)
	importArgs := "{ }"
	if cfg := opts.configExpr(); cfg != "" {
		importArgs = "{ config = " + cfg + "; }"
	}
	args := make([]string, len(used))
	for i, c := range used {
		src := "<nixpkgs>"
		if url, ok := tarballURL(c.Ref); ok {
			src = fmt.Sprintf("(fetchTarball %q)", url)
		}
		args[i] = fmt.Sprintf("%s ? import %s %s", c.Var, src, importArgs)
	}
	head := "{ " + args[0] + " }:"
	if len(args) > 1 {
		head = "{ " + strings.Join(args, "\n, ") + "\n}:"
	}
	return fmt.Sprintf(`%s

%s.mkShell {
  packages = %s;
}
`, head, used[0].Var, packagesExpr(channels, pkgs, "  "))
}

func renderFlake(channels []Channel, pkgs []Package, opts Options) string {
	used := usedChannels(channels, pkgs)
	cfg := opts.configExpr()
	// legacyPackages ignores config, so with options nixpkgs is imported.
	let := ""
	set := "        %s = %s.legacyPackages.${system};"
	if cfg != "" {
		let = "\n      config = " + cfg + ";"
		set = "        %s = import %s { inherit system config; };"
	}
	var inputs, args, sets, vars []string
	for _, c := range used {
		inputs = append(inputs, fmt.Sprintf("    %s.url = %q;", c.Input, flakeInputURL(c.Ref)))
		args = append(args, c.Input)
		sets = append(sets, fmt.Sprintf(set, c.Var, c.Input))
		vars = append(vars, c.Var)
	}
	return fmt.Sprintf(`{
  description = "Development shell";

  inputs = {
%s
  };

  outputs = { %s, ... }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];%s
      forAllSystems = f: %s.lib.genAttrs systems (system: f {
%s
      });
    in
    {
      devShells = forAllSystems ({ %s }: {
        default = %s.mkShell {
          packages = %s;
        };
      });
    };
}
`, strings.Join(inputs, "\n"), strings.Join(args, ", "), let, used[0].Input,
		strings.Join(sets, "\n"), strings.Join(vars, ", "), used[0].Var,
		packagesExpr(channels, pkgs, "          "))
}

// usedChannels returns the channels referenced by pkgs, in channel order. The
// first one also provides mkShell. With no packages the first channel is used.
func usedChannels(channels []Channel, pkgs []Package) []Channel {
	seen := make([]bool, len(channels))
	for _, p := range pkgs {
		seen[p.Channel] = true
	}
	var used []Channel
	for i, c := range channels {
		if seen[i] {
			used = append(used, c)
		}
	}
	if len(used) == 0 {
		used = channels[:1]
	}
	return used
}

// packagesExpr renders the package list. A single channel gets the idiomatic
// `with pkgs; [ … ]`; mixed channels prefix every attr with its package set.
// indent is the indentation of the line the expression starts on.
func packagesExpr(channels []Channel, pkgs []Package, indent string) string {
	used := usedChannels(channels, pkgs)
	var b strings.Builder
	if len(used) == 1 {
		b.WriteString("with " + used[0].Var + "; [\n")
	} else {
		b.WriteString("[\n")
	}
	for _, p := range pkgs {
		b.WriteString(indent + "  " + nixAttrPath(p.Attr, channels[p.Channel].Var, len(used) == 1) + "\n")
	}
	b.WriteString(indent + "]")
	return b.String()
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_'-]*$`)

// nixAttrPath renders attr inside package set set, quoting segments that are
// not valid Nix identifiers. Under `with set;` the prefix is only needed when
// the first segment is quoted, since that cannot be resolved through `with`.
func nixAttrPath(attr, set string, with bool) string {
	segs := strings.Split(attr, ".")
	quoted := false
	for i, s := range segs {
		if !identRe.MatchString(s) {
			segs[i] = fmt.Sprintf("%q", s)
			quoted = quoted || i == 0
		}
	}
	path := strings.Join(segs, ".")
	if quoted || !with {
		return set + "." + path
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

// ShellExpr is a one-line mkShell expression for `nix develop --impure --expr`
// that takes every package from its channel's flake, with opts applied.
func ShellExpr(channels []Channel, pkgs []Package, opts Options) string {
	used := usedChannels(channels, pkgs)
	var lets []string
	cfg := opts.configExpr()
	if cfg != "" {
		lets = append(lets, "config = "+cfg+";")
	}
	for _, c := range used {
		if cfg != "" {
			lets = append(lets, fmt.Sprintf("%s = import (builtins.getFlake %q).outPath { system = builtins.currentSystem; inherit config; };", c.Var, c.Ref))
		} else {
			lets = append(lets, fmt.Sprintf("%s = (builtins.getFlake %q).legacyPackages.${builtins.currentSystem};", c.Var, c.Ref))
		}
	}
	attrs := make([]string, len(pkgs))
	for i, p := range pkgs {
		attrs[i] = nixAttrPath(p.Attr, channels[p.Channel].Var, false)
	}
	return fmt.Sprintf(`let %s in %s.mkShell { name = "lazyflakes"; packages = [ %s ]; }`,
		strings.Join(lets, " "), used[0].Var, strings.Join(attrs, " "))
}
