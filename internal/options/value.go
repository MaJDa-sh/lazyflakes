package options

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MaJDa-sh/lazyflakes/internal/generate"
)

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_'-]*$`)

// Path is the attribute path to assign the option in a module, with the
// segments that are not plain identifiers quoted.
func (o Option) Path() string {
	loc := o.Loc
	if len(loc) == 0 {
		loc = strings.Split(o.Name, ".")
	}
	segs := make([]string, len(loc))
	for i, s := range loc {
		if identRe.MatchString(s) {
			segs[i] = s
		} else {
			segs[i] = generate.NixString(s)
		}
	}
	return strings.Join(segs, ".")
}

// Format turns what the user typed into the Nix expression to assign. A raw
// value is taken as Nix as it is; otherwise it is read according to the
// option's kind. null is accepted for options that allow it.
func (o Option) Format(input string, raw bool) (string, error) {
	in := strings.TrimSpace(input)
	if strings.ContainsAny(in, "\n\r") {
		return "", errors.New("one line only")
	}
	if in == "" && !(o.Kind == StringList && !raw) && !(o.Kind == String && !raw) {
		return "", errors.New("enter a value")
	}
	if o.Nullable && in == "null" {
		return "null", nil
	}
	if raw || o.Kind == Other {
		return in, nil
	}

	switch o.Kind {
	case Bool:
		switch strings.ToLower(in) {
		case "true", "yes", "on", "1":
			return "true", nil
		case "false", "no", "off", "0":
			return "false", nil
		}
		return "", errors.New("true or false")
	case Enum:
		for _, c := range o.Choices {
			if in == c {
				return generate.NixString(c), nil
			}
		}
		return "", fmt.Errorf("one of: %s", strings.Join(o.Choices, ", "))
	case Int:
		n, err := strconv.ParseInt(in, 10, 64)
		if err != nil {
			return "", errors.New("a whole number")
		}
		return strconv.FormatInt(n, 10), nil
	case String:
		return generate.NixString(input), nil // spaces and all: not trimmed
	case Path:
		if !strings.HasPrefix(in, "/") {
			return "", errors.New("an absolute path, starting with /")
		}
		// A string, not a path literal: a literal is copied into the Nix
		// store when it is used as text.
		return generate.NixString(in), nil
	case StringList:
		var items []string
		for _, f := range commaList(in) {
			items = append(items, generate.NixString(f))
		}
		if len(items) == 0 {
			return "[ ]", nil
		}
		return "[ " + strings.Join(items, " ") + " ]", nil
	case Package:
		return packageExpr(in)
	case PackageList:
		var items []string
		for _, f := range commaList(in) {
			e, err := packageExpr(f)
			if err != nil {
				return "", err
			}
			items = append(items, e)
		}
		if len(items) == 0 {
			return "[ ]", nil
		}
		return "[ " + strings.Join(items, " ") + " ]", nil
	}
	return in, nil
}

func commaList(s string) []string {
	var items []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			items = append(items, f)
		}
	}
	return items
}

// packageExpr reads a package: a name such as htop or python3Packages.requests
// is taken from pkgs, while something that already starts with a package set
// (pkgs.htop, pkgs-unstable.htop) is kept as typed.
func packageExpr(s string) (string, error) {
	s = strings.TrimSpace(s)
	segs := strings.Split(s, ".")
	for _, seg := range segs {
		if !identRe.MatchString(seg) {
			return "", errors.New("a package name such as htop, or an expression in raw mode")
		}
	}
	if strings.HasPrefix(segs[0], "pkgs") {
		return s, nil
	}
	return "pkgs." + s, nil
}

// CheckSyntax asks Nix to parse expr, so a typo is caught before it reaches
// a configuration.
func CheckSyntax(expr string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nix-instantiate", "--parse", "-E", expr).CombinedOutput()
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return nil // no nix-instantiate: nothing to check with
	}
	msg := strings.TrimSpace(string(out))
	if i := strings.Index(msg, "error:"); i >= 0 {
		msg = strings.TrimSpace(msg[i+len("error:"):])
	}
	if line, _, ok := strings.Cut(msg, "\n"); ok {
		msg = line
	}
	return errors.New("not valid Nix: " + msg)
}
