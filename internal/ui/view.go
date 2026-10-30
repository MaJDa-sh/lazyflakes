package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/MaJDa-sh/lazyflakes/internal/generate"
	"github.com/MaJDa-sh/lazyflakes/internal/nix"
)

const (
	searchHeight = 3
	versionWidth = 12 // width of each channel's version column
)

var channelColors = []lipgloss.Color{"2", "3", "5", "6"}

// channelStyle colors everything that belongs to channel c the same way.
func channelStyle(c int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(channelColors[c%len(channelColors)])
}

// Layout, lazygit style:
//
//	╭ Search ─────────╮╭ Details ──────────╮
//	╰─────────────────╯│                   │
//	╭ Packages ───────╮│                   │
//	│                 │╰───────────────────╯
//	│                 │╭ Selected ─────────╮
//	│                 ││                   │
//	╰─────────────────╯╰───────────────────╯
//	 keybindings                     status
func (m Model) View() string {
	if m.width == 0 {
		return ""
	}
	if m.loading {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, m.loadingView())
	}
	if m.err != nil {
		msg := errStyle.Render("error: ") + m.err.Error() + "\n\n" +
			dimStyle.Render("ctrl+r retry · q quit")
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			lipgloss.NewStyle().Width(min(m.width, 100)).Render(msg))
	}

	leftW := max(30, m.width*11/20)
	rightW := m.width - leftW
	bodyH := m.height - 1
	selH := max(5, bodyH/4)
	instH := max(5, bodyH/4)

	var left, right string
	switch m.tab {
	case tabGenerations:
		left = lipgloss.JoinVertical(lipgloss.Left,
			m.genHeaderView(leftW),
			m.genListView(leftW, bodyH-searchHeight),
		)
		right = m.genChangesView(rightW, bodyH)
	case tabOptions:
		left = lipgloss.JoinVertical(lipgloss.Left,
			m.searchView(leftW),
			m.optionsListView(leftW, bodyH-searchHeight),
		)
		right = lipgloss.JoinVertical(lipgloss.Left,
			m.optionDetailsView(rightW, bodyH-selH-instH),
			m.pendingView(rightW, selH),
			m.setView(rightW, instH),
		)
	default:
		left = lipgloss.JoinVertical(lipgloss.Left,
			m.searchView(leftW),
			m.packagesView(leftW, bodyH-searchHeight),
		)
		right = lipgloss.JoinVertical(lipgloss.Left,
			m.detailsView(rightW, bodyH-selH-instH),
			m.selectedView(rightW, selH),
			m.installedView(rightW, instH),
		)
	}
	screen := lipgloss.JoinHorizontal(lipgloss.Top, left, right) + "\n" + m.footerView()

	switch m.popup {
	case popupGenerate:
		screen = overlayCenter(screen, m.generateView(), m.width, m.height)
	case popupConfirm:
		screen = overlayCenter(screen, m.confirmView(), m.width, m.height)
	case popupSystem:
		screen = overlayCenter(screen, m.systemView(), m.width, m.height)
	case popupValue:
		screen = overlayCenter(screen, m.editorView(), m.width, m.height)
	case popupRollback:
		screen = overlayCenter(screen, m.rollbackView(), m.width, m.height)
	case popupOptions:
		if m.optReturn == popupGenerate {
			screen = overlayCenter(screen, m.generateView(), m.width, m.height)
		}
		screen = overlayCenter(screen, m.optionsView(), m.width, m.height)
	}
	return screen
}

func (m Model) loadingView() string {
	lines := []string{m.spinner.View() + " " + m.status + " package sets…", ""}
	for i, c := range m.channels {
		l := m.loads[i]
		switch {
		case l.err != nil:
			lines = append(lines, errStyle.Render("✗ ")+c.Name)
		case l.done:
			lines = append(lines, markStyle.Render("✓ ")+l.info)
		default:
			lines = append(lines, m.spinner.View()+" "+channelStyle(i).Render(c.Name)+" "+dimStyle.Render(c.Ref))
		}
	}
	lines = append(lines, "", dimStyle.Render("the first evaluation of a channel can take a minute"))
	// Left-align the lines as one block; Place would center each line.
	return lipgloss.NewStyle().Align(lipgloss.Left).Render(strings.Join(lines, "\n"))
}

// generateView is the format menu with a live preview of the file.
func (m Model) generateView() string {
	w := min(m.width-4, 80)
	h := min(m.height-2, 30)
	inner := w - 2

	descs := map[generate.Format]string{
		generate.ShellNix: "classic nix-shell, uses your nixpkgs channel",
		generate.Flake:    "nix develop, pinned by flake.lock",
	}
	var lines []string
	for i, f := range generate.Formats {
		text := fmt.Sprintf(" %-10s %s", f.Filename(), dimStyle.Render(descs[f]))
		if i == m.genCursor {
			text = cursorStyle.Render(padTo(fmt.Sprintf(" %-10s %s", f.Filename(), descs[f]), inner))
		}
		lines = append(lines, text)
	}

	f := generate.Formats[m.genCursor]
	box := "[ ]"
	if m.envrc {
		box = markStyle.Render("[x]")
	}
	lines = append(lines, "", fmt.Sprintf(" %s also write .envrc %s", box,
		dimStyle.Render("("+f.EnvrcLine()+", for direnv) · e toggle")))
	lines = append(lines, " "+labelStyle.Render("options")+" "+m.optionsSummary()+dimStyle.Render(" · o edit"))
	if len(m.projectTools) > 0 {
		labels := make([]string, len(m.projectTools))
		for i, t := range m.projectTools {
			labels[i] = t.Label
		}
		lines = append(lines, " "+labelStyle.Render("detected")+" "+markStyle.Render(strings.Join(labels, ", "))+
			dimStyle.Render(" · select these with space to include them"))
	}

	lines = append(lines, "", labelStyle.Render(" preview"))
	if _, err := os.Stat(filepath.Join(m.dir, f.Filename())); err == nil {
		lines[len(lines)-1] += errStyle.Render(" (" + f.Filename() + " exists, will ask before overwriting)")
	}
	for _, l := range strings.Split(generate.Render(f, m.channels, m.selectedPackages(), m.opts), "\n") {
		lines = append(lines, " "+l)
	}
	return panel("Generate in "+displayPath(m.dir), "enter write · s/f shortcut · e .envrc · o options · esc cancel", lines, w, h, true)
}

func (m Model) optionsView() string {
	w := min(m.width-4, 72)
	inner := w - 2
	var lines []string
	for i, o := range generate.AllOptions {
		box := "[ ]"
		if m.opts.Has(o) {
			box = "[x]"
		}
		text := fmt.Sprintf(" %d %s %-18s ", i+1, box, o.Name())
		if i == m.optCursor {
			lines = append(lines, cursorStyle.Render(padTo(text+o.Description(), inner)))
			continue
		}
		if m.opts.Has(o) {
			text = fmt.Sprintf(" %d %s %-18s ", i+1, markStyle.Render(box), o.Name())
		}
		lines = append(lines, text+dimStyle.Render(o.Description()))
	}
	lines = append(lines, "", dimStyle.Render(" applies to written files (w) and the shell (enter)"))
	return panel("Shell options", "space toggle · 1-4 · esc done", lines, w, len(lines)+2, true)
}

// optionsSummary lists the enabled options, e.g. "unfree, insecure".
func (m Model) optionsSummary() string {
	if keys := m.opts.Keys(); len(keys) > 0 {
		return markStyle.Render(strings.Join(keys, ", "))
	}
	return dimStyle.Render("none")
}

func (m Model) confirmView() string {
	f := generate.Formats[m.genCursor]
	msg := " ./" + f.Filename() + " already exists. Overwrite it?"
	w := min(m.width-4, ansi.StringWidth(msg)+4)
	return panel("Overwrite?", "y yes · n no", []string{"", msg, ""}, w, 5, true)
}

// overlayCenter draws fg centered on top of bg, both being full screens of text.
func overlayCenter(bg, fg string, w, h int) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")
	fgW := lipgloss.Width(fg)
	x := max(0, (w-fgW)/2)
	y := max(0, (h-len(fgLines))/2)
	for i, l := range fgLines {
		row := y + i
		if row >= len(bgLines) {
			break
		}
		b := bgLines[row]
		left := ansi.Truncate(b, x, "")
		left += strings.Repeat(" ", x-ansi.StringWidth(left))
		bgLines[row] = left + l + ansi.TruncateLeft(b, x+fgW, "")
	}
	return strings.Join(bgLines, "\n")
}

// displayPath shortens the home directory to ~.
func displayPath(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, path); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return path
}

// listHeight is the number of package rows: the panel minus its borders and
// the column header.
func (m Model) listHeight() int {
	return m.height - 1 - searchHeight - 2 - 1
}

func (m Model) searchView(w int) string {
	m.input.Width = w - 4
	return panel(m.searchTitle(), "", []string{m.input.View()}, w, searchHeight, m.focus == focusSearch)
}

func (m Model) packagesView(w, h int) string {
	inner := w - 2
	attrW := max(10, inner-2-len(m.channels)*(versionWidth+1))

	header := "  " + padTo("attr", attrW)
	for c, ch := range m.channels {
		name := padLeft(ch.Name, versionWidth)
		if c == m.channel {
			header += " " + channelStyle(c).Bold(true).Underline(true).Render(name)
		} else {
			header += " " + dimStyle.Render(name)
		}
	}
	lines := []string{dimStyle.Render(header[:2+attrW]) + header[2+attrW:]}

	for i := m.offset; i < len(m.filtered) && len(lines) < h-2; i++ {
		p := m.filtered[i]
		attr := padTo(ansi.Truncate(m.pkgs[p].Attr, attrW, "…"), attrW)
		mark := m.listMark(p)

		if i == m.cursor {
			text := attr
			for c := range m.channels {
				text += " " + padLeft(versionCell(m.versions[p][c]), versionWidth)
			}
			style := cursorDimStyle
			if m.focus == focusPackages || m.focus == focusSearch {
				style = cursorStyle
			}
			lines = append(lines, mark+style.Render(padTo(text, inner-2)))
			continue
		}

		row := mark + attrStyle.Render(attr)
		for c := range m.channels {
			v := m.versions[p][c]
			cell := padLeft(versionCell(v), versionWidth)
			if v == "" || c != m.channel {
				cell = dimStyle.Render(cell)
			}
			row += " " + cell
		}
		lines = append(lines, row)
	}
	if len(m.filtered) == 0 {
		lines = append(lines, dimStyle.Render("  no packages match"))
	}

	title := fmt.Sprintf("[1] Packages %d", len(m.pkgs))
	if m.input.Value() != "" {
		title = fmt.Sprintf("[1] Packages %d of %d", len(m.filtered), len(m.pkgs))
	}
	footer := ""
	if len(m.filtered) > 0 {
		footer = fmt.Sprintf("%d of %d", m.cursor+1, len(m.filtered))
	}
	return panel(title, footer, lines, w, h, m.focus == focusPackages || m.focus == focusSearch)
}

// versionCell is a version truncated to its column, or "—" when missing.
func versionCell(v string) string {
	if v == "" {
		return "—"
	}
	return ansi.Truncate(v, versionWidth, "…")
}

func (m Model) detailsView(w, h int) string {
	p, ok := m.current()
	if !ok {
		return panel("Details", "", nil, w, h, false)
	}
	pkg := m.pkgs[p]
	inner := w - 4

	field := func(label, value string) string {
		if value == "" {
			value = dimStyle.Render("—")
		}
		return labelStyle.Render(fmt.Sprintf("%-9s", label)) + value
	}
	lines := []string{
		field("attr", attrStyle.Render(pkg.Attr)),
		field("pname", pkg.Pname),
	}
	for c, ch := range m.channels {
		v := m.versions[p][c]
		if v == "" {
			v = dimStyle.Render("not available")
		}
		lines = append(lines, channelStyle(c).Bold(true).Render(fmt.Sprintf("%-9s", ch.Name))+v)
	}
	for _, in := range m.installed[pkg.Attr] {
		lines = append(lines, labelStyle.Render("installed")+" "+in.target+" · "+in.channel+
			dimStyle.Render("  (press 3 to remove or change it)"))
	}
	if hint, ok := nix.ModuleHint[pkg.Attr]; ok {
		lines = append(lines, labelStyle.Render("hint     ")+markStyle.Render(hint)+
			dimStyle.Render("  (in the Options tab)"))
	}
	lines = append(lines, "", labelStyle.Render("description"))
	desc := pkg.Description
	if desc == "" {
		desc = dimStyle.Render("no description")
	}
	lines = append(lines, wrap(desc, inner)...)
	lines = append(lines, "",
		labelStyle.Render("try it"),
		dimStyle.Render("$ ")+m.tryCommand(p),
	)
	for i := range lines {
		lines[i] = " " + lines[i]
	}
	return panel("Details", "", lines, w, h, false)
}

// tryCommand is a one-off `nix shell` for package p, with the options
// passed as environment variables (which require --impure).
func (m Model) tryCommand(p int) string {
	ref := m.channels[m.channelFor(p)].Ref + "#" + m.pkgs[p].Attr
	if env := m.opts.Env(); len(env) > 0 {
		return strings.Join(env, " ") + " nix shell --impure " + ref
	}
	return "nix shell " + ref
}

func (m Model) selectedView(w, h int) string {
	inner := w - 2
	lines := make([]string, 0, len(m.selected))
	// Keep the selected cursor visible in the small panel.
	rows := h - 2
	start := max(0, m.selCursor-rows+1)
	for i := start; i < len(m.selected); i++ {
		s := m.selected[i]
		tag := m.channels[s.channel].Name + " " + m.versions[s.pkg][s.channel]
		attr := ansi.Truncate(m.pkgs[s.pkg].Attr, max(1, inner-3-ansi.StringWidth(tag)), "…")
		gap := strings.Repeat(" ", max(1, inner-2-ansi.StringWidth(attr)-ansi.StringWidth(tag)))
		if i == m.selCursor && m.focus == focusSelected {
			lines = append(lines, " "+cursorStyle.Render(attr+gap+tag)+" ")
		} else {
			lines = append(lines, " "+markStyle.Render(attr)+gap+channelStyle(s.channel).Render(tag))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render(" press space on a package to add it"))
	}
	footer := ""
	if keys := m.opts.Keys(); len(keys) > 0 {
		footer = "allow " + strings.Join(keys, ", ")
	}
	return panel(fmt.Sprintf("[2] Selected %d", len(m.selected)), footer, lines, w, h, m.focus == focusSelected)
}

func (m Model) footerView() string {
	var keys [][2]string
	switch m.tab {
	case tabOptions:
		keys = m.optionsFooterKeys()
	case tabGenerations:
		keys = m.generationsFooterKeys()
	default:
		keys = m.packagesFooterKeys()
	}
	switch m.popup {
	case popupValue:
		keys = [][2]string{{"enter", "confirm"}, {"esc", "cancel"}, {"ctrl+r", "typed/raw"}, {"j/k", "choose"}}
	case popupGenerate:
		keys = [][2]string{{"j/k", "format"}, {"enter", "write"}, {"s", "shell.nix"}, {"f", "flake.nix"}, {"e", ".envrc"}, {"o", "options"}, {"esc", "cancel"}}
	case popupConfirm:
		keys = [][2]string{{"y", "overwrite"}, {"n", "back"}}
	case popupSystem:
		keys = [][2]string{{"enter", "apply"}, {"s", "save only"}, {"d", "remove"}, {"c", "channel"}, {"tab", "target"}, {"esc", "close"}}
	case popupOptions:
		keys = [][2]string{{"j/k", "move"}, {"space", "toggle"}, {"1-4", "toggle"}, {"esc", "done"}}
	case popupRollback:
		keys = [][2]string{{"enter", "roll back"}, {"esc", "cancel"}}
	}
	return m.renderFooter(keys)
}

// packagesFooterKeys are the key hints of the Packages tab.
func (m Model) packagesFooterKeys() [][2]string {
	switch m.focus {
	case focusSearch:
		return [][2]string{{"enter", "done"}, {"esc", "clear"}, {"↑/↓", "move"}}
	case focusInstalled:
		return [][2]string{{"j/k", "move"}, {"d", "remove"}, {"c", "channel"}, {"u", "undo"}, {"enter", "apply"}, {"t", "tab"}, {"q", "quit"}}
	case focusSelected:
		return [][2]string{{"j/k", "move"}, {"d", "unselect"}, {"c", "channel"}, {"enter", "shell"}, {"w", "write"}, {"a", "install"}, {"t", "tab"}, {"q", "quit"}}
	}
	return [][2]string{{"/", "search"}, {"space", "select"}, {"enter", "shell"}, {"w", "write"}, {"a", "install"}, {"o", "allow"}, {"t", "tab"}, {"q", "quit"}}
}

// renderFooter draws the key hints on the left and the status on the right.
func (m Model) renderFooter(keys [][2]string) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = keyStyle.Render(k[0]) + " " + k[1]
	}
	left := " " + strings.Join(parts, dimStyle.Render(" │ "))
	// The status matters more than the key hints (they are also in the panels'
	// muscle memory), so hints get cut first when space runs out.
	status := m.status
	if strings.HasPrefix(status, "error") {
		status = errStyle.Render(status)
	} else if strings.HasPrefix(status, "wrote") {
		status = markStyle.Render(status)
	} else {
		status = dimStyle.Render(status)
	}
	status = ansi.Truncate(status, m.width-2, "…") + " "
	left = ansi.Truncate(left, max(0, m.width-lipgloss.Width(status)-1), "…")
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(status)
	return left + strings.Repeat(" ", max(0, gap)) + status
}

func padLeft(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

func padTo(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}
