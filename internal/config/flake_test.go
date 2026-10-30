package config

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// gitFlake is a git repository with a tracked flake, a tracked module in a
// subdirectory and an ignored secret.
func gitFlake(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"flake.nix":     "{ outputs = _: { }; }\n",
		".gitignore":    "secret.key\n",
		"secret.key":    "TOPSECRET\n",
		"hosts/a/x.nix": "{ }\n",
		"untracked.nix": "{ }\n",
	}
	for name, content := range files {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "flake.nix", ".gitignore", "hosts/a/x.nix"}} {
		if err := exec.Command("git", append([]string{"-C", dir}, args...)...).Run(); err != nil {
			t.Skip("git not available")
		}
	}
	return dir
}

func TestFlakeExprListsTrackedFiles(t *testing.T) {
	dir := gitFlake(t)
	expr, cleanup, err := FlakeExpr(dir)
	if err != nil {
		t.Fatal(err)
	}
	list := regexp.MustCompile(`readFile "([^"]+)"`).FindStringSubmatch(expr)
	if list == nil {
		t.Fatalf("no list of tracked files in:\n%s", expr)
	}
	data, err := os.ReadFile(list[1])
	if err != nil {
		t.Fatal(err)
	}
	var tracked struct{ Files, Dirs []string }
	json.Unmarshal(data, &tracked)
	if want := []string{".gitignore", "flake.nix", "hosts/a/x.nix"}; !reflect.DeepEqual(tracked.Files, want) {
		t.Errorf("files = %q, want %q", tracked.Files, want)
	}
	if want := []string{"hosts/a", "hosts"}; !reflect.DeepEqual(tracked.Dirs, want) {
		t.Errorf("dirs = %q, want %q", tracked.Dirs, want)
	}
	cleanup()
	if _, err := os.Stat(list[1]); err == nil {
		t.Error("cleanup should remove the list")
	}
}

func TestFlakeExprOutsideGit(t *testing.T) {
	dir := t.TempDir()
	expr, cleanup, err := FlakeExpr(dir)
	defer cleanup()
	if err != nil || expr != `(builtins.getFlake "path:`+dir+`")` {
		t.Errorf("FlakeExpr = %s, %v", expr, err)
	}
}

// With LAZYFLAKES_NIX_EVAL=1: what Nix copies into the store.
func TestFlakeExprCopiesNoIgnoredFiles(t *testing.T) {
	if os.Getenv("LAZYFLAKES_NIX_EVAL") == "" {
		t.Skip("set LAZYFLAKES_NIX_EVAL=1 to evaluate with nix")
	}
	dir := gitFlake(t)
	expr, cleanup, err := FlakeExpr(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	out, err := exec.Command("nix", "--extra-experimental-features", "nix-command flakes", "eval", "--impure", "--json", "--expr",
		`let f = `+expr+`; ls = d: builtins.attrNames (builtins.readDir d); in { top = ls f.outPath; sub = ls (f.outPath + "/hosts/a"); }`).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var got struct{ Top, Sub []string }
	json.Unmarshal(out, &got)
	if want := []string{".gitignore", "flake.nix", "hosts"}; !reflect.DeepEqual(got.Top, want) || !reflect.DeepEqual(got.Sub, []string{"x.nix"}) {
		t.Errorf("store copy has %q and hosts/a %q, want %q and [x.nix]", got.Top, got.Sub, want)
	}
	if strings.Contains(string(out), "secret") {
		t.Error("an ignored file reached the store")
	}
}
