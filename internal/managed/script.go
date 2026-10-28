package managed

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/generate"
)

// A script target does not get a module: the user's script decides what
// installing means. lazyflakes only remembers which packages the script
// installed successfully, so the package list, the Installed panel and the
// review popup keep working.

// hookOps are the calls a plan makes to the user's script.
type hookOps struct {
	removes, adds []Entry
	allow         string // enabled options, comma-separated
}

// stateFile is the JSON lazyflakes keeps for a script target.
type stateFile struct {
	Packages []stateEntry `json:"packages"`
	Options  []string     `json:"options,omitempty"`
}

type stateEntry struct {
	Channel string `json:"channel"`
	Attr    string `json:"attr"`
}

// Load returns what lazyflakes knows is installed in t: the packages in a
// module target's file, or the ones a script target's script installed. A
// script target always "exists": no state just means nothing installed yet.
func Load(t *config.Target) (m Module, exists bool, err error) {
	if !t.IsScript() {
		return Read(t.File)
	}
	data, err := os.ReadFile(t.File)
	if errors.Is(err, fs.ErrNotExist) {
		return Module{}, true, nil
	}
	if err != nil {
		return Module{}, true, err
	}
	var st stateFile
	if err := json.Unmarshal(data, &st); err != nil {
		return Module{}, true, fmt.Errorf("%s: %w", t.File, err)
	}
	for _, e := range st.Packages {
		m.Packages = append(m.Packages, Entry{Channel: e.Channel, Attr: e.Attr})
	}
	if opts, err := generate.ParseOptions(strings.Join(st.Options, ",")); err == nil {
		m.Options = opts
	}
	return m, true, nil
}

// SaveState records m as the installed packages of a script target.
func SaveState(t *config.Target, m Module) error {
	st := stateFile{Options: m.Options.Keys()}
	for _, e := range m.Packages {
		st.Packages = append(st.Packages, stateEntry{Channel: e.Channel, Attr: e.Attr})
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(t.File), 0o755); err != nil {
		return err
	}
	tmp := t.File + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, t.File)
}

// Prepare plans moving t from the packages it has now (old) to want. For a
// module target that is rendering the module; for a script target it is the
// calls to the script. rebuild only applies to module targets.
func Prepare(t *config.Target, old, want Module, rebuild bool) (Plan, error) {
	if !t.IsScript() {
		content, err := Render(t, want)
		if err != nil {
			return Plan{}, err
		}
		return NewPlan(t, content, rebuild)
	}

	ops := &hookOps{allow: strings.Join(want.Options.Keys(), ",")}
	for _, o := range old.Packages {
		i := slices.IndexFunc(want.Packages, func(e Entry) bool { return e.Attr == o.Attr })
		if i < 0 || want.Packages[i].Channel != o.Channel {
			ops.removes = append(ops.removes, o) // gone, or moving to another channel
		}
	}
	for _, w := range want.Packages {
		i := slices.IndexFunc(old.Packages, func(e Entry) bool { return e.Attr == w.Attr })
		if i < 0 || old.Packages[i].Channel != w.Channel {
			ops.adds = append(ops.adds, w)
		}
	}
	return Plan{Target: t, hook: ops, save: &want}, nil
}

// Done finishes a plan after its script ran with result err: when it
// succeeded, a script target's state is recorded. It returns err, or the
// error from recording.
func (p Plan) Done(err error) error {
	if err == nil && p.save != nil {
		return SaveState(p.Target, *p.save)
	}
	return err
}

// HasWork reports whether running the plan would change anything.
func (p Plan) HasWork() bool {
	return p.hook == nil || len(p.hook.removes)+len(p.hook.adds) > 0
}

// hookScript is the shell script that calls the user's script: removals
// first, then additions, each as one call with every package.
func (p Plan) hookScript() string {
	t := p.Target
	var b strings.Builder
	b.WriteString("set -u\n")
	b.WriteString("hook=" + shq(t.Script) + "\n")
	b.WriteString(`step() { printf '\n\033[1;32m==>\033[0m %s\n' "$*"; }
fail() { printf '\n\033[1;31m==>\033[0m %s\n' "$*"; exit 1; }
run() {
  action=$1; shift
  step "$hook $action $*"
  LAZYFLAKES_ACTION=$action LAZYFLAKES_TARGET=` + shq(t.Name) + ` LAZYFLAKES_ALLOW=` + shq(p.hook.allow) + `\
    sh -c "$hook \"\$@\"" lazyflakes-script "$action" "$@" ||
    fail "the script failed (action: $action), so nothing was recorded"
}
`)
	for _, call := range []struct {
		action  string
		entries []Entry
	}{{"remove", p.hook.removes}, {"add", p.hook.adds}} {
		if len(call.entries) == 0 {
			continue
		}
		args := make([]string, len(call.entries))
		for i, e := range call.entries {
			args[i] = shq(e.Channel + ":" + e.Attr)
		}
		b.WriteString("run " + call.action + " " + strings.Join(args, " ") + "\n")
	}
	b.WriteString("step done\n")
	return b.String()
}
