package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/MaJDa-sh/lazyflakes/internal/generations"
	"github.com/MaJDa-sh/lazyflakes/internal/managed"
)

// runGenerations implements `lazyflakes generations`.
func runGenerations(args []string) error {
	f := newTargetFlags("generations", "generations [flags]")
	f.fs.Parse(args)
	t, err := f.load()
	if err != nil {
		return err
	}
	gens, err := generations.List(t)
	if err != nil {
		return err
	}
	if len(gens) == 0 {
		return fmt.Errorf("%s has no generations yet", t.Name)
	}
	for _, g := range gens {
		mark := " "
		if g.Current {
			mark = "*"
		}
		fmt.Printf("%s %4d  %s  %-26s %s\n", mark, g.Number, g.Date.Format("2006-01-02 15:04"), g.Version, recordSummary(g.Record))
	}
	return nil
}

// recordSummary says what lazyflakes had installed in a generation.
func recordSummary(rec *managed.Module) string {
	if rec == nil {
		return "(no lazyflakes record)"
	}
	return plural(len(rec.Packages), "package") + ", " + plural(len(rec.Settings), "option")
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// runRollback implements `lazyflakes rollback [N]`.
func runRollback(args []string) error {
	f := newTargetFlags("rollback", "rollback [flags] [GENERATION]  (default: the one before the current)")
	yes := f.fs.Bool("yes", false, "do not ask before rolling back")
	f.fs.Parse(args)
	if f.fs.NArg() > 1 {
		f.fs.Usage()
		return errors.New("give one generation number at most")
	}
	t, err := f.load()
	if err != nil {
		return err
	}
	gens, err := generations.List(t)
	if err != nil {
		return err
	}
	to, err := generations.Find(gens, f.fs.Arg(0))
	if err != nil {
		return err
	}
	plan, err := generations.Prepare(t, gens, to)
	if err != nil {
		return err
	}
	defer plan.Cleanup()

	fmt.Printf("%s: roll back from generation %d to %d (%s)\n\n", t.Name, plan.From.Number, to.Number, to.Date.Format("2006-01-02 15:04"))
	fmt.Println(moduleSummary(plan))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if lines, err := generations.Diff(ctx, plan.From.Link, to.Link); err != nil {
		fmt.Fprintln(os.Stderr, "could not compare the generations:", err)
	} else {
		fmt.Println(diffSummary(lines, 25))
	}
	fmt.Println()
	if !*yes && !confirm("Roll back?", false) {
		return nil
	}
	cmd := exec.Command("sh", "-c", plan.Script())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("rolling back %s: %w", t.Name, err)
	}
	return nil
}

// moduleSummary says what a rollback does to lazyflakes.nix.
func moduleSummary(p generations.Plan) string {
	file := p.Target.File
	switch p.Module {
	case generations.Unchanged:
		return file + " already is what generation " + fmt.Sprint(p.To.Number) + " had."
	case generations.NoRecord:
		return fmt.Sprintf("Generation %d has no record of what lazyflakes installed, so %s stays as it is\nand the next rebuild installs its packages again.", p.To.Number, file)
	}
	var from managed.Module
	if p.From.Record != nil {
		from = *p.From.Record
	} else if cur, _, err := managed.Load(p.Target); err == nil {
		from = cur
	}
	changes := generations.Changes(from, *p.To.Record)
	if len(changes) == 0 {
		return file + " is restored as generation " + fmt.Sprint(p.To.Number) + " had it."
	}
	return file + " is restored as generation " + fmt.Sprint(p.To.Number) + " had it: " + strings.Join(changes, ", ")
}

// diffSummary lists at most max of the package changes.
func diffSummary(lines []string, max int) string {
	if len(lines) == 0 {
		return "No package versions change: the generations differ only in their configuration."
	}
	var b strings.Builder
	b.WriteString("Package changes:\n")
	for i, l := range lines {
		if i == max {
			fmt.Fprintf(&b, "  … and %d more\n", len(lines)-max)
			break
		}
		b.WriteString("  " + l + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
