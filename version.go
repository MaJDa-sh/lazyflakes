package main

import (
	"fmt"
	"runtime/debug"
)

// version is set at build time with -ldflags "-X main.version=v1.2.3" by
// release builds and the Nix package.
var version = ""

// Version reports the release version, falling back to the module version
// that `go install …@v1.2.3` records, then to "dev".
func Version() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

func runVersion([]string) error {
	fmt.Println("lazyflakes", Version())
	return nil
}
