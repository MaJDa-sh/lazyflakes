package managed

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/generate"
)

func scriptTarget(t *testing.T, script string) *config.Target {
	t.Helper()
	dir := t.TempDir()
	return &config.Target{Name: "mine", Kind: config.KindScript, Script: script, File: filepath.Join(dir, "state.json"), Sudo: "sudo"}
}

func runScript(t *testing.T, p Plan) (string, error) {
	t.Helper()
	out, err := exec.Command("sh", "-c", p.Script()).CombinedOutput()
	t.Logf("script output:\n%s", out)
	return string(out), err
}

func TestScriptStateRoundTrip(t *testing.T) {
	tgt := scriptTarget(t, "true")
	if m, exists, err := Load(tgt); err != nil || !exists || len(m.Packages) != 0 {
		t.Fatalf("no state yet: %+v exists=%v err=%v", m, exists, err)
	}
	want := Module{
		Packages: []Entry{{"stable", "cowsay"}, {"unstable", "gtk+3"}},
		Options:  generate.Options(0).With(generate.Unfree),
	}
	if err := SaveState(tgt, want); err != nil {
		t.Fatal(err)
	}
	if got, _, err := Load(tgt); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Load = %+v, %v; want %+v", got, err, want)
	}
	os.WriteFile(tgt.File, []byte("not json"), 0o644)
	if _, _, err := Load(tgt); err == nil {
		t.Error("a corrupt state file should be an error")
	}
}

func TestPrepareScriptCallsRemovesBeforeAdds(t *testing.T) {
	tgt := scriptTarget(t, "true")
	old := Module{Packages: []Entry{{"stable", "cowsay"}, {"stable", "htop"}, {"stable", "jq"}}}
	want := Module{
		Packages: []Entry{{"stable", "htop"}, {"unstable", "jq"}, {"unstable", "ripgrep"}},
		Options:  generate.Options(0).With(generate.Unfree).With(generate.Insecure),
	}
	p, err := Prepare(tgt, old, want, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Rebuild || !p.HasWork() {
		t.Errorf("script plans never rebuild and here have work: %+v", p)
	}
	s := p.Script()
	removes := strings.Index(s, "run remove 'stable:cowsay' 'stable:jq'")
	adds := strings.Index(s, "run add 'unstable:jq' 'unstable:ripgrep'")
	if removes < 0 || adds < 0 || removes > adds {
		t.Errorf("a changed channel is a remove plus an add, removes first:\n%s", s)
	}
	if strings.Contains(s, "htop") {
		t.Errorf("an unchanged package must not be passed to the script:\n%s", s)
	}
	if !strings.Contains(s, "LAZYFLAKES_ALLOW='unfree,insecure'") || !strings.Contains(s, "LAZYFLAKES_TARGET='mine'") {
		t.Errorf("the environment is missing:\n%s", s)
	}

	if noop, _ := Prepare(tgt, old, old, false); noop.HasWork() {
		t.Error("identical packages have nothing to do")
	}
}

func TestScriptReceivesActionPackagesAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	script := filepath.Join(dir, "hook.sh")
	os.WriteFile(script, []byte("#!/bin/sh\necho \"$LAZYFLAKES_ACTION|$LAZYFLAKES_TARGET|$LAZYFLAKES_ALLOW|$#|$*\" >> "+log+"\n"), 0o755)

	// The command may carry arguments and spaces of its own.
	tgt := scriptTarget(t, "'"+script+"' --flag")
	p, _ := Prepare(tgt, Module{Packages: []Entry{{"stable", "old"}}},
		Module{Packages: []Entry{{"stable", "a"}, {"unstable", "b c"}}, Options: generate.Options(0).With(generate.Unfree)}, false)
	if _, err := runScript(t, p); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(log)
	// $# counts the command's own --flag, the action and the packages.
	want := "remove|mine|unfree|3|--flag remove stable:old\nadd|mine|unfree|4|--flag add stable:a unstable:b c\n"
	if string(got) != want {
		t.Errorf("calls:\n%s\nwant:\n%s", got, want)
	}
}

func TestScriptFailureRecordsNothing(t *testing.T) {
	tgt := scriptTarget(t, "exit 7")
	old := Module{Packages: []Entry{{"stable", "cowsay"}}}
	SaveState(tgt, old)

	p, _ := Prepare(tgt, old, Module{Packages: []Entry{{"stable", "cowsay"}, {"stable", "jq"}}}, false)
	out, err := runScript(t, p)
	if err == nil || !strings.Contains(out, "nothing was recorded") {
		t.Fatalf("a failing script must fail the plan: err=%v\n%s", err, out)
	}
	if err := p.Done(err); err == nil {
		t.Fatal("Done must pass the failure on")
	}
	if got, _, _ := Load(tgt); !reflect.DeepEqual(got, old) {
		t.Errorf("state after a failed run = %+v, want unchanged %+v", got, old)
	}

	// And a successful one is recorded.
	tgt.Script = "true"
	p, _ = Prepare(tgt, old, Module{Packages: []Entry{{"stable", "jq"}}}, false)
	if _, err := runScript(t, p); err != nil {
		t.Fatal(err)
	}
	if err := p.Done(nil); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := Load(tgt); !reflect.DeepEqual(got.Packages, []Entry{{"stable", "jq"}}) {
		t.Errorf("state after success = %+v", got)
	}
}

// The example script from the repository edits a real file.
func TestExampleHookScript(t *testing.T) {
	example, err := filepath.Abs("../../examples/install-hook.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	packages := filepath.Join(dir, "packages.nix")
	os.WriteFile(packages, []byte("{ pkgs, pkgs-unstable, ... }: {\n  environment.systemPackages = [\n    # lazyflakes:begin\n    # lazyflakes:end\n  ];\n}\n"), 0o644)
	t.Setenv("PACKAGES_FILE", packages)

	tgt := scriptTarget(t, example)
	var current Module
	apply := func(want Module) string {
		t.Helper()
		p, err := Prepare(tgt, current, want, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runScript(t, p); err != nil {
			t.Fatal(err)
		}
		if err := p.Done(nil); err != nil {
			t.Fatal(err)
		}
		current, _, _ = Load(tgt)
		b, _ := os.ReadFile(packages)
		return string(b)
	}

	got := apply(Module{Packages: []Entry{{"stable", "ripgrep"}, {"unstable", "jq"}}})
	for _, s := range []string{"    pkgs.ripgrep\n", "    pkgs-unstable.jq\n    # lazyflakes:end\n"} {
		if !strings.Contains(got, s) {
			t.Errorf("after adding, missing %q:\n%s", s, got)
		}
	}

	// Moving jq to stable, dropping ripgrep, adding jq again must not duplicate.
	got = apply(Module{Packages: []Entry{{"stable", "jq"}}})
	if strings.Contains(got, "ripgrep") || strings.Contains(got, "pkgs-unstable.jq") || strings.Count(got, "pkgs.jq") != 1 {
		t.Errorf("after the move:\n%s", got)
	}
	if !strings.Contains(got, "# lazyflakes:begin") || !strings.Contains(got, "# lazyflakes:end") {
		t.Errorf("markers must survive:\n%s", got)
	}
}
