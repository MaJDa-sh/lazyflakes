package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MaJDa-sh/lazyflakes/internal/generate"
)

// FlakeExpr is a Nix expression for the flake in dir, for evaluations
// that pass --impure.
//
// A git repository is copied into the store the way a git+file reference
// copies it, tracked files only, but without Nix reading the repository
// itself: Nix refuses repositories owned by another user, which is the
// normal state of /etc/nixos. A path: reference would work there too, but
// it copies every file, so ignored ones (often secrets) and .git would end
// up in the world-readable store. A directory outside git is copied whole,
// which is what Nix does with it as well.
//
// The tracked files are listed in a temporary file that cleanup removes
// once Nix is done.
func FlakeExpr(dir string) (expr string, cleanup func(), err error) {
	dir = filepath.Clean(dir)
	noop := func() {}
	out, err := exec.Command("git", "-c", "safe.directory="+dir, "-C", dir, "ls-files", "-z").Output()
	if err != nil {
		if _, statErr := os.Stat(filepath.Join(dir, ".git")); statErr == nil {
			// Never fall back to copying everything.
			return "", noop, fmt.Errorf("listing the files git tracks in %s: %w", dir, err)
		}
		return "(builtins.getFlake " + generate.NixString("path:"+dir) + ")", noop, nil
	}

	// Empty lists, not null, for a flake without subdirectories.
	tracked := struct {
		Files []string `json:"files"`
		Dirs  []string `json:"dirs"`
	}{Files: []string{}, Dirs: []string{}}
	seen := map[string]bool{}
	for f := range bytes.SplitSeq(out, []byte{0}) {
		if len(f) == 0 {
			continue
		}
		name := string(f)
		tracked.Files = append(tracked.Files, name)
		for d := filepath.Dir(name); d != "." && !seen[d]; d = filepath.Dir(d) {
			seen[d] = true
			tracked.Dirs = append(tracked.Dirs, d)
		}
	}
	data, err := json.Marshal(tracked)
	if err != nil {
		return "", noop, err
	}
	list, err := os.CreateTemp("", "lazyflakes-tracked-*.json")
	if err != nil {
		return "", noop, err
	}
	cleanup = func() { os.Remove(list.Name()) }
	_, err = list.Write(data)
	if closeErr := list.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		cleanup()
		return "", noop, err
	}

	// getFlake refuses a string that refers to a store path, so the copy's
	// context is dropped; builtins.path has already put it in the store.
	expr = strings.NewReplacer("@ROOT@", generate.NixString(dir), "@LIST@", generate.NixString(list.Name())).Replace(`(let
  root = @ROOT@;
  tracked = builtins.fromJSON (builtins.readFile @LIST@);
  names = l: builtins.listToAttrs (map (name: { inherit name; value = true; }) l);
  files = names tracked.files;
  dirs = names tracked.dirs;
  src = builtins.path {
    name = "source";
    path = root;
    filter = p: type:
      let rel = builtins.substring (builtins.stringLength root + 1) (builtins.stringLength p) p;
      in if type == "directory" then dirs ? ${rel} else files ? ${rel};
  };
in builtins.getFlake "path:${builtins.unsafeDiscardStringContext src}")`)
	return expr, cleanup, nil
}
