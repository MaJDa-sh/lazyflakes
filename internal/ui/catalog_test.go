package ui

import (
	"reflect"
	"testing"

	"github.com/MaJDa-sh/lazyflakes/internal/nix"
)

func TestMerge(t *testing.T) {
	unstable := []nix.Package{
		{Attr: "ripgrep", Version: "15.2.0", Description: "new"},
		{Attr: "newtool", Version: "1.0"},
	}
	stable := []nix.Package{
		{Attr: "ripgrep", Version: "14.1.1", Description: "old"},
		{Attr: "oldtool", Version: ""},
	}
	pkgs, versions := merge([][]nix.Package{unstable, stable})

	var attrs []string
	for _, p := range pkgs {
		attrs = append(attrs, p.Attr)
	}
	if want := []string{"newtool", "oldtool", "ripgrep"}; !reflect.DeepEqual(attrs, want) {
		t.Fatalf("attrs = %v, want %v", attrs, want)
	}
	want := [][]string{{"1.0", ""}, {"", "?"}, {"15.2.0", "14.1.1"}}
	if !reflect.DeepEqual(versions, want) {
		t.Errorf("versions = %v, want %v", versions, want)
	}
	if pkgs[2].Description != "new" {
		t.Errorf("metadata should come from the first channel, got %q", pkgs[2].Description)
	}
}
