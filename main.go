// lazyflakes is a lazygit-style TUI for browsing nixpkgs (stable and unstable)
// and generating flake.nix / shell.nix files from the selected packages.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaJDa-sh/lazyflakes/internal/generate"
	"github.com/MaJDa-sh/lazyflakes/internal/ui"
)

func main() {
	channels := generate.DefaultChannels(time.Now())
	flag.StringVar(&channels[0].Ref, "unstable", channels[0].Ref, "flake reference of the unstable channel")
	flag.StringVar(&channels[1].Ref, "stable", channels[1].Ref, "flake reference of the stable channel (default: latest NixOS release)")
	channelFlag := flag.String("channel", envOr("LAZYFLAKES_CHANNEL", "unstable"), "channel new selections come from: unstable or stable (env LAZYFLAKES_CHANNEL)")
	refresh := flag.Bool("refresh", false, "ignore the package cache and re-evaluate")
	formatFlag := flag.String("format", envOr("LAZYFLAKES_FORMAT", "flake"), "preferred file to generate: flake or shell (env LAZYFLAKES_FORMAT)")
	envrc := flag.Bool("envrc", os.Getenv("LAZYFLAKES_ENVRC") != "0", "also write a direnv .envrc (env LAZYFLAKES_ENVRC=0 to disable)")
	dir := flag.String("dir", ".", "directory to write generated files into")
	flag.Parse()

	format, err := generate.ParseFormat(*formatFlag)
	if err != nil {
		fail(err)
	}
	channel := slices.IndexFunc(channels, func(c generate.Channel) bool { return c.Name == *channelFlag })
	if channel < 0 {
		fail(fmt.Errorf("unknown channel %q (want unstable or stable)", *channelFlag))
	}
	absDir, err := resolveDir(*dir)
	if err != nil {
		fail(err)
	}

	p := tea.NewProgram(ui.New(channels, channel, *refresh, absDir, format, *envrc), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fail(err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func resolveDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	return abs, nil
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "lazyflakes:", err)
	os.Exit(1)
}
