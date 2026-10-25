package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	activeBorder      = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	inactiveBorder    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	dimStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	attrStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	markStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	cursorStyle       = lipgloss.NewStyle().Background(lipgloss.Color("4")).Foreground(lipgloss.Color("15")).Bold(true)
	cursorRemoveStyle = lipgloss.NewStyle().Background(lipgloss.Color("1")).Foreground(lipgloss.Color("15")).Bold(true)
	cursorDimStyle    = lipgloss.NewStyle().Background(lipgloss.Color("8")).Foreground(lipgloss.Color("15"))
	labelStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	keyStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	errStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

// panel draws a lazygit-style rounded box of exactly w×h cells with the title
// embedded in the top border and an optional footer in the bottom border.
func panel(title, footer string, lines []string, w, h int, active bool) string {
	if w < 4 || h < 2 {
		return ""
	}
	border := inactiveBorder
	if active {
		border = activeBorder
	}
	inner := w - 2

	var b strings.Builder
	b.WriteString(border.Render(edge("╭", "╮", title, inner)))
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(lines) {
			line = ansi.Truncate(lines[i], inner, "…")
		}
		pad := inner - ansi.StringWidth(line)
		b.WriteString("\n" + border.Render("│") + line + strings.Repeat(" ", max(0, pad)) + border.Render("│"))
	}
	b.WriteString("\n" + border.Render(edge("╰", "╯", footer, inner)))
	return b.String()
}

// edge renders a horizontal border line with an optional label on the left.
func edge(left, right, label string, inner int) string {
	if label == "" {
		return left + strings.Repeat("─", inner) + right
	}
	label = ansi.Truncate(" "+label+" ", max(0, inner-1), "…")
	rest := inner - 1 - ansi.StringWidth(label)
	return left + "─" + label + strings.Repeat("─", max(0, rest)) + right
}

// wrap word-wraps s to width w and returns the lines.
func wrap(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	return strings.Split(lipgloss.NewStyle().Width(w).Render(s), "\n")
}
