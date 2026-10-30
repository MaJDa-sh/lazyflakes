package generations

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
	"github.com/MaJDa-sh/lazyflakes/internal/managed"
)

// ModuleChange is what a rollback does to lazyflakes.nix.
type ModuleChange int

const (
	// Restore writes the module as the generation's record has it, so the
	// next rebuild keeps the rollback.
	Restore ModuleChange = iota
	// Unchanged: the module already is what the generation had.
	Unchanged
	// NoRecord: the generation predates the records, so the module is left
	// as it is and the next rebuild installs its packages again.
	NoRecord
)

// Plan rolls a target back to a generation.
type Plan struct {
	Target   *config.Target
	From, To Generation
	Module   ModuleChange
	profile  string
	write    managed.Plan // writes lazyflakes.nix when Module is Restore
}

// Prepare plans rolling target t back to generation to.
func Prepare(t *config.Target, gens []Generation, to Generation) (Plan, error) {
	if t.IsScript() {
		return Plan{}, fmt.Errorf("target %q runs a script, which has no generations", t.Name)
	}
	from, ok := CurrentOf(gens)
	if !ok {
		return Plan{}, errors.New("cannot tell which generation is the current one")
	}
	if to.Current {
		return Plan{}, fmt.Errorf("generation %d is the current one already", to.Number)
	}
	profile, err := Profile(t)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Target: t, From: from, To: to, profile: profile, Module: NoRecord}
	if to.Record == nil {
		return p, nil
	}
	content, err := managed.Render(t, *to.Record)
	if err != nil {
		return Plan{}, fmt.Errorf("the record of generation %d: %w", to.Number, err)
	}
	if current, err := os.ReadFile(t.File); err == nil && string(current) == content {
		p.Module = Unchanged
		return p, nil
	}
	if p.write, err = managed.NewPlan(t, content, false); err != nil {
		return Plan{}, err
	}
	p.Module = Restore
	return p, nil
}

// ModuleSummary says what the rollback does to lazyflakes.nix.
func (p Plan) ModuleSummary() string {
	file, n := p.Target.File, p.To.Number
	switch p.Module {
	case Unchanged:
		return fmt.Sprintf("%s already is what generation %d had.", file, n)
	case NoRecord:
		return fmt.Sprintf("Generation %d has no record of what lazyflakes installed, so %s stays as it is and the next rebuild installs its packages again.", n, file)
	}
	if changes := ModuleChanges(p.Target, p.From, p.To); len(changes) > 0 {
		return fmt.Sprintf("%s is restored as generation %d had it: %s", file, n, strings.Join(changes, ", "))
	}
	return fmt.Sprintf("%s is restored as generation %d had it.", file, n)
}

// ModuleChanges is what going from generation from to generation to does
// to lazyflakes' packages and options. When from has no record, the
// module as it is now stands in for it.
func ModuleChanges(t *config.Target, from, to Generation) []string {
	if to.Record == nil {
		return nil
	}
	var base managed.Module
	if from.Record != nil {
		base = *from.Record
	} else if cur, _, err := managed.Load(t); err == nil {
		base = cur
	}
	return Changes(base, *to.Record)
}

// Cleanup removes the temporary files.
func (p Plan) Cleanup() { p.write.Cleanup() }

// Script is the shell script that rolls back. It switches the profile
// first (when that fails nothing has changed), then restores lazyflakes.nix
// (when that fails the profile is switched back), then activates the
// generation. Whatever the activation reports, the profile, and with it
// the boot menu, and lazyflakes.nix agree on the generation afterwards.
func (p Plan) Script() string {
	t, n := p.Target, p.To.Number
	sudo := ""
	if unix.Access(filepath.Dir(p.profile), unix.W_OK) != nil {
		sudo = t.Sudo
		if sudo == "" {
			sudo = config.DefaultSudo
		}
		sudo += " "
	}
	switchTo := func(n int) string {
		return fmt.Sprintf("%snix-env --profile %s --switch-generation %d", sudo, shq(p.profile), n)
	}

	var b strings.Builder
	b.WriteString(`set -u
step() { printf '\n\033[1;32m==>\033[0m %s\n' "$*"; }
warn() { printf '\n\033[1;33m==>\033[0m %s\n' "$*"; }
fail() { printf '\n\033[1;31m==>\033[0m %s\n' "$*"; exit 1; }
`)
	fmt.Fprintf(&b, "step %s\n%s || fail %s\n", shq(fmt.Sprintf("switching %s to generation %d", p.profile, n)),
		switchTo(n), shq("could not switch the profile, so nothing was changed"))

	switch p.Module {
	case Restore:
		// The write plan's own script, without its closing "done".
		write := strings.TrimSuffix(p.write.Script(), "step \"done\"\n")
		fmt.Fprintf(&b, "if ! (\n%s); then\n  %s\n  fail %s\nfi\n", write, switchTo(p.From.Number),
			shq(fmt.Sprintf("could not restore %s, so the profile was switched back to generation %d and nothing was changed", t.File, p.From.Number)))
	case NoRecord:
		fmt.Fprintf(&b, "warn %s\n", shq(fmt.Sprintf("generation %d has no record of what lazyflakes installed, so %s stays as it is: the next rebuild installs its packages again", n, t.File)))
	}

	fmt.Fprintf(&b, "step %s\n%s || fail %s\n", shq(fmt.Sprintf("activating generation %d", n)), p.activate(sudo),
		shq(fmt.Sprintf("activating generation %d reported errors; it is the default generation now and %s matches it", n, filepath.Base(t.File))))
	fmt.Fprintf(&b, "step %s\n", shq(fmt.Sprintf("done: generation %d is the current one", n)))
	return b.String()
}

// activate is the command that makes generation p.To the running one.
func (p Plan) activate(sudo string) string {
	gen := p.To.Link
	switch p.Target.Kind {
	case "nix-darwin":
		// Older nix-darwin activates the user's part separately.
		return fmt.Sprintf("{ [ ! -x %s ] || %s; } && %s%s", shq(gen+"/activate-user"), shq(gen+"/activate-user"), sudo, shq(gen+"/activate"))
	case "home-manager":
		return shq(gen + "/activate")
	}
	return sudo + shq(gen+"/bin/switch-to-configuration") + " switch"
}

// shq quotes s for POSIX sh.
func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
