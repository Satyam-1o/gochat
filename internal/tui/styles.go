package tui

import "github.com/charmbracelet/lipgloss"

var (
	cSub    = lipgloss.AdaptiveColor{Light: "#A49FA5", Dark: "#777777"}
	cDim    = lipgloss.AdaptiveColor{Light: "#847A85", Dark: "#5C5C5C"}
	cBorder = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#484848"}
	cSel    = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	cGreen  = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}
	cWarn   = lipgloss.AdaptiveColor{Light: "#9E7E0C", Dark: "#ECDD5B"}
	cErr    = lipgloss.AdaptiveColor{Light: "#DB5F5F", Dark: "#FF7B7B"}

	styleSidebar     = lipgloss.NewStyle().Width(sidebarWidth-4).BorderStyle(lipgloss.RoundedBorder()).BorderForeground(cBorder).Padding(0, 1)
	styleSidebarHdr  = lipgloss.NewStyle().Foreground(cGreen).Bold(true)
	styleSidebarItem = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	styleSidebarSel  = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(cSel).Padding(0, 1)
	styleSidebarHint = lipgloss.NewStyle().Foreground(cDim).Faint(true)

	styleHeader     = lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Bold(true)
	styleHeaderDim  = lipgloss.NewStyle().Foreground(cDim)
	styleViewport   = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(cBorder)
	styleInput      = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(cBorder)
	styleInputFocus = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).BorderForeground(cSel)
	styleStatus     = lipgloss.NewStyle().Foreground(cSub)

	styleTime = lipgloss.NewStyle().Foreground(cDim).Faint(true)
	styleBody = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	styleSys  = lipgloss.NewStyle().Foreground(cWarn).Faint(true)
	styleErr  = lipgloss.NewStyle().Foreground(cErr)

	// Own sent messages are tinted so it is obvious which lines are yours.
	styleNickOwn = lipgloss.NewStyle().Foreground(cGreen).Bold(true)
	styleBodyOwn = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	nickPalette = []lipgloss.Color{
		lipgloss.Color("#F7768E"),
		lipgloss.Color("#7AA2F7"),
		lipgloss.Color("#9ECE6A"),
		lipgloss.Color("#E0AF68"),
		lipgloss.Color("#BB9AF7"),
		lipgloss.Color("#7DCFFF"),
		lipgloss.Color("#FF9E64"),
		lipgloss.Color("#73DACA"),
	}
)

// styleNick returns a style with a color picked deterministically from the nick.
func styleNick(nick string) lipgloss.Style {
	h := 0
	for _, r := range nick {
		h = (h*31 + int(r)) % 100003
	}
	if h < 0 {
		h = -h
	}
	return lipgloss.NewStyle().Foreground(nickPalette[h%len(nickPalette)]).Bold(true)
}
