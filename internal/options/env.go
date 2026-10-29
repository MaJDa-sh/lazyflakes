package options

import (
	"os/exec"
	"regexp"
	"strings"
)

var releaseRe = regexp.MustCompile(`^(\d\d\.\d\d)`)

// DetectEnv looks at the machine: the home-manager release that is installed,
// so its options match what the user runs.
func DetectEnv(fallbackNixpkgs string) Env {
	env := Env{FallbackNixpkgs: fallbackNixpkgs}
	if out, err := exec.Command("home-manager", "--version").Output(); err == nil {
		env.HomeManagerRelease = release(string(out))
	}
	return env
}

// release is the YY.MM of a released version such as "25.11" or
// "25.11.9586.10e7ad5bbcb4", and "" for a development version such as
// "26.05-pre".
func release(version string) string {
	version = strings.TrimSpace(version)
	m := releaseRe.FindStringSubmatch(version)
	if m == nil || strings.Contains(version, "pre") {
		return ""
	}
	return m[1]
}
