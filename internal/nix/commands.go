package nix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// CommandMap maps a command name (binary) to the package attributes that
// provide it, e.g. "rg" → ["ripgrep"]. It is loaded from the
// programs.sqlite that NixOS ships with its channel.
type CommandMap map[string][]string

// CommandCacheTTL is how long the command map is cached.
const CommandCacheTTL = 7 * 24 * time.Hour

// LoadCommands returns the command→package map, using the cache when it is
// fresh and loading from programs.sqlite otherwise. It is meant to run in
// the background; errors are silently ignored (the feature is disabled).
func LoadCommands(ctx context.Context) CommandMap {
	path, err := commandCachePath()
	if err != nil {
		return nil
	}
	if m, err := readCommandCache(path); err == nil {
		return m
	}
	m, err := loadFromDB(ctx)
	if err != nil || len(m) == 0 {
		return nil
	}
	_ = writeCommandCache(path, m) // not fatal
	return m
}

func commandCachePath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lazyflakes", "commands.json"), nil
}

func readCommandCache(path string) (CommandMap, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if time.Since(info.ModTime()) > CommandCacheTTL {
		return nil, errors.New("cache expired")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m CommandMap
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func writeCommandCache(path string, m CommandMap) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// programsDB finds programs.sqlite from NIX_PATH or the NixOS channel,
// without evaluating nixpkgs.
func programsDB() string {
	// NIX_PATH entries like nixos=/nix/store/…-nixos/nixos
	for entry := range strings.SplitSeq(os.Getenv("NIX_PATH"), ":") {
		for _, prefix := range []string{"nixos=", "nixpkgs="} {
			if dir, ok := strings.CutPrefix(entry, prefix); ok {
				if p := filepath.Join(dir, "programs.sqlite"); fileExists(p) {
					return p
				}
			}
		}
	}
	// The NixOS channel, always present on NixOS.
	p := "/nix/var/nix/profiles/per-user/root/channels/nixos/programs.sqlite"
	if fileExists(p) {
		return p
	}
	return ""
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return !errors.Is(err, fs.ErrNotExist)
}

// findSQLite3 locates the sqlite3 binary. It tries PATH first, then asks
// nix-instantiate to evaluate nixpkgs.sqlite.bin (which takes ~0.4s but
// finds the exact binary in the store).
func findSQLite3(ctx context.Context) string {
	if p, err := exec.LookPath("sqlite3"); err == nil {
		return p
	}
	out, err := exec.CommandContext(ctx, "nix-instantiate", "--eval", "-E",
		`"${(import <nixpkgs> {}).sqlite.bin}/bin/sqlite3"`).Output()
	if err != nil {
		return ""
	}
	p := strings.Trim(strings.TrimSpace(string(out)), `"`)
	if fileExists(p) {
		return p
	}
	return ""
}

func nixSystem() string {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if arch == "" {
		arch = runtime.GOARCH
	}
	return arch + "-" + runtime.GOOS
}

// loadFromDB reads programs.sqlite and builds the command map.
func loadFromDB(ctx context.Context) (CommandMap, error) {
	db := programsDB()
	if db == "" {
		return nil, errors.New("programs.sqlite not found")
	}
	sqlite3 := findSQLite3(ctx)
	if sqlite3 == "" {
		return nil, errors.New("sqlite3 not found")
	}
	query := fmt.Sprintf(
		"SELECT name, package FROM Programs WHERE system = '%s' AND name != package ORDER BY name, package",
		nixSystem())
	cmd := exec.CommandContext(ctx, sqlite3, "-separator", "\t", db, query)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("sqlite3: %w", err)
	}
	m := CommandMap{}
	for line := range strings.SplitSeq(string(out), "\n") {
		f := strings.SplitN(line, "\t", 2)
		if len(f) != 2 || f[0] == "" {
			continue
		}
		cmd, pkg := f[0], f[1]
		// Skip deeply nested packages (haskellPackages.*, etc.) for cleaner results.
		if strings.Count(pkg, ".") > 1 {
			continue
		}
		m[cmd] = append(m[cmd], pkg)
	}
	return m, nil
}
