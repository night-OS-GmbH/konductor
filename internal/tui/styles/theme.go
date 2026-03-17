package styles

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Color palette — modern dark theme.
var (
	// Base
	ColorBg       = lipgloss.Color("#0D1117")
	ColorBgPanel  = lipgloss.Color("#161B22")
	ColorBgActive = lipgloss.Color("#1C2333")
	ColorBorder   = lipgloss.Color("#30363D")
	ColorBorderActive = lipgloss.Color("#58A6FF")
	ColorBgSubtle = lipgloss.Color("#21262D")

	// Text
	ColorText       = lipgloss.Color("#E6EDF3")
	ColorTextDim    = lipgloss.Color("#7D8590")
	ColorTextAccent = lipgloss.Color("#58A6FF")

	// Status
	ColorHealthy  = lipgloss.Color("#3FB950")
	ColorWarning  = lipgloss.Color("#D29922")
	ColorCritical = lipgloss.Color("#F85149")
	ColorInfo     = lipgloss.Color("#58A6FF")

	// Accents
	ColorPrimary   = lipgloss.Color("#58A6FF")
	ColorSecondary = lipgloss.Color("#BC8CFF")
	ColorTertiary  = lipgloss.Color("#79C0FF")
)

// Reusable styles
var (
	// Layout
	PanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(1, 2)

	ActivePanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorBorderActive).
				Padding(1, 2)

	// Text
	TitleStyle = lipgloss.NewStyle().
			Foreground(ColorText).
			Bold(true).
			MarginBottom(1)

	SubtitleStyle = lipgloss.NewStyle().
			Foreground(ColorTextDim).
			Italic(true)

	LabelStyle = lipgloss.NewStyle().
			Foreground(ColorTextDim).
			Width(16)

	ValueStyle = lipgloss.NewStyle().
			Foreground(ColorText).
			Bold(true)

	// Status indicators
	HealthyStyle = lipgloss.NewStyle().
			Foreground(ColorHealthy).
			Bold(true)

	WarningStyle = lipgloss.NewStyle().
			Foreground(ColorWarning).
			Bold(true)

	CriticalStyle = lipgloss.NewStyle().
			Foreground(ColorCritical).
			Bold(true)

	InfoStyle = lipgloss.NewStyle().
			Foreground(ColorInfo)

	// Header
	HeaderStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true).
			Padding(0, 1)

	FooterStyle = lipgloss.NewStyle().
			Foreground(ColorTextDim).
			Padding(0, 1)

	// Tab bar — canonical connected tab style.
	activeTabBorder = lipgloss.Border{
		Top: "─", Bottom: " ", Left: "│", Right: "│",
		TopLeft: "╭", TopRight: "╮",
		BottomLeft: "┘", BottomRight: "└",
	}
	inactiveTabBorder = lipgloss.Border{
		Top: "─", Bottom: "─", Left: "│", Right: "│",
		TopLeft: "╭", TopRight: "╮",
		BottomLeft: "┴", BottomRight: "┴",
	}
	tabGapBorder = lipgloss.Border{
		Top: " ", Bottom: "─", Left: " ", Right: " ",
		TopLeft: " ", TopRight: " ",
		BottomLeft: "─", BottomRight: "─",
	}

	ActiveTabStyle = lipgloss.NewStyle().
			Border(activeTabBorder).
			BorderForeground(ColorPrimary).
			Foreground(ColorPrimary).
			Bold(true).
			Padding(0, 2)

	InactiveTabStyle = lipgloss.NewStyle().
				Border(inactiveTabBorder).
				BorderForeground(ColorBorder).
				Foreground(ColorTextDim).
				Padding(0, 2)

	TabGapStyle = lipgloss.NewStyle().
			Border(tabGapBorder).
			BorderForeground(ColorBorder)

	// Keys
	KeyStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true)

	KeyDescStyle = lipgloss.NewStyle().
			Foreground(ColorTextDim)
)

// Badge renders a colored background badge (e.g. "HEALTHY", "FAILED").
func Badge(label string, bg lipgloss.Color) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(bg).
		Bold(true).
		Padding(0, 1).
		Render(label)
}

// StatusIndicator returns a styled status dot with label.
func StatusIndicator(healthy bool, label string) string {
	if healthy {
		return HealthyStyle.Render("●") + " " + lipgloss.NewStyle().Foreground(ColorText).Render(label)
	}
	return CriticalStyle.Render("●") + " " + lipgloss.NewStyle().Foreground(ColorText).Render(label)
}

// ProgressBar renders a horizontal bar with percentage.
func ProgressBar(percent float64, width int) string {
	filled := int(percent / 100 * float64(width))
	if filled > width {
		filled = width
	}

	var color lipgloss.Color
	switch {
	case percent >= 90:
		color = ColorCritical
	case percent >= 70:
		color = ColorWarning
	default:
		color = ColorHealthy
	}

	bar := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled))
	empty := lipgloss.NewStyle().Foreground(ColorBorder).Render(strings.Repeat("░", width-filled))
	pct := lipgloss.NewStyle().Foreground(color).Bold(true).Render(fmt.Sprintf(" %3.0f%%", percent))

	return bar + empty + pct
}

// MiniBar renders a compact bar without percentage text.
func MiniBar(percent float64, width int, color lipgloss.Color) string {
	filled := int(percent / 100 * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}

	bar := lipgloss.NewStyle().Foreground(color).Render(strings.Repeat("█", filled))
	empty := lipgloss.NewStyle().Foreground(ColorBorder).Render(strings.Repeat("░", width-filled))
	return bar + empty
}
