package ui

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
)

// termCmd runs a command in the terminal while Bubble Tea has released it,
// like lazygit does for subprocesses.
type termCmd struct {
	cmd     *exec.Cmd
	banner  string // printed before the command runs
	errHint string // printed when the command fails
	pause   bool   // wait for enter even when the command succeeds
	stdin   io.Reader
	stdout  io.Writer
}

func (c *termCmd) SetStdin(r io.Reader)  { c.stdin, c.cmd.Stdin = r, r }
func (c *termCmd) SetStdout(w io.Writer) { c.stdout, c.cmd.Stdout = w, w }
func (c *termCmd) SetStderr(w io.Writer) { c.cmd.Stderr = w }

func (c *termCmd) Run() error {
	fmt.Fprint(c.stdout, c.banner)
	err := c.cmd.Run()
	if err != nil {
		fmt.Fprintf(c.stdout, "\nlazyflakes: %v\n%s", err, c.errHint)
	}
	// Keep the output on screen until it has been read; the alt screen would
	// otherwise hide it immediately.
	if err != nil || c.pause {
		fmt.Fprint(c.stdout, "\npress enter to return to lazyflakes")
		bufio.NewReader(c.stdin).ReadString('\n')
	}
	return err
}
