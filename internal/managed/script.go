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
	unsets        []string  // option paths no longer set
	sets          []Setting // options newly set or changed
	allow         string    // enabled options, comma-separated
	allowChanged  bool      // allow differs from what was recorded
}

// stateFile is the JSON lazyflakes keeps for a script target.
type stateFile struct {
	Packages []stateEntry   `json:"packages"`
	Options  []string       `json:"options,omitempty"`
	Settings []stateSetting `json:"settings,omitempty"`
}

type stateSetting struct {
	Path  string `json:"path"`
	Value string `json:"value"`
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
	return st.module(), true, nil
}

// toState is m in the JSON form of a state file or generation record.
func toState(m Module) stateFile {
	st := stateFile{Options: m.Options.Keys()}
	for _, e := range m.Packages {
		st.Packages = append(st.Packages, stateEntry{Channel: e.Channel, Attr: e.Attr})
	}
	for _, s := range m.Settings {
		st.Settings = append(st.Settings, stateSetting{Path: s.Path, Value: s.Value})
	}
	return st
}

func (st stateFile) module() Module {
	var m Module
	for _, e := range st.Packages {
		m.Packages = append(m.Packages, Entry{Channel: e.Channel, Attr: e.Attr})
	}
	for _, s := range st.Settings {
		m.Settings = append(m.Settings, Setting{Path: s.Path, Value: s.Value})
	}
	if opts, err := generate.ParseOptions(strings.Join(st.Options, ",")); err == nil {
		m.Options = opts
	}
	return m
}

// SaveState records m as the installed packages of a script target.
func SaveState(t *config.Target, m Module) error {
	data, err := json.MarshalIndent(toState(m), "", "  ")
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

	ops := &hookOps{allow: strings.Join(want.Options.Keys(), ","), allowChanged: want.Options != old.Options}
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
	for _, o := range old.Settings {
		if !slices.ContainsFunc(want.Settings, func(s Setting) bool { return s.Path == o.Path }) {
			ops.unsets = append(ops.unsets, o.Path)
		}
	}
	for _, w := range want.Settings {
		i := slices.IndexFunc(old.Settings, func(s Setting) bool { return s.Path == w.Path })
		if i < 0 || old.Settings[i].Value != w.Value {
			ops.sets = append(ops.sets, w)
		}
	}
	progress, err := os.CreateTemp("", "lazyflakes-progress-*")
	if err != nil {
		return Plan{}, err
	}
	progress.Close()
	return Plan{Target: t, hook: ops, from: &old, save: &want, progress: progress.Name()}, nil
}

// Done finishes a plan after its script ran with result err. A script
// target's state is recorded: all of it when the script succeeded, and
// when a call failed, what the calls before it did, so the state still
// matches what is installed. It returns err, joined with an error from
// recording.
func (p Plan) Done(err error) error {
	switch {
	case p.save == nil:
		return err
	case err == nil:
		return SaveState(p.Target, *p.save)
	}
	if m, ok := p.partial(); ok {
		if saveErr := SaveState(p.Target, m); saveErr != nil {
			return errors.Join(err, saveErr)
		}
	}
	return err
}

// partial is the state after the calls the script finished before one
// failed, which hookScript lists in the progress file. ok is false when
// none finished.
func (p Plan) partial() (m Module, ok bool) {
	data, _ := os.ReadFile(p.progress)
	done := strings.Fields(string(data))
	if len(done) == 0 {
		return Module{}, false
	}
	m = *p.from
	m.Packages, m.Settings = slices.Clone(m.Packages), slices.Clone(m.Settings)
	m.Options = p.save.Options // every call was given them
	for _, action := range done {
		switch action {
		case "remove":
			m.Packages = slices.DeleteFunc(m.Packages, func(e Entry) bool { return slices.Contains(p.hook.removes, e) })
		case "add":
			m.Packages = append(m.Packages, p.hook.adds...)
		case "unset":
			m.Settings = slices.DeleteFunc(m.Settings, func(s Setting) bool { return slices.Contains(p.hook.unsets, s.Path) })
		case "set":
			for _, w := range p.hook.sets {
				if i := slices.IndexFunc(m.Settings, func(s Setting) bool { return s.Path == w.Path }); i >= 0 {
					m.Settings[i] = w
				} else {
					m.Settings = append(m.Settings, w)
				}
			}
		}
	}
	return m, true
}

// HasWork reports whether running the plan would change anything. For a
// script target, new allow options alone are work too: no call is made,
// but they are recorded and given to the script from then on.
func (p Plan) HasWork() bool {
	return p.hook == nil || p.hook.allowChanged || len(p.hook.removes)+len(p.hook.adds)+len(p.hook.unsets)+len(p.hook.sets) > 0
}

// hookScript is the shell script that calls the user's script: removals
// first, then additions, each as one call with every package, then the
// options: `unset path...` and `set path=value...`. Each call that succeeds
// is listed in the progress file for Done.
func (p Plan) hookScript() string {
	t := p.Target
	var b strings.Builder
	b.WriteString("set -u\n")
	b.WriteString("hook=" + shq(t.Script) + "\n")
	b.WriteString("progress=" + shq(p.progress) + "\n")
	b.WriteString(`step() { printf '\n\033[1;32m==>\033[0m %s\n' "$*"; }
fail() { printf '\n\033[1;31m==>\033[0m %s\n' "$*"; exit 1; }
run() {
  action=$1; shift
  step "$hook $action $*"
  LAZYFLAKES_ACTION=$action LAZYFLAKES_TARGET=` + shq(t.Name) + ` LAZYFLAKES_ALLOW=` + shq(p.hook.allow) + `\
    sh -c "$hook \"\$@\"" lazyflakes-script "$action" "$@" ||
    fail "the script failed (action: $action); only the calls before it were recorded"
  printf '%s\n' "$action" >>"$progress"
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
	if len(p.hook.unsets) > 0 {
		args := make([]string, len(p.hook.unsets))
		for i, path := range p.hook.unsets {
			args[i] = shq(path)
		}
		b.WriteString("run unset " + strings.Join(args, " ") + "\n")
	}
	if len(p.hook.sets) > 0 {
		args := make([]string, len(p.hook.sets))
		for i, s := range p.hook.sets {
			args[i] = shq(s.Path + "=" + s.Value)
		}
		b.WriteString("run set " + strings.Join(args, " ") + "\n")
	}
	b.WriteString("step done\n")
	return b.String()
}
