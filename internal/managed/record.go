package managed

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/generate"
)

// Every generation built from the module carries a record of what
// lazyflakes installed and set in it, so rolling back to a generation can
// also put lazyflakes.nix back the way it was: otherwise the next rebuild
// would bring the packages of the newer generation right back.

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

// recordName is the record's file name: the target's name, made safe for a
// path and a quoted Nix attribute.
func recordName(t *config.Target) string {
	return unsafeName.ReplaceAllString(t.Name, "_") + ".json"
}

// RecordPath is where a generation of target t holds its record, relative
// to the generation's directory: an /etc file of the system, or a file in
// the home directory for home-manager.
func RecordPath(t *config.Target) string {
	if t.Kind == "home-manager" {
		return filepath.Join("home-files", ".local", "share", "lazyflakes", recordName(t))
	}
	return filepath.Join("etc", "lazyflakes", recordName(t))
}

// recordLine is the module line that puts the record of m into the
// generation.
func recordLine(t *config.Target, m Module) (string, error) {
	data, err := json.Marshal(toState(m))
	if err != nil {
		return "", err
	}
	// The line must not contain marker text, which a value may hold: JSON
	// can write # as an escape, and then there is no # at all.
	text := strings.ReplaceAll(string(data), "#", `\u0023`)
	attr := `environment.etc."lazyflakes/` + recordName(t) + `".text`
	if t.Kind == "home-manager" {
		attr = `home.file.".local/share/lazyflakes/` + recordName(t) + `".text`
	}
	return fmt.Sprintf("  %s = %s;\n", attr, generate.NixString(text)), nil
}

// ReadRecord reads the record of target t in generation gen (a profile
// link or the store path it points to). ok is false for a generation built
// before lazyflakes kept records, or without lazyflakes.
func ReadRecord(t *config.Target, gen string) (m Module, ok bool, err error) {
	data, err := os.ReadFile(filepath.Join(gen, RecordPath(t)))
	if errors.Is(err, fs.ErrNotExist) {
		return Module{}, false, nil
	}
	if err != nil {
		return Module{}, false, err
	}
	var st stateFile
	if err := json.Unmarshal(data, &st); err != nil {
		return Module{}, false, fmt.Errorf("the record of %s: %w", gen, err)
	}
	return st.module(), true, nil
}
