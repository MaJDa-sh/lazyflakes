package ui

import (
	"bufio"
	"fmt"
	"io"
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

// shellCmd runs `nix develop` for the given packages in the user's $SHELL while
// Bubble Tea has released the terminal, like lazygit does for subprocesses.
type shellCmd struct {
	cmd    *exec.Cmd
	labels []string
	stdin  io.Reader
	stdout io.Writer
}

func newShellCmd(labels []string, expr string) *shellCmd {
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
	return &shellCmd{cmd: cmd, labels: labels}
}

func (s *shellCmd) SetStdin(r io.Reader)  { s.stdin, s.cmd.Stdin = r, r }
func (s *shellCmd) SetStdout(w io.Writer) { s.stdout, s.cmd.Stdout = w, w }
func (s *shellCmd) SetStderr(w io.Writer) { s.cmd.Stderr = w }

func (s *shellCmd) Run() error {
	fmt.Fprintf(s.stdout, "lazyflakes: entering nix shell with %s\n(type `exit` to return)\n\n",
		strings.Join(s.labels, ", "))
	err := s.cmd.Run()
	if err != nil {
		// Keep nix's error on screen until the user has read it; the alt screen
		// would otherwise hide it immediately.
		fmt.Fprintf(s.stdout, "\nlazyflakes: %v\n"+
			"(refused an unfree, insecure or broken package? allow it with `o`)\n"+
			"press enter to return to lazyflakes", err)
		bufio.NewReader(s.stdin).ReadString('\n')
	}
	return err
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
