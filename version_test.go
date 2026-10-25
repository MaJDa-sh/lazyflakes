package main

import "testing"

func TestVersion(t *testing.T) {
	defer func(v string) { version = v }(version)

	version = "v1.2.3"
	if got := Version(); got != "v1.2.3" {
		t.Errorf("Version() = %q, want the build-time version", got)
	}
	version = ""
	if got := Version(); got == "" {
		t.Error("Version() must never be empty")
	}
}
