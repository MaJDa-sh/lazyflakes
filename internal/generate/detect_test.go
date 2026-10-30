package generate

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDetectProject(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}\n"), 0o644)

	tools := DetectProject(dir)
	attrs := make([]string, len(tools))
	for i, t := range tools {
		attrs[i] = t.Attr
	}
	if !slices.Contains(attrs, "go") || !slices.Contains(attrs, "nodejs") {
		t.Errorf("expected go and nodejs, got %v", attrs)
	}
	// Each attr appears only once even with multiple Python markers.
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "pyproject.toml"), nil, 0o644)
	os.WriteFile(filepath.Join(dir2, "requirements.txt"), nil, 0o644)
	tools2 := DetectProject(dir2)
	count := 0
	for _, t := range tools2 {
		if t.Attr == "python3" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("python3 should appear once, got %d in %v", count, tools2)
	}
}

func TestDetectProjectEmpty(t *testing.T) {
	if tools := DetectProject(t.TempDir()); len(tools) != 0 {
		t.Errorf("empty dir: %v", tools)
	}
}
