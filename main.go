// lazyflakes is a lazygit-style TUI for browsing nixpkgs and, eventually,
// generating flake.nix / shell.nix files from the selected packages.
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/kacper/lazyflakes/internal/ui"
)

func main() {
	flakeRef := flag.String("flake", "nixpkgs", "flake reference to browse (e.g. nixpkgs, github:NixOS/nixpkgs/nixos-25.11)")
	refresh := flag.Bool("refresh", false, "ignore the package cache and re-evaluate")
	flag.Parse()

	p := tea.NewProgram(ui.New(*flakeRef, *refresh), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "lazyflakes:", err)
		os.Exit(1)
	}
}
