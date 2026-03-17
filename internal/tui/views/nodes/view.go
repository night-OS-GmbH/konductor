package nodes

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/night-OS-GmbH/konductor/internal/config"
	"github.com/night-OS-GmbH/konductor/internal/k8s"
	"github.com/night-OS-GmbH/konductor/internal/tui/styles"
)

type Model struct {
	cfg      *config.Config
	nodes    []k8s.NodeInfo
	selected int
	err      error
}

func New(cfg *config.Config) Model {
	return Model{cfg: cfg}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m *Model) SetData(nodes []k8s.NodeInfo) {
	m.nodes = nodes
	m.err = nil
}

func (m *Model) SetError(err error) {
	m.err = err
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			m.selected++
			if m.selected >= len(m.nodes) {
				m.selected = len(m.nodes) - 1
			}
			if m.selected < 0 {
				m.selected = 0
			}
		case "k", "up":
			m.selected--
			if m.selected < 0 {
				m.selected = 0
			}
		}
	}
	return m, nil
}

func (m Model) View(width, height int) string {
	if m.err != nil {
		content := lipgloss.JoinVertical(lipgloss.Left,
			styles.TitleStyle.Render("Nodes"),
			"",
			styles.CriticalStyle.Render("Error: "+m.err.Error()),
		)
		return styles.PanelStyle.Width(width - 2).Height(height - 2).Render(content)
	}

	if m.nodes == nil {
		content := lipgloss.JoinVertical(lipgloss.Left,
			styles.TitleStyle.Render("Nodes"),
			"",
			styles.InfoStyle.Render("Loading..."),
		)
		return styles.PanelStyle.Width(width - 2).Height(height - 2).Render(content)
	}

	content := lipgloss.JoinVertical(lipgloss.Left,
		styles.TitleStyle.Render("Nodes"),
		styles.SubtitleStyle.Render(fmt.Sprintf("  %d nodes in cluster", len(m.nodes))),
		"",
		m.renderTable(width-6),
	)

	return styles.PanelStyle.
		Width(width - 2).
		Height(height - 2).
		Render(content)
}

func (m Model) renderTable(width int) string {
	// Fixed column widths.
	nameW := 26
	statusW := 12
	roleW := 16
	typeW := 12
	cpuW := 20
	memW := 20
	ageW := 8
	ipW := width - nameW - statusW - roleW - typeW - cpuW - memW - ageW
	if ipW < 10 {
		ipW = 10
	}

	accent := lipgloss.NewStyle().Foreground(styles.ColorTextAccent).Bold(true)

	// Header using lipgloss Width.
	header := lipgloss.NewStyle().Width(nameW).Render(accent.Render("NAME")) +
		lipgloss.NewStyle().Width(statusW).Render(accent.Render("STATUS")) +
		lipgloss.NewStyle().Width(roleW).Render(accent.Render("ROLE")) +
		lipgloss.NewStyle().Width(typeW).Render(accent.Render("TYPE")) +
		lipgloss.NewStyle().Width(cpuW).Render(accent.Render("CPU")) +
		lipgloss.NewStyle().Width(memW).Render(accent.Render("MEMORY")) +
		lipgloss.NewStyle().Width(ageW).Render(accent.Render("AGE")) +
		lipgloss.NewStyle().Width(ipW).Render(accent.Render("IP"))

	separator := lipgloss.NewStyle().
		Foreground(styles.ColorBorder).
		Render("  " + strings.Repeat("─", width-4))

	rows := []string{header, separator}

	for i, n := range m.nodes {
		rows = append(rows, m.renderRow(n, i == m.selected, nameW, statusW, roleW, typeW, cpuW, memW, ageW, ipW))
	}

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m Model) renderRow(n k8s.NodeInfo, selected bool, nameW, statusW, roleW, typeW, cpuW, memW, ageW, ipW int) string {
	// Status.
	var statusStr string
	if n.Status == "Ready" {
		statusStr = styles.HealthyStyle.Render("● Ready")
	} else {
		statusStr = styles.CriticalStyle.Render("● " + n.Status)
	}

	// Role.
	role := strings.Join(n.Roles, ",")
	var roleStr string
	if strings.Contains(role, "control-plane") {
		roleStr = lipgloss.NewStyle().Foreground(styles.ColorSecondary).Render(truncate(role, roleW-2))
	} else {
		roleStr = styles.InfoStyle.Render(truncate(role, roleW-2))
	}

	// Instance type.
	instanceType := n.InstanceType
	if instanceType == "" {
		instanceType = "-"
	}

	ip := n.InternalIP
	if ip == "" {
		ip = n.ExternalIP
	}

	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)

	// Build row with lipgloss Width.
	var nameStr string
	if selected {
		nameStr = styles.ValueStyle.Bold(true).Render(truncate(n.Name, nameW-2))
	} else {
		nameStr = styles.ValueStyle.Render(truncate(n.Name, nameW-2))
	}

	row := lipgloss.NewStyle().Width(nameW).Render(nameStr) +
		lipgloss.NewStyle().Width(statusW).Render(statusStr) +
		lipgloss.NewStyle().Width(roleW).Render(roleStr) +
		lipgloss.NewStyle().Width(typeW).Render(dim.Render(instanceType)) +
		lipgloss.NewStyle().Width(cpuW).Render(styles.ProgressBar(n.CPUPercent, 12)) +
		lipgloss.NewStyle().Width(memW).Render(styles.ProgressBar(n.MemoryPercent, 12)) +
		lipgloss.NewStyle().Width(ageW).Render(dim.Render(formatDuration(n.Age))) +
		lipgloss.NewStyle().Width(ipW).Render(dim.Render(ip))

	if selected {
		return lipgloss.NewStyle().Background(styles.ColorBgActive).Render(row)
	}

	return row
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	hours := int(d.Hours())
	if hours > 0 {
		return fmt.Sprintf("%dh", hours)
	}
	minutes := int(d.Minutes())
	return fmt.Sprintf("%dm", minutes)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen > 3 {
		return s[:maxLen-2] + ".."
	}
	return s[:maxLen]
}
