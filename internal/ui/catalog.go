package ui

import (
	"sort"

	"github.com/MaJDa-sh/lazyflakes/internal/nix"
)

// merge unions per-channel package lists by attr path. Metadata comes from the
// first channel that has the package; versions[i][c] is "" when channel c
// does not have package i.
func merge(lists [][]nix.Package) ([]nix.Package, [][]string) {
	type entry struct {
		pkg      nix.Package
		versions []string
	}
	byAttr := make(map[string]*entry)
	for c, list := range lists {
		for _, p := range list {
			e, ok := byAttr[p.Attr]
			if !ok {
				e = &entry{pkg: p, versions: make([]string, len(lists))}
				byAttr[p.Attr] = e
			}
			e.versions[c] = p.Version
			if e.versions[c] == "" {
				e.versions[c] = "?" // present, but without a version string
			}
		}
	}

	entries := make([]*entry, 0, len(byAttr))
	for _, e := range byAttr {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].pkg.Attr < entries[j].pkg.Attr })

	pkgs := make([]nix.Package, len(entries))
	versions := make([][]string, len(entries))
	for i, e := range entries {
		pkgs[i], versions[i] = e.pkg, e.versions
	}
	return pkgs, versions
}
