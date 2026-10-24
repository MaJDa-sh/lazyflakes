// Package search ranks packages against a free-text query.
package search

import (
	"sort"
	"strings"

	"github.com/MaJDa-sh/lazyflakes/internal/nix"
)

// Index holds lowercased copies of the searchable fields so filtering on every
// keystroke does not allocate per package.
type Index struct {
	attr  []string
	pname []string
	desc  []string
}

func NewIndex(pkgs []nix.Package) *Index {
	idx := &Index{
		attr:  make([]string, len(pkgs)),
		pname: make([]string, len(pkgs)),
		desc:  make([]string, len(pkgs)),
	}
	for i, p := range pkgs {
		idx.attr[i] = strings.ToLower(p.Attr)
		idx.pname[i] = strings.ToLower(p.Pname)
		idx.desc[i] = strings.ToLower(p.Description)
	}
	return idx
}

// Filter returns indices of matching packages, best match first. Every
// whitespace-separated term must match the attr path, pname or description.
// An empty query returns all packages in their original order.
func (idx *Index) Filter(query string) []int {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		all := make([]int, len(idx.attr))
		for i := range all {
			all[i] = i
		}
		return all
	}

	type hit struct{ i, score int }
	var hits []hit
	for i := range idx.attr {
		total := 0
		for _, t := range terms {
			s := idx.score(i, t)
			if s == 0 {
				total = 0
				break
			}
			total += s
		}
		if total > 0 {
			hits = append(hits, hit{i, total})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })

	out := make([]int, len(hits))
	for k, h := range hits {
		out[k] = h.i
	}
	return out
}

func (idx *Index) score(i int, term string) int {
	attr, name := idx.attr[i], idx.pname[i]
	// The last attr segment is what people usually type ("requests" for
	// "python3Packages.requests").
	leaf := attr[strings.LastIndexByte(attr, '.')+1:]
	// Prefer top-level attrs and short names over deeply nested variants.
	// Clamped so a match never scores zero (zero means "no match").
	bonus := max(-40, 30-min(30, len(attr)/2)-10*strings.Count(attr, "."))

	switch {
	case attr == term:
		return 1000 + bonus
	case leaf == term || name == term:
		return 800 + bonus
	case strings.HasPrefix(leaf, term) || strings.HasPrefix(name, term):
		return 500 + bonus
	case strings.Contains(attr, term) || strings.Contains(name, term):
		return 300 + bonus
	case strings.Contains(idx.desc[i], term):
		return 100 + bonus
	case len(term) > 2 && isSubsequence(term, leaf):
		return 50 + bonus
	}
	return 0
}

// isSubsequence reports whether all bytes of needle appear in hay in order
// (fuzzy match: "pyreq" matches "pyrequests").
func isSubsequence(needle, hay string) bool {
	j := 0
	for k := 0; k < len(hay) && j < len(needle); k++ {
		if hay[k] == needle[j] {
			j++
		}
	}
	return j == len(needle)
}
