package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/generate"
	"github.com/MaJDa-sh/lazyflakes/internal/managed"
	"github.com/MaJDa-sh/lazyflakes/internal/options"
)

// runSet implements `lazyflakes set PATH VALUE` and `lazyflakes unset PATH...`.
func runSet(args []string, unset bool) error {
	name, usage := "set", "set [flags] OPTION VALUE"
	if unset {
		name, usage = "unset", "unset [flags] OPTION..."
	}
	f := newTargetFlags(name, usage)
	raw := f.fs.Bool("raw", false, "VALUE is a Nix expression, used as given (otherwise it is read according to the option's type)")
	noRebuild := f.fs.Bool("no-rebuild", false, "only write the module (script targets always run their script)")
	f.fs.Parse(args)
	rest := f.fs.Args()
	if len(rest) == 0 || (!unset && len(rest) < 2) {
		f.fs.Usage()
		return errors.New("an option" + map[bool]string{true: "", false: " and a value"}[unset] + " is needed")
	}
	t, err := f.load()
	if err != nil {
		return err
	}
	mod, _, err := managed.Load(t)
	if err != nil {
		return err
	}
	old := mod
	old.Settings = slices.Clone(mod.Settings)

	if unset {
		for _, path := range rest {
			i := slices.IndexFunc(mod.Settings, func(s managed.Setting) bool { return s.Path == path })
			if i < 0 {
				fmt.Fprintf(os.Stderr, "%s is not set through lazyflakes (see `lazyflakes list`)\n", path)
				continue
			}
			mod.Settings = slices.Delete(mod.Settings, i, i+1)
		}
	} else {
		path, value, err := resolveSetting(t, rest[0], strings.Join(rest[1:], " "), *raw)
		if err != nil {
			return err
		}
		if i := slices.IndexFunc(mod.Settings, func(s managed.Setting) bool { return s.Path == path }); i >= 0 {
			mod.Settings[i].Value = value
		} else {
			mod.Settings = append(mod.Settings, managed.Setting{Path: path, Value: value})
		}
	}

	if slices.Equal(old.Settings, mod.Settings) {
		fmt.Printf("%s: nothing to change\n", t.Name)
		return nil
	}
	ran, err := applyChange(t, old, mod, *noRebuild)
	if err != nil || !ran {
		return err
	}
	fmt.Printf("%s: %d → %d options set\n", t.Name, len(old.Settings), len(mod.Settings))
	return nil
}

// resolveSetting works out the attribute path and Nix value for an option
// the user named. Unless raw, the option has to exist and the value is read
// according to its type.
func resolveSetting(t *config.Target, name, value string, raw bool) (path, expr string, err error) {
	if raw {
		if err := options.CheckPath(name); err != nil {
			return "", "", err
		}
		if err := options.CheckSyntax(value); err != nil {
			return "", "", err
		}
		return name, strings.TrimSpace(value), nil
	}
	fmt.Fprintf(os.Stderr, "looking up %s…\n", name)
	src, err := options.SourceFor(t, options.DetectEnv(generate.DefaultChannels(time.Now())[1].Ref))
	if err != nil {
		return "", "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	all, err := src.Load(ctx)
	if err != nil {
		return "", "", err
	}
	for _, o := range all {
		if o.Name != name && o.Path() != name {
			continue
		}
		if !o.Settable() {
			return "", "", fmt.Errorf("%s cannot be set directly (read-only, or it needs a name such as a host)", name)
		}
		expr, err := o.Format(value, false)
		if err != nil {
			return "", "", fmt.Errorf("%s takes %s (%s): %w", name, o.Kind, o.Type, err)
		}
		if o.Kind == options.Other {
			if err := options.CheckSyntax(expr); err != nil {
				return "", "", err
			}
		}
		return o.Path(), expr, nil
	}
	return "", "", fmt.Errorf("no option named %s in the %s (use -raw to set it anyway)", name, src.Label)
}
