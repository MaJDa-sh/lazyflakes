package search

import (
	"testing"

	"github.com/kacper/lazyflakes/internal/nix"
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
