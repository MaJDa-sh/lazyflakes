package generations

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/generate"
	"github.com/MaJDa-sh/lazyflakes/internal/managed"
)

// fakeProfile makes a system profile with one generation per record (nil:
// none) in a temporary directory, the last one current, and returns the
// target, whose module lives next to it.
func fakeProfile(t *testing.T, records ...*managed.Module) *config.Target {
	t.Helper()
	dir := t.TempDir()
	tgt := &config.Target{Name: "system", Kind: "nixos", File: filepath.Join(dir, "lazyflakes.nix"),
		Attribute: "environment.systemPackages", Sets: map[string]string{"stable": "pkgs"}, Sudo: "sudo"}
	profile := filepath.Join(dir, "profiles", "system")
	os.MkdirAll(filepath.Dir(profile), 0o755)
	for i, rec := range records {
		n := i + 1
		gen := filepath.Join(dir, "store", "gen"+string(rune('0'+n)))
		os.MkdirAll(filepath.Join(gen, "bin"), 0o755)
		os.WriteFile(filepath.Join(gen, "nixos-version"), []byte("25.11.1\n"), 0o644)
		// Activating records which generation ran.
		os.WriteFile(filepath.Join(gen, "bin", "switch-to-configuration"),
			[]byte("#!/bin/sh\necho "+string(rune('0'+n))+" >"+filepath.Join(dir, "activated")+"\n"), 0o755)
		if rec != nil {
			src, err := managed.Render(tgt, *rec)
			if err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(dir, "module"+string(rune('0'+n))), []byte(src), 0o644)
			path := filepath.Join(gen, managed.RecordPath(tgt))
			os.MkdirAll(filepath.Dir(path), 0o755)
			os.WriteFile(path, []byte(recordJSON(t, *rec)), 0o644)
		}
		os.Symlink(gen, profile+"-"+string(rune('0'+n))+"-link")
	}
	os.Symlink("system-"+string(rune('0'+len(records)))+"-link", profile)
	os.Symlink(t.TempDir(), profile+"-foo-link") // another profile, not a generation
	old := systemProfile
	systemProfile = profile
	t.Cleanup(func() { systemProfile = old })
	return tgt
}

// recordJSON is the record of m as the module writes it.
func recordJSON(t *testing.T, m managed.Module) string {
	t.Helper()
	var keys []string
	for _, k := range m.Options.Keys() {
		keys = append(keys, `"`+k+`"`)
	}
	var pkgs []string
	for _, e := range m.Packages {
		pkgs = append(pkgs, `{"channel":"`+e.Channel+`","attr":"`+e.Attr+`"}`)
	}
	return `{"packages":[` + strings.Join(pkgs, ",") + `],"options":[` + strings.Join(keys, ",") + `]}`
}

func TestList(t *testing.T) {
	htop := &managed.Module{Packages: []managed.Entry{{Channel: "stable", Attr: "htop"}}}
	tgt := fakeProfile(t, nil, htop)
	gens, err := List(tgt)
	if err != nil {
		t.Fatal(err)
	}
	if len(gens) != 2 || gens[0].Number != 2 || gens[1].Number != 1 {
		t.Fatalf("generations = %+v, want 2 and 1, newest first", gens)
	}
	if !gens[0].Current || gens[1].Current || gens[0].Version != "25.11.1" {
		t.Errorf("generations = %+v", gens)
	}
	if gens[1].Record != nil || gens[0].Record == nil || !reflect.DeepEqual(*gens[0].Record, *htop) {
		t.Errorf("records: %+v, %+v", gens[0].Record, gens[1].Record)
	}
}

func TestFind(t *testing.T) {
	gens := []Generation{{Number: 7, Current: true}, {Number: 5}, {Number: 3}}
	for arg, want := range map[string]int{"": 5, "3": 3} {
		if g, err := Find(gens, arg); err != nil || g.Number != want {
			t.Errorf("Find(%q) = %d, %v; want %d", arg, g.Number, err, want)
		}
	}
	for _, arg := range []string{"7", "4", "x", "-1"} {
		if _, err := Find(gens, arg); err == nil {
			t.Errorf("Find(%q) should fail", arg)
		}
	}
	if _, err := Find([]Generation{{Number: 1, Current: true}}, ""); err == nil {
		t.Error("the oldest generation has none before it")
	}
}

func TestChanges(t *testing.T) {
	from := managed.Module{
		Packages: []managed.Entry{{Channel: "stable", Attr: "htop"}, {Channel: "stable", Attr: "jq"}},
		Settings: []managed.Setting{{Path: "a", Value: "1"}, {Path: "b", Value: "true"}},
	}
	to := managed.Module{
		Packages: []managed.Entry{{Channel: "unstable", Attr: "jq"}, {Channel: "stable", Attr: "ripgrep"}},
		Settings: []managed.Setting{{Path: "a", Value: "2"}, {Path: "c", Value: `"x"`}},
		Options:  generate.Options(0).With(generate.Unfree),
	}
	want := []string{"~jq (stable → unstable)", "+ripgrep", "−htop", "~a = 2 (was 1)", `+c = "x"`, "−b", "allow: unfree"}
	if got := Changes(from, to); !reflect.DeepEqual(got, want) {
		t.Errorf("Changes = %q\nwant      %q", got, want)
	}
	if got := Changes(to, to); len(got) != 0 {
		t.Errorf("no changes: %q", got)
	}
}

// fakeNixEnv puts a nix-env on PATH that switches a profile the way
// `nix-env --profile P --switch-generation N` does.
func fakeNixEnv(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "nix-env"), []byte(`#!/bin/sh
[ "$1" = --profile ] && [ "$3" = --switch-generation ] || exit 2
[ -e "$2-$4-link" ] || exit 1
ln -sfn "$(basename "$2")-$4-link" "$2"
`), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func run(t *testing.T, p Plan) (string, error) {
	t.Helper()
	out, err := exec.Command("sh", "-c", p.Script()).CombinedOutput()
	t.Logf("script output:\n%s", out)
	return string(out), err
}

// state is the generation the profile points to, the one last activated
// and the module's content.
func state(t *testing.T, tgt *config.Target) (profile, activated, module string) {
	t.Helper()
	dir := filepath.Dir(tgt.File)
	link, _ := os.Readlink(systemProfile)
	act, _ := os.ReadFile(filepath.Join(dir, "activated"))
	mod, _ := os.ReadFile(tgt.File)
	return link, strings.TrimSpace(string(act)), string(mod)
}

func TestRollbackRestoresTheModule(t *testing.T) {
	fakeNixEnv(t)
	one := &managed.Module{Packages: []managed.Entry{{Channel: "stable", Attr: "htop"}}}
	two := &managed.Module{Packages: []managed.Entry{{Channel: "stable", Attr: "htop"}, {Channel: "stable", Attr: "jq"}}}
	tgt := fakeProfile(t, one, two)
	cur, _ := os.ReadFile(filepath.Join(filepath.Dir(tgt.File), "module2"))
	os.WriteFile(tgt.File, cur, 0o644)

	gens, _ := List(tgt)
	to, _ := Find(gens, "")
	p, err := Prepare(tgt, gens, to)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Cleanup()
	if p.Module != Restore || p.From.Number != 2 || p.To.Number != 1 {
		t.Fatalf("plan = %+v", p)
	}
	if _, err := run(t, p); err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(filepath.Join(filepath.Dir(tgt.File), "module1"))
	if profile, activated, module := state(t, tgt); profile != "system-1-link" || activated != "1" || module != string(want) {
		t.Errorf("profile=%s activated=%s module=\n%s\nwant generation 1 and its module:\n%s", profile, activated, module, want)
	}
	// What the module now says is what lazyflakes reads back.
	if m, _, _ := managed.Load(tgt); !reflect.DeepEqual(m.Packages, one.Packages) {
		t.Errorf("module packages = %+v", m.Packages)
	}
}

func TestRollbackSwitchesBackWhenTheModuleCannotBeWritten(t *testing.T) {
	fakeNixEnv(t)
	one := &managed.Module{Packages: []managed.Entry{{Channel: "stable", Attr: "htop"}}}
	tgt := fakeProfile(t, one, &managed.Module{})
	os.WriteFile(tgt.File, []byte("current\n"), 0o644)
	gens, _ := List(tgt)
	p, err := Prepare(tgt, gens, gens[1])
	if err != nil {
		t.Fatal(err)
	}
	defer p.Cleanup()
	// install, which writes the module, fails.
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "install"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	out, err := run(t, p)
	if err == nil || !strings.Contains(out, "switched back to generation 2") {
		t.Fatalf("err = %v", err)
	}
	if profile, activated, module := state(t, tgt); profile != "system-2-link" || activated != "" || module != "current\n" {
		t.Errorf("nothing may change: profile=%s activated=%q module=%q", profile, activated, module)
	}
}

func TestRollbackKeepsAgreementWhenActivationFails(t *testing.T) {
	fakeNixEnv(t)
	one := &managed.Module{Packages: []managed.Entry{{Channel: "stable", Attr: "htop"}}}
	tgt := fakeProfile(t, one, &managed.Module{})
	os.WriteFile(tgt.File, []byte("current\n"), 0o644)
	gens, _ := List(tgt)
	os.WriteFile(filepath.Join(gens[1].Link, "bin", "switch-to-configuration"), []byte("#!/bin/sh\nexit 4\n"), 0o755)
	p, _ := Prepare(tgt, gens, gens[1])
	defer p.Cleanup()

	out, err := run(t, p)
	if err == nil || !strings.Contains(out, "reported errors") {
		t.Fatalf("err = %v", err)
	}
	// Like nixos-rebuild: the generation stays the default, and the module
	// matches it so a rebuild does not undo the rollback.
	want, _ := os.ReadFile(filepath.Join(filepath.Dir(tgt.File), "module1"))
	if profile, _, module := state(t, tgt); profile != "system-1-link" || module != string(want) {
		t.Errorf("profile=%s module=%q", profile, module)
	}
}

func TestRollbackWithoutRecord(t *testing.T) {
	fakeNixEnv(t)
	tgt := fakeProfile(t, nil, &managed.Module{})
	os.WriteFile(tgt.File, []byte("current\n"), 0o644)
	gens, _ := List(tgt)
	p, _ := Prepare(tgt, gens, gens[1])
	defer p.Cleanup()
	if p.Module != NoRecord {
		t.Fatalf("module = %v", p.Module)
	}
	out, err := run(t, p)
	if err != nil || !strings.Contains(out, "no record") {
		t.Fatalf("err = %v", err)
	}
	if profile, activated, module := state(t, tgt); profile != "system-1-link" || activated != "1" || module != "current\n" {
		t.Errorf("profile=%s activated=%s module=%q", profile, activated, module)
	}
}

func TestRollbackModuleAlreadyRight(t *testing.T) {
	one := &managed.Module{Packages: []managed.Entry{{Channel: "stable", Attr: "htop"}}}
	tgt := fakeProfile(t, one, one)
	cur, _ := os.ReadFile(filepath.Join(filepath.Dir(tgt.File), "module1"))
	os.WriteFile(tgt.File, cur, 0o644)
	gens, _ := List(tgt)
	if p, _ := Prepare(tgt, gens, gens[1]); p.Module != Unchanged {
		t.Errorf("module = %v, want Unchanged", p.Module)
	}
	if _, err := Prepare(tgt, gens, gens[0]); err == nil {
		t.Error("the current generation cannot be rolled back to")
	}
}

func TestActivateCommands(t *testing.T) {
	gen := Generation{Number: 3, Link: "/p/system-3-link"}
	for kind, want := range map[string]string{
		"nixos":        `sudo '/p/system-3-link/bin/switch-to-configuration' switch`,
		"nix-darwin":   `{ [ ! -x '/p/system-3-link/activate-user' ] || '/p/system-3-link/activate-user'; } && sudo '/p/system-3-link/activate'`,
		"home-manager": `'/p/system-3-link/activate'`,
	} {
		p := Plan{Target: &config.Target{Kind: kind}, To: gen}
		sudo := "sudo "
		if kind == "home-manager" {
			sudo = ""
		}
		if got := p.activate(sudo); got != want {
			t.Errorf("%s: %s\nwant %s", kind, got, want)
		}
	}
}

func TestScriptTargetsHaveNoGenerations(t *testing.T) {
	if _, err := List(&config.Target{Name: "mine", Kind: config.KindScript}); err == nil {
		t.Error("expected an error")
	}
}
