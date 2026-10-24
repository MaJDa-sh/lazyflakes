package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const searchHeight = 3

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
		msg := m.spinner.View() + " " + m.status + "…\n\n" +
			dimStyle.Render("the first evaluation of nixpkgs can take a minute")
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, msg)
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
	selH := max(5, bodyH/3)

	left := lipgloss.JoinVertical(lipgloss.Left,
		m.searchView(leftW),
		m.packagesView(leftW, bodyH-searchHeight),
	)
	right := lipgloss.JoinVertical(lipgloss.Left,
		m.detailsView(rightW, bodyH-selH),
		m.selectedView(rightW, selH),
	)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right) + "\n" + m.footerView()
}

func (m Model) listHeight() int {
	return m.height - 1 - searchHeight - 2
}

func (m Model) searchView(w int) string {
	m.input.Width = w - 4
	return panel("Search", "", []string{m.input.View()}, w, searchHeight, m.focus == focusSearch)
}

func (m Model) packagesView(w, h int) string {
	inner := w - 2
	rows := h - 2
	lines := make([]string, 0, rows)
	for i := m.offset; i < len(m.filtered) && len(lines) < rows; i++ {
		p := m.pkgs[m.filtered[i]]
		mark := "  "
		if m.isSelected(m.filtered[i]) {
			mark = markStyle.Render("● ")
		}
		version := p.Version
		gap := inner - 2 - len(p.Attr) - len(version)
		if i == m.cursor {
			text := p.Attr + strings.Repeat(" ", max(1, gap)) + version
			style := cursorDimStyle
			if m.focus != focusSelected {
				style = cursorStyle
			}
			lines = append(lines, mark+style.Render(padTo(text, inner-2)))
			continue
		}
		lines = append(lines, mark+attrStyle.Render(p.Attr)+strings.Repeat(" ", max(1, gap))+dimStyle.Render(version))
	}
	if len(m.filtered) == 0 {
		lines = append(lines, dimStyle.Render("  no packages match"))
	}

	title := fmt.Sprintf("Packages %d", len(m.pkgs))
	if m.input.Value() != "" {
		title = fmt.Sprintf("Packages %d of %d", len(m.filtered), len(m.pkgs))
	}
	footer := ""
	if len(m.filtered) > 0 {
		footer = fmt.Sprintf("%d of %d", m.cursor+1, len(m.filtered))
	}
	return panel(title, footer, lines, w, h, m.focus != focusSelected)
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
		field("version", pkg.Version),
		"",
		labelStyle.Render("description"),
	}
	desc := pkg.Description
	if desc == "" {
		desc = dimStyle.Render("no description")
	}
	lines = append(lines, wrap(desc, inner)...)
	lines = append(lines, "",
		labelStyle.Render("try it"),
		dimStyle.Render("$ ")+fmt.Sprintf("nix shell %s#%s", m.flakeRef, pkg.Attr),
	)
	for i := range lines {
		lines[i] = " " + lines[i]
	}
	return panel("Details", "", lines, w, h, false)
}

func (m Model) selectedView(w, h int) string {
	lines := make([]string, 0, len(m.selected))
	// Keep the selected cursor visible in the small panel.
	rows := h - 2
	start := max(0, m.selCursor-rows+1)
	for i := start; i < len(m.selected); i++ {
		attr := m.pkgs[m.selected[i]].Attr
		if i == m.selCursor && m.focus == focusSelected {
			lines = append(lines, " "+cursorStyle.Render(padTo(attr, w-3)))
		} else {
			lines = append(lines, " "+markStyle.Render(attr))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, dimStyle.Render(" press space on a package to add it"))
	}
	return panel(fmt.Sprintf("Selected %d", len(m.selected)), "", lines, w, h, m.focus == focusSelected)
}

func (m Model) footerView() string {
	var keys [][2]string
	switch m.focus {
	case focusSearch:
		keys = [][2]string{{"enter", "done"}, {"esc", "clear"}, {"↑/↓", "move"}}
	case focusSelected:
		keys = [][2]string{{"j/k", "move"}, {"space/d", "remove"}, {"tab", "packages"}, {"q", "quit"}}
	default:
		keys = [][2]string{{"/", "search"}, {"j/k", "move"}, {"space", "select"}, {"tab", "selected"}, {"ctrl+r", "refresh"}, {"q", "quit"}}
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = keyStyle.Render(k[0]) + " " + k[1]
	}
	left := " " + strings.Join(parts, dimStyle.Render(" │ "))
	right := dimStyle.Render(m.status + " ")
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func padTo(s string, w int) string {
	if n := lipgloss.Width(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}
