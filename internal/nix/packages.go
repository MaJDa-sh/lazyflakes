// Package nix loads package metadata from a nixpkgs flake via `nix search`.
package nix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Package is a single attribute from the package set.
type Package struct {
	Attr        string `json:"attr"` // e.g. "python3Packages.requests"
	Pname       string `json:"pname"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// CacheTTL is how long a cached package list is considered fresh.
const CacheTTL = 24 * time.Hour

type searchEntry struct {
	Pname       string `json:"pname"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// attrPrefix matches "legacyPackages.x86_64-linux." / "packages.aarch64-darwin." etc.
var attrPrefix = regexp.MustCompile(`^(legacyPackages|packages)\.[^.]+\.`)

// Load returns all packages of flakeRef, using the on-disk cache when it is
// fresh and refresh is false. The bool reports whether the cache was used.
func Load(ctx context.Context, flakeRef string, refresh bool) ([]Package, bool, error) {
	path, err := cachePath(flakeRef)
	if err != nil {
		return nil, false, err
	}
	if !refresh {
		if pkgs, err := readCache(path); err == nil {
			return pkgs, true, nil
		}
	}
	pkgs, err := Search(ctx, flakeRef)
	if err != nil {
		return nil, false, err
	}
	// A cache write failure is not fatal; we just evaluate again next time.
	_ = writeCache(path, pkgs)
	return pkgs, false, nil
}

// Search runs `nix search <flakeRef> ^ --json` and parses the result.
func Search(ctx context.Context, flakeRef string) ([]Package, error) {
	cmd := exec.CommandContext(ctx, "nix",
		"--extra-experimental-features", "nix-command flakes",
		"search", flakeRef, "^", "--json")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("nix search %s: %w\n%s", flakeRef, err, lastLines(stderr.String(), 5))
	}

	var raw map[string]searchEntry
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parsing nix search output: %w", err)
	}
	pkgs := make([]Package, 0, len(raw))
	for key, e := range raw {
		pkgs = append(pkgs, Package{
			Attr:        attrPrefix.ReplaceAllString(key, ""),
			Pname:       e.Pname,
			Version:     e.Version,
			Description: e.Description,
		})
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Attr < pkgs[j].Attr })
	return pkgs, nil
}

func cachePath(flakeRef string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	name := regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(flakeRef, "_")
	return filepath.Join(dir, "lazyflakes", name+".json"), nil
}

func readCache(path string) ([]Package, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if time.Since(info.ModTime()) > CacheTTL {
		return nil, errors.New("cache expired")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pkgs []Package
	if err := json.Unmarshal(data, &pkgs); err != nil {
		return nil, err
	}
	return pkgs, nil
}

func writeCache(path string, pkgs []Package) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(pkgs)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
