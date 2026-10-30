package search

import (
	"testing"

	"github.com/MaJDa-sh/lazyflakes/internal/nix"
)

func TestFilterRanking(t *testing.T) {
	pkgs := []nix.Package{
		{Attr: "python3Packages.requests", Pname: "requests"},
		{Attr: "ripgrep", Pname: "ripgrep", Description: "fast grep"},
		{Attr: "gnugrep", Pname: "gnugrep", Description: "GNU grep"},
		{Attr: "hello", Pname: "hello", Description: "says hello"},
	}
	idx := NewIndex(pkgs)

	if got := idx.Filter(""); len(got) != 4 {
		t.Fatalf("empty query: got %d results, want 4", len(got))
	}
	got := idx.Filter("ripgrep")
	if len(got) == 0 || pkgs[got[0]].Attr != "ripgrep" {
		t.Fatalf("ripgrep should rank first, got %v", got)
	}
	if got := idx.Filter("requests"); len(got) != 1 || got[0] != 0 {
		t.Fatalf("leaf attr match failed: %v", got)
	}
	if got := idx.Filter("grep fast"); len(got) != 1 || got[0] != 1 {
		t.Fatalf("multi-term AND failed: %v", got)
	}
	if got := idx.Filter("nothingmatches"); len(got) != 0 {
		t.Fatalf("expected no results, got %v", got)
	}
}

func TestCommandSearch(t *testing.T) {
	pkgs := []nix.Package{
		{Attr: "ripgrep", Pname: "ripgrep", Description: "a line-oriented search tool"},
		{Attr: "silver-searcher", Pname: "silver-searcher", Description: "code searching tool"},
		{Attr: "grep", Pname: "grep", Description: "GNU grep"},
		{Attr: "fd", Pname: "fd", Description: "simple, fast alternative to find"},
		{Attr: "findutils", Pname: "findutils", Description: "GNU Find Utilities"},
		{Attr: "tealdeer", Pname: "tealdeer", Description: "simplified man pages"},
		{Attr: "imagemagick", Pname: "imagemagick", Description: "image manipulation suite"},
	}
	idx := NewIndex(pkgs)
	idx.SetCommands(nix.CommandMap{
		"rg":      {"ripgrep"},
		"ag":      {"silver-searcher"},
		"fd":      {"fd"},
		"tldr":    {"tealdeer"},
		"convert": {"imagemagick"},
	})

	// "rg" should find ripgrep at the top, even though the name doesn't contain "rg".
	if got := idx.Filter("rg"); len(got) == 0 || got[0] != 0 {
		t.Errorf("rg should find ripgrep first, got indices %v", got)
	}
	// "ag" should find silver-searcher.
	if got := idx.Filter("ag"); len(got) == 0 || got[0] != 1 {
		t.Errorf("ag should find silver-searcher first, got indices %v", got)
	}
	// "convert" should find imagemagick.
	if got := idx.Filter("convert"); len(got) == 0 || got[0] != 6 {
		t.Errorf("convert should find imagemagick first, got indices %v", got)
	}
	// "tldr" should find tealdeer.
	if got := idx.Filter("tldr"); len(got) == 0 || got[0] != 5 {
		t.Errorf("tldr should find tealdeer first, got indices %v", got)
	}
	// Regular search still works.
	if got := idx.Filter("grep"); len(got) == 0 || got[0] != 2 {
		t.Errorf("grep should still find grep: %v", got)
	}
	// Without commands set, it falls back to normal matching.
	idx2 := NewIndex(pkgs)
	if got := idx2.Filter("rg"); len(got) != 0 && got[0] == 0 {
		t.Logf("without commands, rg may or may not match ripgrep: %v", got)
	}
}
