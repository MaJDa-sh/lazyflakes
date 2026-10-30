package options

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func load(t *testing.T) map[string]Option {
	t.Helper()
	f, err := os.Open("testdata/options.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	opts, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Option{}
	for i, o := range opts {
		m[o.Name] = o
		if i > 0 && opts[i-1].Name > o.Name {
			t.Errorf("options are not sorted: %s before %s", opts[i-1].Name, o.Name)
		}
	}
	return m
}

func TestParseAndClassify(t *testing.T) {
	m := load(t)
	cases := []struct {
		name     string
		kind     Kind
		nullable bool
		choices  []string
	}{
		{"services.openssh.enable", Bool, false, nil},
		{"services.openssh.settings.PermitRootLogin", Enum, true, []string{"yes", "without-password", "prohibit-password", "forced-commands-only", "no"}},
		{"networking.hostName", String, false, nil},
		{"services.openssh.ports", Other, false, nil}, // a list of integers is not a list of strings
		{"services.openssh.banner", String, true, nil},
		{"services.xserver.videoDrivers", StringList, false, nil},
		{"boot.loader.grub.timeout", Int, true, nil},
		{"services.postgresql.dataDir", Path, false, nil},
		{"programs.git.config", Other, false, nil},
		{"services.foo.size", Other, false, nil}, // a union needs an expression
		{"programs.git.package", Package, true, nil},
		{"environment.systemPackages", PackageList, false, nil},
	}
	for _, c := range cases {
		o, ok := m[c.name]
		if !ok {
			t.Errorf("%s missing", c.name)
			continue
		}
		if o.Kind != c.kind || o.Nullable != c.nullable || !reflect.DeepEqual(o.Choices, c.choices) {
			t.Errorf("%s: kind=%v nullable=%v choices=%v, want %v %v %v", c.name, o.Kind, o.Nullable, o.Choices, c.kind, c.nullable, c.choices)
		}
	}

	ssh := m["services.openssh.enable"]
	if ssh.Default != "false" || ssh.Declared != "nixos/modules/services/networking/ssh/sshd.nix" || ssh.Description != "Whether to enable the OpenSSH secure shell daemon, which allows secure remote logins." {
		t.Errorf("openssh.enable = %+v", ssh)
	}
	if got := m["networking.hostName"].Declared; got != "nixos/modules/tasks/network-interfaces.nix" {
		t.Errorf("object declarations: %q", got)
	}
	if got := m["boot.loader.grub.timeout"].Default; got != "5" {
		t.Errorf("a plain default is kept as JSON: %q", got)
	}
	if got := m["services.xserver.videoDrivers"].Example; got != `[ "nvidia" "amdgpu" ]` {
		t.Errorf("example = %q", got)
	}
	desc := m["services.openssh.settings.PermitRootLogin"].Description
	if strings.Contains(desc, "{manpage}") || strings.Contains(desc, "`") || strings.Contains(desc, "\n\n\n") ||
		!strings.Contains(desc, "See sshd_config(5) and services.openssh.enable.") {
		t.Errorf("markdown was not cleaned: %q", desc)
	}
}

func TestSettable(t *testing.T) {
	m := load(t)
	for name, want := range map[string]bool{
		"services.openssh.enable":                 true,
		"_module.args":                            false, // the module system's own
		"system.build.toplevel":                   false, // read only
		"services.nginx.virtualHosts.<name>.root": false, // needs a name first
		"systemd.services.\"odd name\".enable":    true,
	} {
		if got := m[name].Settable(); got != want {
			t.Errorf("%s: Settable = %v, want %v", name, got, want)
		}
	}
}

func TestPathQuotesOddSegments(t *testing.T) {
	m := load(t)
	if got := m["services.openssh.settings.PermitRootLogin"].Path(); got != "services.openssh.settings.PermitRootLogin" {
		t.Errorf("Path = %q", got)
	}
	if got := m[`systemd.services."odd name".enable`].Path(); got != `systemd.services."odd name".enable` {
		t.Errorf("Path = %q", got)
	}
	if got := (Option{Name: "a.b-c.d'e"}).Path(); got != "a.b-c.d'e" {
		t.Errorf("a name without loc is split on dots: %q", got)
	}
}

func TestFormat(t *testing.T) {
	m := load(t)
	ok := []struct {
		option, input string
		raw           bool
		want          string
	}{
		{"services.openssh.enable", "true", false, "true"},
		{"services.openssh.enable", " No ", false, "false"},
		{"services.openssh.settings.PermitRootLogin", "no", false, `"no"`},
		{"services.openssh.settings.PermitRootLogin", "null", false, "null"},
		{"networking.hostName", `my "box" ${x}\`, false, `"my \"box\" \${x}\\"`},
		{"networking.hostName", "", false, `""`},
		{"boot.loader.grub.timeout", "-1", false, "-1"},
		{"boot.loader.grub.timeout", "null", false, "null"},
		{"services.postgresql.dataDir", "/var/lib/pg", false, `"/var/lib/pg"`},
		{"services.xserver.videoDrivers", "nvidia, amdgpu", false, `[ "nvidia" "amdgpu" ]`},
		{"services.xserver.videoDrivers", "", false, "[ ]"},
		{"services.openssh.ports", "[ 22 2222 ]", false, "[ 22 2222 ]"}, // Other is always raw
		{"networking.hostName", "config.networking.domain", true, "config.networking.domain"},
		{"programs.git.package", "gitFull", false, "pkgs.gitFull"},
		{"programs.git.package", "python3Packages.requests", false, "pkgs.python3Packages.requests"},
		{"programs.git.package", "pkgs-unstable.git", false, "pkgs-unstable.git"},
		// Packages whose names start with "pkgs" are not package sets.
		{"programs.git.package", "pkgsite", false, "pkgs.pkgsite"},
		{"programs.git.package", "pkgsStatic.git", false, "pkgs.pkgsStatic.git"},
		{"programs.git.package", "pkgsUnknown.git", false, "pkgs.pkgsUnknown.git"},
		{"programs.git.package", "null", false, "null"},
		{"environment.systemPackages", "htop, pkgs.jq", false, "[ pkgs.htop pkgs.jq ]"},
	}
	for _, c := range ok {
		got, err := m[c.option].Format(c.input, c.raw, []string{"pkgs-unstable"})
		if err != nil || got != c.want {
			t.Errorf("%s %q: got %q, %v; want %q", c.option, c.input, got, err, c.want)
		}
	}

	bad := []struct{ option, input string }{
		{"services.openssh.enable", "maybe"},
		{"services.openssh.enable", ""},
		{"services.openssh.settings.PermitRootLogin", "sometimes"},
		{"boot.loader.grub.timeout", "five"},
		{"services.openssh.enable", "null"}, // not nullable
		{"services.postgresql.dataDir", "relative/path"},
		{"networking.hostName", "two\nlines"},
		{"programs.git.package", "git; rm -rf"},
		{"environment.systemPackages", "htop, not a package"},
	}
	for _, c := range bad {
		if got, err := m[c.option].Format(c.input, false, nil); err == nil {
			t.Errorf("%s %q should be rejected, got %q", c.option, c.input, got)
		}
	}
}

func TestCheckSyntax(t *testing.T) {
	if _, err := exec.LookPath("nix-instantiate"); err != nil {
		t.Skip("nix-instantiate is not installed")
	}
	if err := CheckSyntax(`[ "a" "b" ]`); err != nil {
		t.Errorf("valid expression rejected: %v", err)
	}
	if err := CheckSyntax(`[ "a" `); err == nil || !strings.HasPrefix(err.Error(), "not valid Nix") {
		t.Errorf("invalid expression accepted, err = %v", err)
	}
	// Negative numbers are expressions, not flags of nix-instantiate.
	if err := CheckSyntax(`-1`); err != nil {
		t.Errorf("-1 rejected: %v", err)
	}
	// The module writes `path = value; # marker` on one line, where a
	// comment would swallow the ; and break the whole file.
	if err := CheckSyntax(`true # note`); err == nil || !strings.Contains(err.Error(), "comment") {
		t.Errorf("a trailing comment must be refused, err = %v", err)
	}
	if err := CheckSyntax(`"a # b"`); err != nil {
		t.Errorf("a # inside a string is fine: %v", err)
	}
	// Nothing may follow the value: it has to be one expression.
	if err := CheckSyntax(`true; y = 1`); err == nil {
		t.Error("a second assignment must be refused")
	}
}

func TestCheckPath(t *testing.T) {
	if _, err := exec.LookPath("nix-instantiate"); err != nil {
		t.Skip("nix-instantiate is not installed")
	}
	for _, ok := range []string{"services.openssh.enable", `users.users."a#b".isNormalUser`} {
		if err := CheckPath(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	// Each of these would add something else to the module's line.
	for _, bad := range []string{"a=null;b", "a # c", "a or b", "a b", ""} {
		if err := CheckPath(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
}

func TestRelease(t *testing.T) {
	for in, want := range map[string]string{
		"25.11":                     "25.11",
		"25.11.9586.10e7ad5bbcb4\n": "25.11",
		"26.05-pre":                 "",
		"26.05pre1234.abc":          "",
		"unstable":                  "",
		"":                          "",
	} {
		if got := release(in); got != want {
			t.Errorf("release(%q) = %q, want %q", in, got, want)
		}
	}
}
