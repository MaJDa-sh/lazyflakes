package generate

import (
	"os"
	"path/filepath"
)

// ProjectTool is a development tool detected from a project file.
type ProjectTool struct {
	Attr     string // the nixpkgs attribute, e.g. "go"
	Label    string // shown to the user, e.g. "Go (go.mod)"
	FromFile string // the file that triggered the detection
}

// DetectProject looks at dir for language markers and returns the
// toolchains it finds. The order follows how common the language is; each
// file is checked with stat, not read.
func DetectProject(dir string) []ProjectTool {
	var tools []ProjectTool
	check := func(file, attr, label string) {
		if _, err := os.Stat(filepath.Join(dir, file)); err == nil {
			tools = append(tools, ProjectTool{Attr: attr, Label: label, FromFile: file})
		}
	}

	// JavaScript / TypeScript
	check("package.json", "nodejs", "Node.js (package.json)")
	if has(dir, "bun.lock", "bun.lockb", "bunfig.toml") {
		tools = append(tools, ProjectTool{Attr: "bun", Label: "Bun", FromFile: "bun.lock"})
	}
	if has(dir, "deno.json", "deno.jsonc", "deno.lock") {
		tools = append(tools, ProjectTool{Attr: "deno", Label: "Deno", FromFile: "deno.json"})
	}

	// Python
	check("pyproject.toml", "python3", "Python (pyproject.toml)")
	check("setup.py", "python3", "Python (setup.py)")
	check("requirements.txt", "python3", "Python (requirements.txt)")
	check("Pipfile", "python3", "Python (Pipfile)")

	// Rust
	check("Cargo.toml", "cargo", "Rust (Cargo.toml)")

	// Go
	check("go.mod", "go", "Go (go.mod)")

	// Java / Kotlin / Scala
	check("pom.xml", "maven", "Java (pom.xml)")
	check("build.gradle", "gradle", "Java/Kotlin (build.gradle)")
	check("build.gradle.kts", "gradle", "Kotlin (build.gradle.kts)")
	check("build.sbt", "sbt", "Scala (build.sbt)")

	// Ruby
	check("Gemfile", "ruby", "Ruby (Gemfile)")

	// PHP
	check("composer.json", "php", "PHP (composer.json)")

	// .NET
	if hasGlob(dir, "*.csproj") || hasGlob(dir, "*.fsproj") || has(dir, "Directory.Build.props") {
		tools = append(tools, ProjectTool{Attr: "dotnet-sdk", Label: ".NET (*.csproj)", FromFile: "*.csproj"})
	}

	// Haskell
	check("stack.yaml", "stack", "Haskell (stack.yaml)")
	if has(dir, "cabal.project") || hasGlob(dir, "*.cabal") {
		tools = append(tools, ProjectTool{Attr: "cabal-install", Label: "Haskell (*.cabal)", FromFile: "*.cabal"})
	}

	// Zig
	check("build.zig", "zig", "Zig (build.zig)")

	// Elixir
	check("mix.exs", "elixir", "Elixir (mix.exs)")

	// C/C++
	check("CMakeLists.txt", "cmake", "C/C++ (CMakeLists.txt)")
	check("Makefile", "gnumake", "Make (Makefile)")
	check("meson.build", "meson", "Meson (meson.build)")

	// Flutter / Dart
	check("pubspec.yaml", "flutter", "Flutter/Dart (pubspec.yaml)")

	// Nix — no package to add, just recognition
	// (the user is already in the right place)

	// Deduplicate by attr (e.g. multiple Python markers).
	seen := map[string]bool{}
	var deduped []ProjectTool
	for _, t := range tools {
		if !seen[t.Attr] {
			seen[t.Attr] = true
			deduped = append(deduped, t)
		}
	}
	return deduped
}

func has(dir string, files ...string) bool {
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}

func hasGlob(dir, pattern string) bool {
	matches, _ := filepath.Glob(filepath.Join(dir, pattern))
	return len(matches) > 0
}
