package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/generate"
	"github.com/MaJDa-sh/lazyflakes/internal/managed"
)

// runSetup implements `lazyflakes setup`: detect the system's configuration,
// write config.toml and create the empty managed modules.
func runSetup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "config file to write")
	yes := fs.Bool("yes", false, "answer yes to every question")
	printOnly := fs.Bool("print", false, "only print the detected config")
	flakeDir := fs.String("flake", "", "directory of your configuration flake (default: search "+strings.Join(config.FlakeDirs, ", ")+")")
	host := fs.String("host", "", "flake configuration to manage (default: the one named after this machine)")
	fs.Parse(args)

	if *flakeDir == "" {
		fmt.Println("Looking for your configuration (flakes are evaluated, which can take a moment)…")
	}
	sys := config.Inspect(*flakeDir)
	d := config.Detect(sys, generate.DefaultChannels(time.Now()), *host)
	toml := d.TOML()
	if *printOnly {
		fmt.Print(toml)
		return nil
	}

	fmt.Println("Detected:")
	for _, n := range d.Notes {
		fmt.Println("  • " + n)
	}
	fmt.Printf("\nProposed %s:\n\n", *configPath)
	for _, line := range strings.Split(strings.TrimRight(toml, "\n"), "\n") {
		fmt.Println("    " + line)
	}
	fmt.Println()

	ask := func(q string, def bool) bool { return *yes || confirm(q, def) }
	if _, err := os.Stat(*configPath); err == nil {
		if !ask(*configPath+" already exists. Overwrite it?", false) {
			fmt.Println("Kept the existing config.")
			return nil
		}
	} else if !ask("Write this config?", true) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(*configPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*configPath, []byte(toml), 0o644); err != nil {
		return err
	}
	fmt.Println("Wrote " + *configPath)

	targets := d.Config.Ordered()
	for _, t := range targets {
		if _, err := os.Stat(t.File); err == nil {
			continue
		}
		if !ask(fmt.Sprintf("Create an empty %s for target %q now?", t.File, t.Name), true) {
			continue
		}
		if err := createModule(t); err != nil {
			fmt.Fprintf(os.Stderr, "could not create %s: %v\n", t.File, err)
		}
	}

	fmt.Println("\nNext steps:")
	for _, t := range targets {
		if managed.IsImported(context.Background(), t) {
			fmt.Printf("  • %s: %s is already imported\n", t.Name, t.File)
			continue
		}
		name := filepath.Base(t.File)
		if t.Flake != "" || strings.Contains(t.Rebuild, "--flake") {
			// A flake lists its modules; imports inside a module also work.
			fmt.Printf("  • %s: add ./%s to the modules list of your configuration in flake.nix (or to the imports of a module), then rebuild once:\n"+
				"      modules = [ ./configuration.nix ./%s ];\n      %s\n", t.Name, name, name, t.Rebuild)
			continue
		}
		fmt.Printf("  • %s: add ./%s to the imports of %s, then rebuild once:\n      %s\n",
			t.Name, name, importSite(t, sys), t.Rebuild)
	}
	fmt.Println("  • then in lazyflakes select packages with space and press a to install them")
	if len(targets) == 0 {
		return errors.New("nothing detected; edit the config by hand")
	}
	return nil
}

// createModule writes an empty managed module, using sudo when needed.
func createModule(t *config.Target) error {
	content, err := managed.Render(t, managed.Module{})
	if err != nil {
		return err
	}
	plan, err := managed.NewPlan(t, content, false)
	if err != nil {
		return err
	}
	defer plan.Cleanup()
	if plan.Sudo {
		fmt.Println("(the directory is not writable, so sudo may ask for your password)")
	}
	cmd := exec.Command("sh", "-c", plan.Script())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// importSite names the file the user should import the module from.
func importSite(t *config.Target, sys config.System) string {
	dir := filepath.Dir(t.File)
	switch {
	case t.Kind == "nixos" && sys.NixOSConfig != "" && filepath.Dir(sys.NixOSConfig) == dir:
		return sys.NixOSConfig
	case t.Kind == "home-manager":
		if _, err := os.Stat(filepath.Join(dir, "home.nix")); err == nil {
			return filepath.Join(dir, "home.nix")
		}
	}
	return "your configuration in " + dir
}

// stdin is shared so answers piped in ahead of the questions are not lost.
var stdin = bufio.NewReader(os.Stdin)

func confirm(question string, def bool) bool {
	hint := "[Y/n]"
	if !def {
		hint = "[y/N]"
	}
	fmt.Printf("%s %s ", question, hint)
	line, _ := stdin.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	}
	return def
}

func defaultConfigPath() string {
	path, err := config.Path()
	if err != nil {
		return "lazyflakes.toml"
	}
	return path
}
