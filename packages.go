package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/generate"
	"github.com/MaJDa-sh/lazyflakes/internal/managed"
)

// targetFlags are the flags shared by add, remove and list.
type targetFlags struct {
	fs         *flag.FlagSet
	configPath *string
	target     *string
}

func newTargetFlags(name, usage string) targetFlags {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: lazyflakes %s\n\nFlags:\n", usage)
		fs.PrintDefaults()
	}
	return targetFlags{
		fs:         fs,
		configPath: fs.String("config", defaultConfigPath(), "config file with the system targets"),
		target:     fs.String("target", "", "target to change (default: the config's default)"),
	}
}

func (f targetFlags) load() (*config.Target, error) {
	cfg, err := config.Load(*f.configPath)
	if errors.Is(err, config.ErrNoConfig) {
		return nil, fmt.Errorf("no config at %s, run `lazyflakes setup` first", *f.configPath)
	}
	if err != nil {
		return nil, err
	}
	targets := cfg.Ordered()
	if *f.target == "" {
		if len(targets) == 0 {
			return nil, errors.New("the config has no targets")
		}
		return targets[0], nil
	}
	if t := cfg.Targets[*f.target]; t != nil {
		return t, nil
	}
	return nil, fmt.Errorf("no target %q in %s", *f.target, *f.configPath)
}

// runChange implements `lazyflakes add` and `lazyflakes remove`.
func runChange(args []string, remove bool) error {
	name, usage := "add", "add [flags] ATTR..."
	if remove {
		name, usage = "remove", "remove [flags] ATTR..."
	}
	f := newTargetFlags(name, usage)
	channel := f.fs.String("channel", "", "channel to install from (default: the one the target maps to pkgs)")
	allow := f.fs.String("allow", "", "nixpkgs options to enable: unfree,insecure,broken,unsupported")
	noRebuild := f.fs.Bool("no-rebuild", false, "only write the module (script targets always run their script)")
	f.fs.Parse(args)
	attrs := f.fs.Args()
	if len(attrs) == 0 {
		f.fs.Usage()
		return errors.New("no packages given")
	}
	t, err := f.load()
	if err != nil {
		return err
	}
	opts, err := generate.ParseOptions(*allow)
	if err != nil {
		return err
	}
	mod, _, err := managed.Load(t)
	if err != nil {
		return err
	}
	old := mod
	old.Packages = slices.Clone(mod.Packages) // the loop below edits mod in place

	ch := *channel
	if ch == "" {
		ch = defaultChannel(t)
	}
	before := len(mod.Packages)
	changed := opts&^mod.Options != 0
	mod.Options |= opts
	for _, attr := range attrs {
		i := slices.IndexFunc(mod.Packages, func(e managed.Entry) bool { return e.Attr == attr })
		switch {
		case remove && i < 0:
			fmt.Fprintf(os.Stderr, "%s is not installed through lazyflakes\n", attr)
		case remove:
			mod.Packages = slices.Delete(mod.Packages, i, i+1)
			changed = true
		case i < 0:
			mod.Packages = append(mod.Packages, managed.Entry{Channel: ch, Attr: attr})
			changed = true
		case mod.Packages[i].Channel != ch:
			mod.Packages[i].Channel = ch
			changed = true
		default:
			fmt.Fprintf(os.Stderr, "%s is already installed from %s\n", attr, ch)
		}
	}
	if !changed {
		return nil
	}

	plan, err := managed.Prepare(t, old, mod, !*noRebuild)
	if err != nil {
		return err
	}
	defer plan.Cleanup()
	if !plan.HasWork() {
		fmt.Printf("%s: nothing to do\n", t.Name)
		return nil
	}
	cmd := exec.Command("sh", "-c", plan.Script())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := plan.Done(cmd.Run()); err != nil {
		return fmt.Errorf("%s was not changed: %w", t.Name, err)
	}
	fmt.Printf("%s: %d → %d packages\n", t.Name, before, len(mod.Packages))
	return nil
}

// defaultChannel is the channel a target maps to its own pkgs, else the
// first channel it has a set for.
func defaultChannel(t *config.Target) string {
	var names []string
	for name, expr := range t.Sets {
		if expr == "pkgs" {
			return name
		}
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) > 0 {
		return names[0]
	}
	return "stable"
}

// runList implements `lazyflakes list`.
func runList(args []string) error {
	f := newTargetFlags("list", "list [flags]")
	f.fs.Parse(args)
	t, err := f.load()
	if err != nil {
		return err
	}
	mod, exists, err := managed.Load(t)
	if err != nil {
		return err
	}
	if !exists {
		fmt.Printf("%s: %s does not exist yet\n", t.Name, t.File)
		return nil
	}
	for _, e := range mod.Packages {
		fmt.Printf("%-40s %s\n", e.Attr, e.Channel)
	}
	if keys := mod.Options.Keys(); len(keys) > 0 {
		fmt.Printf("options: %s\n", strings.Join(keys, ", "))
	}
	if !t.IsScript() && !managed.IsImported(context.Background(), t) {
		fmt.Printf("warning: %s is not imported by your configuration yet\n", t.File)
	}
	return nil
}
