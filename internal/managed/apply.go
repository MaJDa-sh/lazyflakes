package managed

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/MaJDa-sh/lazyflakes/internal/config"
)

// Plan is everything needed to apply new module content to a target.
type Plan struct {
	Target  *config.Target
	NewFile string // temporary file holding the new module
	Sudo    bool   // the module's location is not writable by the user
	GitAdd  bool   // the module lives in a git repo and is not tracked yet
	Rebuild bool   // run the target's rebuild command after writing
}

// NewPlan writes content to a temporary file and works out how to install it.
func NewPlan(t *config.Target, content string, rebuild bool) (Plan, error) {
	f, err := os.CreateTemp("", "lazyflakes-*.nix")
	if err != nil {
		return Plan{}, err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return Plan{}, err
	}
	if err := f.Close(); err != nil {
		return Plan{}, err
	}
	return Plan{
		Target:  t,
		NewFile: f.Name(),
		Sudo:    !writable(t.File),
		GitAdd:  needsGitAdd(t.File),
		Rebuild: rebuild && t.Rebuild != "",
	}, nil
}

// Cleanup removes the temporary file.
func (p Plan) Cleanup() { os.Remove(p.NewFile) }

func writable(file string) bool {
	if _, err := os.Stat(file); err == nil {
		return unix.Access(file, unix.W_OK) == nil
	}
	return unix.Access(filepath.Dir(file), unix.W_OK) == nil
}

// needsGitAdd reports whether file is inside a git work tree but untracked.
// Flakes only see tracked files, so a new module must be added first.
func needsGitAdd(file string) bool {
	dir := filepath.Dir(file)
	if git(dir, "rev-parse", "--is-inside-work-tree").Run() != nil {
		return false
	}
	return git(dir, "ls-files", "--error-unmatch", "--", filepath.Base(file)).Run() != nil
}

// git runs git in dir. Marking dir as a safe directory lets the user read a
// root-owned repository such as /etc/nixos, which git otherwise refuses as
// "dubious ownership".
func git(dir string, args ...string) *exec.Cmd {
	return exec.Command("git", append([]string{"-c", "safe.directory=" + dir, "-C", dir}, args...)...)
}

// Script is a POSIX shell script that installs the new module, rebuilds and,
// if the rebuild fails, puts the previous module back so the configuration
// is left exactly as it was.
func (p Plan) Script() string {
	sudo := ""
	if p.Sudo {
		sudo = "sudo "
	}
	dir, name := filepath.Dir(p.Target.File), filepath.Base(p.Target.File)

	var b strings.Builder
	b.WriteString("set -u\n")
	b.WriteString("file=" + shq(p.Target.File) + "\n")
	b.WriteString("new=" + shq(p.NewFile) + "\n")
	b.WriteString(`backup=$(mktemp)
had_old=0
if [ -e "$file" ]; then cp "$file" "$backup" && had_old=1; fi
step() { printf '\n\033[1;32m==>\033[0m %s\n' "$*"; }
fail() { printf '\n\033[1;31m==>\033[0m %s\n' "$*"; rm -f "$backup"; exit 1; }

step "writing $file"
` + sudo + `install -m 0644 "$new" "$file" || fail "could not write $file"
`)
	gitCmd := sudo + "git -c safe.directory=" + shq(dir) + " -C " + shq(dir)
	if p.GitAdd {
		b.WriteString(`step "git add --intent-to-add ` + name + ` (flakes only see tracked files)"
` + gitCmd + ` add --intent-to-add -- ` + shq(name) + `
`)
	}
	if p.Rebuild {
		b.WriteString(`step ` + shq(p.Target.Rebuild) + `
if ! sh -c ` + shq(p.Target.Rebuild) + `; then
  if [ "$had_old" = 1 ]; then
    ` + sudo + `install -m 0644 "$backup" "$file"
  else
    ` + sudo + `rm -f "$file"
`)
		if p.GitAdd {
			b.WriteString(`    ` + gitCmd + ` rm -q --cached --ignore-unmatch -- ` + shq(name) + "\n")
		}
		b.WriteString(`  fi
  fail "rebuild failed, restored the previous $file"
fi
`)
	}
	b.WriteString(`rm -f "$backup"
step "done"
`)
	return b.String()
}

// shq quotes s for POSIX sh.
func shq(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
