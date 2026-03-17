package namespaces

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/night-OS-GmbH/konductor/internal/k8s"
	"github.com/night-OS-GmbH/konductor/internal/tui/styles"
)

// NamespaceSelectedMsg is emitted when the user presses Enter on a namespace.
type NamespaceSelectedMsg struct {
	Namespace string
}

type Model struct {
	namespaces     []k8s.NamespaceInfo
	totalCPUMillis int64
	totalMemBytes  int64
	selected       int
	err            error
}

func New() Model {
	return Model{}
}

func (m *Model) SetData(namespaces []k8s.NamespaceInfo, nodes []k8s.NodeInfo) {
	m.namespaces = namespaces
	m.err = nil

	m.totalCPUMillis = 0
	m.totalMemBytes = 0
	for _, n := range nodes {
		m.totalCPUMillis += n.CPUCapacity.MilliValue()
		m.totalMemBytes += n.MemoryCapacity.Value()
	}

	if m.selected >= len(namespaces) {
		m.selected = max(0, len(namespaces)-1)
	}
}

func (m *Model) SetError(err error) {
	m.err = err
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok {
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
			if m.selected < len(m.namespaces)-1 {
				m.selected++
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
			if m.selected > 0 {
				m.selected--
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
			if m.selected < len(m.namespaces) {
				ns := m.namespaces[m.selected]
				return m, func() tea.Msg {
					return NamespaceSelectedMsg{Namespace: ns.Name}
				}
			}
		}
	}
	return m, nil
}

func (m Model) View(width, height int) string {
	if m.err != nil {
		return styles.PanelStyle.Width(width - 2).Render(
			styles.CriticalStyle.Render("Error: " + m.err.Error()))
	}
	if m.namespaces == nil {
		return styles.PanelStyle.Width(width - 2).Render(
			styles.InfoStyle.Render("Loading namespace data..."))
	}

	title := styles.TitleStyle.Render("Namespaces")
	subtitle := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render(
		fmt.Sprintf("  %d namespaces with pods", len(m.namespaces)))

	header := title + subtitle

	table := m.renderTable(width-6, height-6)

	content := header + "\n\n" + table
	return styles.PanelStyle.Width(width - 2).Render(content)
}

func (m Model) renderTable(width, maxRows int) string {
	if len(m.namespaces) == 0 {
		return lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render("  No namespaces with pods")
	}

	// Column widths — compact layout that won't overflow.
	nameW := 22
	podsW := 6
	runW := 6
	pendW := 6
	failW := 6
	statusW := 12
	cpuW := 10
	memW := 10

	// Remaining space for bars.
	usedW := nameW + podsW + runW + pendW + failW + statusW + cpuW + memW
	remainW := width - usedW
	cpuBarW := remainW / 2
	memBarW := remainW - cpuBarW
	if cpuBarW < 8 {
		cpuBarW = 8
	}
	if memBarW < 8 {
		memBarW = 8
	}

	accent := lipgloss.NewStyle().Foreground(styles.ColorTextAccent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	bright := lipgloss.NewStyle().Foreground(styles.ColorText)

	// Header.
	hdr := lipgloss.NewStyle().Width(nameW).Render(accent.Render("NAMESPACE")) +
		lipgloss.NewStyle().Width(podsW).Render(accent.Render("PODS")) +
		lipgloss.NewStyle().Width(runW).Render(accent.Render("RUN")) +
		lipgloss.NewStyle().Width(pendW).Render(accent.Render("PEND")) +
		lipgloss.NewStyle().Width(failW).Render(accent.Render("FAIL")) +
		lipgloss.NewStyle().Width(statusW).Render(accent.Render("STATUS")) +
		lipgloss.NewStyle().Width(cpuW).Render(accent.Render("CPU")) +
		lipgloss.NewStyle().Width(cpuBarW).Render("") +
		lipgloss.NewStyle().Width(memW).Render(accent.Render("MEMORY"))

	var lines []string
	lines = append(lines, hdr)

	// Scroll.
	visibleRows := maxRows - 2
	if visibleRows < 5 {
		visibleRows = 5
	}
	startIdx := 0
	if m.selected >= startIdx+visibleRows {
		startIdx = m.selected - visibleRows + 1
	}
	endIdx := startIdx + visibleRows
	if endIdx > len(m.namespaces) {
		endIdx = len(m.namespaces)
		startIdx = max(0, endIdx-visibleRows)
	}

	for i := startIdx; i < endIdx; i++ {
		ns := m.namespaces[i]
		selected := i == m.selected

		name := ns.Name
		if len(name) > nameW-2 {
			name = name[:nameW-4] + ".."
		}

		// Status.
		var statusStr string
		if ns.FailedPods > 0 {
			statusStr = styles.CriticalStyle.Render("● Failed")
		} else if ns.PendingPods > 0 || ns.WarningPods > 0 {
			statusStr = styles.WarningStyle.Render("● Warning")
		} else {
			statusStr = styles.HealthyStyle.Render("● Healthy")
		}

		// CPU.
		cpuPercent := 0.0
		if m.totalCPUMillis > 0 {
			cpuPercent = float64(ns.CPUUsage.MilliValue()) / float64(m.totalCPUMillis) * 100
		}
		cpuText := k8s.FormatCPU(ns.CPUUsage)
		cpuBar := styles.MiniBar(cpuPercent, cpuBarW-1, barColor(cpuPercent))

		// Memory.
		memPercent := 0.0
		if m.totalMemBytes > 0 {
			memPercent = float64(ns.MemUsage.Value()) / float64(m.totalMemBytes) * 100
		}
		memText := k8s.FormatMemory(ns.MemUsage)
		memBar := styles.MiniBar(memPercent, memBarW-1, barColor(memPercent))

		// Pending/Failed with color.
		pendStr := dim.Render(fmt.Sprintf("%d", ns.PendingPods))
		if ns.PendingPods > 0 {
			pendStr = styles.WarningStyle.Render(fmt.Sprintf("%d", ns.PendingPods))
		}
		failStr := dim.Render(fmt.Sprintf("%d", ns.FailedPods))
		if ns.FailedPods > 0 {
			failStr = styles.CriticalStyle.Render(fmt.Sprintf("%d", ns.FailedPods))
		}

		var nameStr string
		if selected {
			nameStr = bright.Bold(true).Render(name)
		} else {
			nameStr = bright.Render(name)
		}

		row := lipgloss.NewStyle().Width(nameW).Render(nameStr) +
			lipgloss.NewStyle().Width(podsW).Render(dim.Render(fmt.Sprintf("%d", ns.PodCount))) +
			lipgloss.NewStyle().Width(runW).Render(dim.Render(fmt.Sprintf("%d", ns.RunningPods))) +
			lipgloss.NewStyle().Width(pendW).Render(pendStr) +
			lipgloss.NewStyle().Width(failW).Render(failStr) +
			lipgloss.NewStyle().Width(statusW).Render(statusStr) +
			lipgloss.NewStyle().Width(cpuW).Render(dim.Render(cpuText)) +
			cpuBar + " " +
			lipgloss.NewStyle().Width(memW).Render(dim.Render(memText)) +
			memBar

		if selected {
			row = lipgloss.NewStyle().Background(styles.ColorBgActive).Width(width).Render(row)
		}

		lines = append(lines, row)
	}

	if len(m.namespaces) > visibleRows {
		lines = append(lines, dim.Render(fmt.Sprintf(
			"  %d/%d namespaces (↑↓ select, enter → pods)",
			min(m.selected+1, len(m.namespaces)), len(m.namespaces))))
	}

	return strings.Join(lines, "\n")
}

func barColor(percent float64) lipgloss.Color {
	switch {
	case percent >= 80:
		return styles.ColorCritical
	case percent >= 50:
		return styles.ColorWarning
	default:
		return styles.ColorHealthy
	}
}
