package generate

import (
	"fmt"
	"time"
)

// Channel is a nixpkgs flake that packages can be taken from.
type Channel struct {
	Name  string // shown in the UI, e.g. "stable"
	Input string // flake input name, e.g. "nixpkgs-stable"
	Var   string // Nix variable bound to its package set, e.g. "pkgsStable"
	Ref   string // flake reference
}

// Package is an attribute taken from one of the channels.
type Package struct {
	Attr    string
	Channel int // index into the channels slice it is rendered with
}

// DefaultChannels returns nixpkgs-unstable followed by the latest stable
// NixOS release at now. Either ref can be replaced by the caller.
func DefaultChannels(now time.Time) []Channel {
	return []Channel{
		{Name: "unstable", Input: "nixpkgs", Var: "pkgs", Ref: "github:NixOS/nixpkgs/nixpkgs-unstable"},
		{Name: "stable", Input: "nixpkgs-stable", Var: "pkgsStable", Ref: "github:NixOS/nixpkgs/" + StableBranch(now)},
	}
}

// StableBranch is the newest nixos-YY.MM branch at t. NixOS releases at the
// end of May and November, so a release counts from the following month.
func StableBranch(t time.Time) string {
	year, month := t.Year()%100, t.Month()
	switch {
	case month == time.December:
		return fmt.Sprintf("nixos-%02d.11", year)
	case month >= time.June:
		return fmt.Sprintf("nixos-%02d.05", year)
	default:
		return fmt.Sprintf("nixos-%02d.11", (year+99)%100)
	}
}
