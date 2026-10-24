package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/MaJDa-sh/lazyflakes/internal/generate"
)

type shellExitedMsg struct {
	labels []string
	err    error
}

// newShellCmd runs `nix develop` with expr in the user's $SHELL.
func newShellCmd(labels []string, expr string) *termCmd {
	args := []string{
		"--extra-experimental-features", "nix-command flakes",
		"develop", "--impure", "--expr", expr,
	}
	// Without -c, nix develop starts bash; prefer the user's own shell.
	if sh := os.Getenv("SHELL"); sh != "" {
		args = append(args, "-c", sh)
	}
	cmd := exec.Command("nix", args...)
	cmd.Env = append(os.Environ(), "LAZYFLAKES_SHELL=1")
	return &termCmd{
		cmd:     cmd,
		banner:  fmt.Sprintf("lazyflakes: entering nix shell with %s\n(type `exit` to return)\n\n", strings.Join(labels, ", ")),
		errHint: "(refused an unfree, insecure or broken package? allow it with `o`)\n",
	}
}

// enterShell suspends the TUI and opens a shell with the selected packages,
// or the package under the cursor when nothing is selected.
func (m *Model) enterShell() tea.Cmd {
	sel := m.selected
	if len(sel) == 0 {
		p, ok := m.current()
		if !ok {
			return nil
		}
		sel = []selection{{pkg: p, channel: m.channelFor(p)}}
	}
	labels := make([]string, len(sel))
	pkgs := make([]generate.Package, len(sel))
	for i, s := range sel {
		labels[i] = m.label(s)
		pkgs[i] = generate.Package{Attr: m.pkgs[s.pkg].Attr, Channel: s.channel}
	}
	expr := generate.ShellExpr(m.channels, pkgs, m.opts)
	return tea.Exec(newShellCmd(labels, expr), func(err error) tea.Msg {
		return shellExitedMsg{labels: labels, err: err}
	})
}
