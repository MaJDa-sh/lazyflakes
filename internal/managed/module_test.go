package managed

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/generate"
)

func target(file string) *config.Target {
	return &config.Target{
		Name: "system", Kind: "nixos", File: file, Attribute: "environment.systemPackages",
		Sets: map[string]string{
			"stable":   "pkgs",
			"unstable": `import (fetchTarball "https://example.org/unstable.tar.gz") { }`,
			"edge":     "pkgs-edge",
		},
	}
}

func TestRenderParseRoundTrip(t *testing.T) {
	m := Module{
		Packages: []Entry{{"stable", "ripgrep"}, {"unstable", "jq"}, {"edge", "python3Packages.requests"}, {"stable", "gtk+3"}},
		Options:  generate.Options(0).With(generate.Unfree).With(generate.Insecure),
	}
	src, err := Render(target("/etc/nixos/lazyflakes.nix"), m)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		"imports = [ ./lazyflakes.nix ];",
		"{ config, lib, pkgs, pkgs-edge, ... }:",
		`  lazyflakes-unstable = import (fetchTarball "https://example.org/unstable.tar.gz") { };`,
		"  nixpkgs.config.allowUnfree = lib.mkDefault (true); # lazyflakes:option unfree",
		"  nixpkgs.config.allowInsecurePredicate = lib.mkDefault (_: true); # lazyflakes:option insecure",
		"    pkgs.ripgrep # lazyflakes:pkg stable ripgrep",
		"    lazyflakes-unstable.jq # lazyflakes:pkg unstable jq",
		"    pkgs-edge.python3Packages.requests # lazyflakes:pkg edge python3Packages.requests",
		`    pkgs."gtk+3" # lazyflakes:pkg stable gtk+3`,
	} {
		if !strings.Contains(src, s) {
			t.Errorf("missing %q in:\n%s", s, src)
		}
	}
	if got := Parse(src); !reflect.DeepEqual(got, m) {
		t.Errorf("Parse(Render(m)) = %+v, want %+v", got, m)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	m := Module{
		Packages: []Entry{{"stable", "ripgrep"}},
		Options:  generate.Options(0).With(generate.Unfree),
		Settings: []Setting{
			{Path: "services.openssh.enable", Value: "true"},
			{Path: "networking.hostName", Value: `"my box"`},
			{Path: `systemd.services."odd name".enable`, Value: "false"},
			{Path: "services.xserver.videoDrivers", Value: `[ "nvidia" "amdgpu" ]`},
		},
	}
	src, err := Render(target("/etc/nixos/lazyflakes.nix"), m)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{
		"  services.openssh.enable = true; # lazyflakes:set services.openssh.enable = true\n",
		`  networking.hostName = "my box"; # lazyflakes:set networking.hostName = "my box"` + "\n",
		`  systemd.services."odd name".enable = false; # lazyflakes:set systemd.services."odd name".enable = false` + "\n",
	} {
		if !strings.Contains(src, s) {
			t.Errorf("missing %q in:\n%s", s, src)
		}
	}
	if strings.Contains(src, "mkDefault (true); # lazyflakes:set") || strings.Contains(src, "lib.mkDefault services") {
		t.Errorf("settings must be plain assignments:\n%s", src)
	}
	if got := Parse(src); !reflect.DeepEqual(got, m) {
		t.Errorf("Parse(Render(m)) = %+v, want %+v", got, m)
	}
}

func TestRenderRejectsBadSettings(t *testing.T) {
	tgt := target("/x/lazyflakes.nix")
	for name, settings := range map[string][]Setting{
		"empty value":    {{Path: "a.b", Value: " "}},
		"empty path":     {{Path: "", Value: "true"}},
		"multi-line":     {{Path: "a.b", Value: "[\n 1\n]"}},
		"duplicate path": {{Path: "a.b", Value: "1"}, {Path: "a.b", Value: "2"}},
		"bad name":       {{Path: "a = b", Value: "1"}},
	} {
		if _, err := Render(tgt, Module{Settings: settings}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// A value may hold marker text: it is read back as written, not as the
// setting or package its text names.
func TestSettingsRoundTripWithMarkerText(t *testing.T) {
	m := Module{Settings: []Setting{
		{Path: "a", Value: `"x # lazyflakes:set evil = 1"`},
		{Path: "b", Value: `"# lazyflakes:pkg stable evil"`},
		{Path: "c", Value: `"x; # lazyflakes:set c = y"`},
	}}
	src, err := Render(target("/x/lazyflakes.nix"), m)
	if err != nil {
		t.Fatal(err)
	}
	if got := Parse(src); !reflect.DeepEqual(got, m) {
		t.Errorf("Parse(Render(m)) = %+v, want %+v", got, m)
	}
}

// A line edited by hand is still read from its marker.
func TestParseHandEditedSetting(t *testing.T) {
	got := Parse("  a = 2; # lazyflakes:set a = 1\n")
	if want := []Setting{{Path: "a", Value: "1"}}; !reflect.DeepEqual(got.Settings, want) {
		t.Errorf("settings = %+v, want %+v", got.Settings, want)
	}
}

func TestRenderRejectsBadPackages(t *testing.T) {
	tgt := target("/x/lazyflakes.nix")
	tgt.Sets["my set"] = "pkgs"
	for _, e := range []Entry{
		{"stable", "foo bar"}, {"stable", "foo\n];"}, {"stable", `a"b`}, {"stable", "a#b"},
		{"stable", "a.${b}"}, {"stable", ""}, {"stable", "a..b"}, {"stable", ".a"}, {"my set", "jq"},
	} {
		if _, err := Render(tgt, Module{Packages: []Entry{e}}); err == nil {
			t.Errorf("%+v: expected an error", e)
		}
	}
	for _, attr := range []string{"gtk+3", "haskellPackages.3d", "_1password-gui", "python3Packages.requests"} {
		if _, err := Render(tgt, Module{Packages: []Entry{{"stable", attr}}}); err != nil {
			t.Errorf("%s: %v", attr, err)
		}
	}
}

// A setting that names a package set gets it as a module argument, even
// when no package comes from that set.
func TestRenderArgumentsForSettings(t *testing.T) {
	tgt := target("/x/lazyflakes.nix")
	tgt.Sets["unstable"] = "pkgs-unstable"
	src, err := Render(tgt, Module{Settings: []Setting{{Path: "programs.git.package", Value: "pkgs-unstable.git"}}})
	if err != nil || !strings.Contains(src, "{ config, lib, pkgs, pkgs-unstable, ... }:") {
		t.Errorf("err = %v\n%s", err, src)
	}
	src, _ = Render(tgt, Module{Settings: []Setting{{Path: "a", Value: "my-pkgs-unstable.git"}}})
	if strings.Contains(src, "pkgs-unstable, ...") {
		t.Errorf("only the set itself counts:\n%s", src)
	}
}

func TestRenderEmptyAndMissingSet(t *testing.T) {
	src, err := Render(target("/x/lazyflakes.nix"), Module{})
	if err != nil || !strings.Contains(src, "environment.systemPackages = [\n  ];") || strings.Contains(src, "let") {
		t.Errorf("empty module: err=%v\n%s", err, src)
	}
	_, err = Render(target("/x/lazyflakes.nix"), Module{Packages: []Entry{{"nightly", "jq"}}})
	if err == nil || !strings.Contains(err.Error(), "[targets.system.sets]") {
		t.Errorf("missing set: err = %v", err)
	}
}

func TestReadAndImported(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "lazyflakes.nix")
	if _, exists, err := Read(file); exists || err != nil {
		t.Fatalf("missing file: exists=%v err=%v", exists, err)
	}
	os.WriteFile(file, []byte("x # lazyflakes:pkg stable jq\n"), 0o644)
	if m, exists, _ := Read(file); !exists || len(m.Packages) != 1 {
		t.Errorf("Read = %+v %v", m, exists)
	}

	if Imported(target(file)) {
		t.Error("nothing imports the module yet")
	}
	os.MkdirAll(filepath.Join(dir, "hosts", "laptop"), 0o755)
	os.WriteFile(filepath.Join(dir, "hosts", "laptop", "default.nix"), []byte("{ imports = [ ../../lazyflakes.nix ]; }"), 0o644)
	if !Imported(target(file)) {
		t.Error("module imported from hosts/laptop/default.nix was not found")
	}
}

// runPlan executes the plan's script and returns its error.
func runPlan(t *testing.T, p Plan) error {
	t.Helper()
	cmd := exec.Command("sh", "-c", p.Script())
	out, err := cmd.CombinedOutput()
	t.Logf("script output:\n%s", out)
	return err
}

func TestApplyScript(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "lazyflakes.nix")
	read := func() string { b, _ := os.ReadFile(file); return string(b) }
	tgt := target(file)

	// Success writes the file and runs the rebuild.
	tgt.Rebuild = "touch " + filepath.Join(dir, "rebuilt")
	p, _ := NewPlan(tgt, "new\n", true)
	defer p.Cleanup()
	if p.Sudo || p.GitAdd {
		t.Fatalf("temp dir plan should need neither sudo nor git: %+v", p)
	}
	if err := runPlan(t, p); err != nil || read() != "new\n" {
		t.Fatalf("apply: err=%v content=%q", err, read())
	}
	if _, err := os.Stat(filepath.Join(dir, "rebuilt")); err != nil {
		t.Error("rebuild command did not run")
	}

	// A failed rebuild restores the previous content.
	tgt.Rebuild = "exit 3"
	p2, _ := NewPlan(tgt, "broken\n", true)
	defer p2.Cleanup()
	if err := runPlan(t, p2); err == nil || read() != "new\n" {
		t.Errorf("failed rebuild: err=%v content=%q (want previous content)", err, read())
	}

	// Writing without rebuilding skips the command.
	p3, _ := NewPlan(tgt, "written\n", false)
	defer p3.Cleanup()
	if err := runPlan(t, p3); err != nil || read() != "written\n" {
		t.Errorf("write only: err=%v content=%q", err, read())
	}

	// A failed rebuild of a brand new module removes it again.
	os.Remove(file)
	p4, _ := NewPlan(tgt, "first\n", true)
	defer p4.Cleanup()
	if err := runPlan(t, p4); err == nil {
		t.Error("expected failure")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("new module should be removed after a failed rebuild, stat err = %v", err)
	}

	// Without a backup nothing is written: a failed rebuild could not
	// restore the module and would delete it as if it were new.
	os.WriteFile(file, []byte("kept\n"), 0o644)
	p5, _ := NewPlan(tgt, "unsafe\n", true)
	defer p5.Cleanup()
	t.Setenv("TMPDIR", filepath.Join(dir, "missing")) // mktemp fails
	if err := runPlan(t, p5); err == nil || read() != "kept\n" {
		t.Errorf("no backup: err=%v content=%q (want the module untouched)", err, read())
	}
}

func TestApplyScriptGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		out, _ := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		return string(out)
	}
	git("init", "-q")
	file := filepath.Join(dir, "lazyflakes.nix")
	tgt := target(file)

	tgt.Rebuild = "exit 1"
	p, _ := NewPlan(tgt, "x\n", true)
	defer p.Cleanup()
	if !p.GitAdd {
		t.Fatal("untracked module in a git repo should be git-added")
	}
	runPlan(t, p)
	if strings.TrimSpace(git("ls-files")) != "" {
		t.Errorf("failed first apply should leave the index clean, ls-files: %q", git("ls-files"))
	}

	tgt.Rebuild = "true"
	p2, _ := NewPlan(tgt, "y\n", true)
	defer p2.Cleanup()
	if err := runPlan(t, p2); err != nil || strings.TrimSpace(git("ls-files")) != "lazyflakes.nix" {
		t.Errorf("module should be tracked after apply: err=%v ls-files=%q", err, git("ls-files"))
	}
}

func TestRenderWithoutNixpkgsConfig(t *testing.T) {
	off := false
	tgt := target("/x/lazyflakes.nix")
	tgt.NixpkgsConfig = &off
	src, err := Render(tgt, Module{Packages: []Entry{{"stable", "unrar"}}, Options: generate.Options(0).With(generate.Unfree)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(src, "nixpkgs.config") || !strings.Contains(src, "pkgs.unrar") {
		t.Errorf("nixpkgs_config = false must not write nixpkgs.config:\n%s", src)
	}
}

func TestApplyScriptUsesConfiguredSudo(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "sudo.log")
	fake := filepath.Join(dir, "doas")
	os.WriteFile(fake, []byte("#!/bin/sh\necho \"$*\" >> "+log+"\nexec \"$@\"\n"), 0o755)

	tgt := target(filepath.Join(dir, "lazyflakes.nix"))
	tgt.Sudo = fake
	tgt.Rebuild = "exit 1"
	p, _ := NewPlan(tgt, "new\n", true)
	defer p.Cleanup()
	p.Sudo = true // pretend the file is root-owned
	runPlan(t, p)

	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "install -m 0644") || !strings.Contains(string(calls), "rm -f") {
		t.Errorf("writing and restoring should go through the configured command, calls:\n%s", calls)
	}
	if strings.Contains(p.Script(), "sudo ") {
		t.Errorf("script still uses sudo:\n%s", p.Script())
	}
}

func TestScriptMarksRepoSafe(t *testing.T) {
	dir := t.TempDir()
	p := Plan{Target: target(filepath.Join(dir, "lazyflakes.nix")), NewFile: "/tmp/x", GitAdd: true, Rebuild: true}
	p.Target.Rebuild = "true"
	if !strings.Contains(p.Script(), "git -c safe.directory='"+dir+"' -C '"+dir+"' add --intent-to-add") {
		t.Errorf("git in the script must mark the repo safe:\n%s", p.Script())
	}
}

// A writable module in a directory the user cannot write (a root-owned
// /etc/nixos with the module chowned to the user) still needs sudo:
// install removes the old file before it writes the new one.
func TestNewPlanSudoForReadOnlyDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "lazyflakes.nix")
	os.WriteFile(file, []byte("{ }\n"), 0o666)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })

	plan, err := NewPlan(target(file), "{ }\n", false)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if !plan.Sudo {
		t.Error("the directory is not writable, so sudo is needed")
	}
}

func TestReadRecord(t *testing.T) {
	m := Module{
		Packages: []Entry{{"stable", "htop"}},
		Options:  generate.Options(0).With(generate.Unfree),
		Settings: []Setting{{Path: "a", Value: `"x # lazyflakes:set evil = 1"`}},
	}
	for _, tgt := range []*config.Target{
		target("/x/lazyflakes.nix"),
		{Name: "home", Kind: "home-manager", File: "/h/lazyflakes.nix", Attribute: "home.packages", Sets: map[string]string{"stable": "pkgs"}},
	} {
		src, err := Render(tgt, m)
		if err != nil {
			t.Fatal(err)
		}
		// The module line holds the record as a Nix string of JSON.
		line := regexp.MustCompile(`(?m)^  (\S+)\.text = (".*");$`).FindStringSubmatch(src)
		if line == nil || strings.Count(line[0], "#") != 0 {
			t.Fatalf("%s: no record line without #:\n%s", tgt.Kind, src)
		}
		gen := t.TempDir()
		path := filepath.Join(gen, RecordPath(tgt))
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(unNix(t, line[2])), 0o644)
		if got, ok, err := ReadRecord(tgt, gen); err != nil || !ok || !reflect.DeepEqual(got, m) {
			t.Errorf("%s: record = %+v, %v, %v; want %+v", tgt.Kind, got, ok, err, m)
		}
	}
	if want := filepath.Join("home-files", ".local", "share", "lazyflakes", "home.json"); RecordPath(&config.Target{Name: "home", Kind: "home-manager"}) != want {
		t.Errorf("home-manager record path = %s", RecordPath(&config.Target{Name: "home", Kind: "home-manager"}))
	}
	if _, ok, err := ReadRecord(target("/x/lazyflakes.nix"), t.TempDir()); ok || err != nil {
		t.Errorf("a generation without a record: ok=%v err=%v", ok, err)
	}
}

// unNix reads back a string written by generate.NixString.
func unNix(t *testing.T, s string) string {
	t.Helper()
	s = strings.TrimSuffix(strings.TrimPrefix(s, `"`), `"`)
	return strings.NewReplacer(`\\`, `\`, `\"`, `"`, `\$`, `$`, `\n`, "\n", `\t`, "\t", `\r`, "\r").Replace(s)
}
