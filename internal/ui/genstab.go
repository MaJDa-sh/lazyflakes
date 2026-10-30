package ui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/MaJDa-sh/lazyflakes/internal/generations"
)

// The Generations tab lists the generations of the current target and
// rolls back to one: the profile, the running system and lazyflakes.nix
// together, so the next rebuild keeps the rollback.

// genDiff is what changes between two generations, as diff-closures says.
type genDiff struct {
	lines   []string
	err     error
	loading bool
}

type genDiffMsg struct {
	key   string
	lines []string
	err   error
}

type rolledBackMsg struct {
	target string
	number int
	err    error
}

// loadGenerations reads the current target's generations, keeping the
// cursor when they belong to the same target, and starts comparing the
// selected one.
func (m *Model) loadGenerations() tea.Cmd {
	t := m.curTarget()
	m.gens, m.gensErr = nil, nil
	if t == nil {
		return nil
	}
	if m.gensFor != t.Name {
		m.gensCursor, m.gensOffset, m.gensFor = 0, 0, t.Name
	}
	m.gens, m.gensErr = generations.List(t)
	m.gensCursor = min(m.gensCursor, max(0, len(m.gens)-1))
	return m.diffSelected()
}

func (m Model) selectedGen() (generations.Generation, bool) {
	if m.gensCursor < 0 || m.gensCursor >= len(m.gens) {
		return generations.Generation{}, false
	}
	return m.gens[m.gensCursor], true
}

func diffKey(from, to generations.Generation) string { return from.Link + "→" + to.Link }

// diffSelected compares the current generation with the selected one in
// the background, unless that is known already.
func (m *Model) diffSelected() tea.Cmd {
	sel, ok := m.selectedGen()
	cur, hasCur := generations.CurrentOf(m.gens)
	if !ok || !hasCur || sel.Current {
		return nil
	}
	key := diffKey(cur, sel)
	if m.genDiffs == nil {
		m.genDiffs = map[string]genDiff{}
	}
	if _, known := m.genDiffs[key]; known {
		return nil
	}
	m.genDiffs[key] = genDiff{loading: true}
	from, to := cur.Link, sel.Link
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		lines, err := generations.Diff(ctx, from, to)
		return genDiffMsg{key: key, lines: lines, err: err}
	}
}

func (m *Model) moveGen(delta int) tea.Cmd {
	m.gensCursor = max(0, min(m.gensCursor+delta, len(m.gens)-1))
	if h := m.listHeight() - 1; h > 0 { // a header row sits above the list
		if m.gensCursor < m.gensOffset {
			m.gensOffset = m.gensCursor
		}
		if m.gensCursor >= m.gensOffset+h {
			m.gensOffset = m.gensCursor - h + 1
		}
	}
	return m.diffSelected()
}

func (m Model) handleGenerationsKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		return m, tea.Quit
	case "t":
		return m, m.switchTab()
	case "T":
		return m, m.switchTab(true)
	case "j", "down":
		return m, m.moveGen(1)
	case "k", "up":
		return m, m.moveGen(-1)
	case "ctrl+d", "pgdown":
		return m, m.moveGen(m.listHeight() / 2)
	case "ctrl+u", "pgup":
		return m, m.moveGen(-m.listHeight() / 2)
	case "g", "home":
		return m, m.moveGen(-len(m.gens))
	case "G", "end":
		return m, m.moveGen(len(m.gens))
	case "ctrl+r":
		m.genDiffs = nil
		return m, m.loadGenerations()
	case "[", "]":
		if len(m.sys.targets) > 1 {
			step := 1
			if key == "[" {
				step = len(m.sys.targets) - 1
			}
			cmd := m.syncSystem((m.sys.target+step)%len(m.sys.targets), false, false)
			return m, tea.Batch(cmd, m.loadGenerations())
		}
	case "enter":
		m.openRollback()
	}
	return m, nil
}

// openRollback asks to confirm rolling back to the selected generation.
func (m *Model) openRollback() {
	sel, ok := m.selectedGen()
	t := m.curTarget()
	switch {
	case !ok || t == nil:
		return
	case sel.Current:
		m.status = fmt.Sprintf("generation %d is the current one", sel.Number)
		return
	}
	plan, err := generations.Prepare(t, m.gens, sel)
	if err != nil {
		m.status = "error: " + err.Error()
		return
	}
	m.rollback = &plan
	m.popup = popupRollback
}

func (m Model) handleRollbackKey(key string) (tea.Model, tea.Cmd) {
	plan := m.rollback
	switch key {
	case "esc", "q", "n":
		plan.Cleanup()
		m.rollback, m.popup = nil, popupNone
	case "enter", "y":
		m.rollback, m.popup = nil, popupNone
		cmd := &termCmd{
			cmd:    exec.Command("sh", "-c", plan.Script()),
			banner: fmt.Sprintf("lazyflakes: rolling %s back to generation %d\n", plan.Target.Name, plan.To.Number),
			pause:  true,
		}
		return m, tea.Exec(cmd, func(err error) tea.Msg {
			plan.Cleanup()
			return rolledBackMsg{target: plan.Target.Name, number: plan.To.Number, err: err}
		})
	}
	return m, nil
}

// rolledBack reloads everything a rollback may have changed.
func (m *Model) rolledBack(msg rolledBackMsg) tea.Cmd {
	m.loadInstalled()
	m.syncSystem(m.sys.target, false, false)
	if msg.err != nil {
		m.status = fmt.Sprintf("error: rolling %s back to generation %d: %v (see the output for what was done)", msg.target, msg.number, msg.err)
	} else {
		m.status = fmt.Sprintf("%s is on generation %d now, and lazyflakes.nix matches it", msg.target, msg.number)
	}
	m.genDiffs = nil
	return m.loadGenerations()
}

// ---- views ----

// genRecord is the short summary of what lazyflakes had in a generation.
func genRecord(g generations.Generation) string {
	if g.Record == nil {
		return dimStyle.Render("no record")
	}
	return fmt.Sprintf("%d pkgs, %d opts", len(g.Record.Packages), len(g.Record.Settings))
}

func (m Model) genHeaderView(w int) string {
	text := dimStyle.Render(" enter rolls back to the selected generation, lazyflakes.nix included")
	if t := m.curTarget(); t != nil && len(m.sys.targets) > 1 {
		text = dimStyle.Render(fmt.Sprintf(" generations of %s · [ ] switches the target", t.Name))
	}
	return panel(m.searchTitle(), "", []string{text}, w, searchHeight, false)
}

func (m Model) genListView(w, h int) string {
	inner := w - 2
	t := m.curTarget()
	title := "[1] Generations"
	var lines []string
	switch {
	case t == nil:
		lines = []string{"", dimStyle.Render("  no target is configured yet"), "", dimStyle.Render("  press t to go to Packages, then a and enter to run lazyflakes setup")}
	case m.gensErr != nil:
		lines = append([]string{""}, wrap("  "+errStyle.Render("error: ")+m.gensErr.Error(), inner-2)...)
	case len(m.gens) == 0:
		lines = []string{"", dimStyle.Render("  no generations yet: rebuild once")}
	default:
		title = fmt.Sprintf("[1] Generations %d · %s", len(m.gens), t.Name)
		const numW, dateW, recW = 5, 16, 16
		verW := max(0, inner-2-numW-dateW-recW-3)
		lines = append(lines, dimStyle.Render(ansi.Truncate("  "+padTo("gen", numW)+" "+padTo("date", dateW)+" "+padTo("version", verW)+" lazyflakes", inner, "")))
		for i := m.gensOffset; i < len(m.gens) && len(lines) < h-2; i++ {
			g := m.gens[i]
			mark := "  "
			if g.Current {
				mark = markStyle.Render("● ")
			}
			text := fmt.Sprintf("%*d", numW, g.Number) + " " + g.Date.Format("2006-01-02 15:04") + " " +
				padTo(ansi.Truncate(g.Version, verW, "…"), verW) + " "
			if i == m.gensCursor {
				rec := "no record"
				if g.Record != nil {
					rec = ansi.Strip(genRecord(g))
				}
				lines = append(lines, mark+cursorStyle.Render(padTo(text+rec, inner-2)))
				continue
			}
			lines = append(lines, mark+text+genRecord(g))
		}
	}
	footer := ""
	if len(m.gens) > 0 {
		footer = fmt.Sprintf("%d of %d · ● current", m.gensCursor+1, len(m.gens))
	}
	return panel(title, footer, lines, w, h, true)
}

// genChangesView says what rolling back to the selected generation would
// change: lazyflakes.nix first, then the package versions.
func (m Model) genChangesView(w, h int) string {
	sel, ok := m.selectedGen()
	t := m.curTarget()
	if !ok || t == nil {
		return panel("Changes", "", nil, w, h, false)
	}
	inner := w - 4
	var lines []string
	add := func(s ...string) {
		for _, l := range s {
			lines = append(lines, " "+l)
		}
	}
	cur, _ := generations.CurrentOf(m.gens)

	if sel.Current {
		add(fmt.Sprintf("Generation %d is the one running.", sel.Number), "")
		add(labelStyle.Render("built    ") + sel.Date.Format("2006-01-02 15:04"))
		if sel.Version != "" {
			add(labelStyle.Render("version  ") + sel.Version)
		}
		add("", labelStyle.Render("lazyflakes"))
		if sel.Record == nil {
			add(dimStyle.Render("no record: built before lazyflakes recorded generations"))
		} else {
			for _, e := range sel.Record.Packages {
				add(e.Attr + dimStyle.Render(" "+e.Channel))
			}
			for _, s := range sel.Record.Settings {
				add(ansi.Truncate(s.Path+" = "+s.Value, inner, "…"))
			}
			if len(sel.Record.Packages)+len(sel.Record.Settings) == 0 {
				add(dimStyle.Render("nothing installed or set"))
			}
		}
		return panel("Current generation", "", lines, w, h, false)
	}

	add(labelStyle.Render("lazyflakes.nix"))
	switch changes := generations.ModuleChanges(t, cur, sel); {
	case sel.Record == nil:
		for _, l := range wrap("no record: generation "+fmt.Sprint(sel.Number)+" was built before lazyflakes recorded generations, so lazyflakes.nix stays as it is and the next rebuild installs its packages again", inner) {
			add(dimStyle.Render(l))
		}
	case len(changes) == 0:
		add(dimStyle.Render("no change"))
	default:
		for _, c := range changes {
			add(changeStyle(c).Render(ansi.Truncate(c, inner, "…")))
		}
	}

	add("", labelStyle.Render("packages"))
	switch d := m.genDiffs[diffKey(cur, sel)]; {
	case d.loading:
		add(dimStyle.Render("comparing the generations…"))
	case d.err != nil:
		for _, l := range wrap(d.err.Error(), inner) {
			add(errStyle.Render(l))
		}
	case len(d.lines) == 0:
		for _, l := range wrap("no package versions change: the generations differ only in their configuration", inner) {
			add(dimStyle.Render(l))
		}
	default:
		room := h - 2 - len(lines)
		for i, l := range d.lines {
			if i == room-1 && len(d.lines) > room {
				add(dimStyle.Render(fmt.Sprintf("… and %d more", len(d.lines)-i)))
				break
			}
			add(ansi.Truncate(l, inner, "…"))
		}
	}
	return panel(fmt.Sprintf("Rolling back: %d → %d", cur.Number, sel.Number), "", lines, w, h, false)
}

// changeStyle colors a line of generations.Changes by what it does.
func changeStyle(c string) interface{ Render(...string) string } {
	switch {
	case strings.HasPrefix(c, "+"):
		return markStyle
	case strings.HasPrefix(c, "−"):
		return errStyle
	}
	return labelStyle
}

// rollbackView asks to confirm a rollback.
func (m Model) rollbackView() string {
	p := m.rollback
	if p == nil {
		return ""
	}
	w := min(m.width-4, 84)
	inner := w - 4
	lines := []string{""}
	lines = append(lines, " "+fmt.Sprintf("Roll %s back from generation %d to %d (%s)?", p.Target.Name, p.From.Number, p.To.Number, p.To.Date.Format("2006-01-02 15:04")), "")
	for _, l := range wrap(p.ModuleSummary(), inner) {
		lines = append(lines, " "+l)
	}
	lines = append(lines, "")
	steps := "The profile is switched first, then lazyflakes.nix is written, then the generation is activated; sudo may ask for your password."
	if p.Module != generations.Restore {
		steps = "The profile is switched first, then the generation is activated; sudo may ask for your password."
	}
	for _, l := range wrap(steps, inner) {
		lines = append(lines, " "+dimStyle.Render(l))
	}
	lines = append(lines, "")
	return panel("Roll back "+p.Target.Name, "enter roll back · esc cancel", lines, w, min(m.height-2, len(lines)+2), true)
}

func (m Model) generationsFooterKeys() [][2]string {
	return [][2]string{{"j/k", "move"}, {"enter", "roll back"}, {"[ ]", "target"}, {"ctrl+r", "reload"}, {"t", "packages"}, {"q", "quit"}}
}
