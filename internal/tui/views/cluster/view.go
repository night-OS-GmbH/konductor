package cluster

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/night-OS-GmbH/konductor/internal/config"
	"github.com/night-OS-GmbH/konductor/internal/k8s"
	"github.com/night-OS-GmbH/konductor/internal/tui/styles"
)

type Model struct {
	cfg     *config.Config
	summary *k8s.ClusterSummary
	context string
	err     error
}

func New(cfg *config.Config) Model {
	return Model{cfg: cfg}
}

func (m Model) Init() tea.Cmd {
	return nil
}

// SetData updates the view with fresh cluster data.
func (m *Model) SetData(summary *k8s.ClusterSummary, context string) {
	m.summary = summary
	m.context = context
	m.err = nil
}

func (m *Model) SetError(err error) {
	m.err = err
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	return m, nil
}

func (m Model) View(width, height int) string {
	panelWidth := (width - 3) / 2
	if panelWidth < 30 {
		panelWidth = width - 2
	}

	healthPanel := styles.PanelStyle.
		Width(panelWidth).
		Height(height - 2).
		Render(m.renderHealth())

	infoPanel := styles.PanelStyle.
		Width(panelWidth).
		Height(height - 2).
		Render(m.renderInfo())

	return lipgloss.JoinHorizontal(lipgloss.Top, healthPanel, " ", infoPanel)
}

func (m Model) renderHealth() string {
	if m.err != nil {
		return lipgloss.JoinVertical(lipgloss.Left,
			styles.TitleStyle.Render("Cluster Health"),
			"",
			styles.CriticalStyle.Render("● Disconnected"),
			"",
			styles.SubtitleStyle.Render(m.err.Error()),
		)
	}

	if m.summary == nil {
		return lipgloss.JoinVertical(lipgloss.Left,
			styles.TitleStyle.Render("Cluster Health"),
			"",
			styles.InfoStyle.Render("Connecting..."),
		)
	}

	s := m.summary

	// Determine overall status.
	statusLine := styles.HealthyStyle.Render("● Healthy")
	if s.NotReadyNodes > 0 {
		statusLine = styles.CriticalStyle.Render(fmt.Sprintf("● %d node(s) NotReady", s.NotReadyNodes))
	}
	if s.PendingPods > 0 {
		statusLine = styles.WarningStyle.Render(fmt.Sprintf("● %d pod(s) Pending", s.PendingPods))
	}

	metricsHint := ""
	if !s.HasMetrics {
		metricsHint = styles.SubtitleStyle.Render("  (install metrics-server for usage data)")
	}

	rows := []string{
		styles.TitleStyle.Render("Cluster Health"),
		"",
		row("Status", statusLine),
		row("Nodes", styles.ValueStyle.Render(fmt.Sprintf("%d Ready", s.ReadyNodes))+
			styles.SubtitleStyle.Render(fmt.Sprintf(" / %d total", s.TotalNodes))),
		row("Pods", styles.ValueStyle.Render(fmt.Sprintf("%d Running", s.RunningPods))+
			styles.SubtitleStyle.Render(fmt.Sprintf(" / %d total", s.TotalPods))),
	}

	if s.PendingPods > 0 {
		rows = append(rows, row("Pending", styles.WarningStyle.Render(fmt.Sprintf("%d", s.PendingPods))))
	}
	if s.FailedPods > 0 {
		rows = append(rows, row("Failed", styles.CriticalStyle.Render(fmt.Sprintf("%d", s.FailedPods))))
	}

	rows = append(rows,
		"",
		styles.SubtitleStyle.Render("Resource Usage")+metricsHint,
		"",
		row("CPU", styles.ProgressBar(s.CPUPercent, 20)),
		row("Memory", styles.ProgressBar(s.MemoryPercent, 20)),
	)

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m Model) renderInfo() string {
	if m.summary == nil {
		return lipgloss.JoinVertical(lipgloss.Left,
			styles.TitleStyle.Render("Cluster Info"),
			"",
			styles.SubtitleStyle.Render("Waiting for connection..."),
		)
	}

	s := m.summary

	contextName := m.context
	if contextName == "" {
		contextName = "unknown"
	}

	rows := []string{
		styles.TitleStyle.Render("Cluster Info"),
		"",
		row("Context", styles.ValueStyle.Render(contextName)),
		row("K8s Version", styles.ValueStyle.Render(s.K8sVersion)),
		row("Metrics API", metricsStatus(s.HasMetrics)),
	}

	// Add config info if available.
	if len(m.cfg.Clusters) > 0 {
		c := m.cfg.Clusters[0]
		if c.Hetzner.Location != "" {
			rows = append(rows, row("Location", styles.ValueStyle.Render(c.Hetzner.Location)))
		}
		if c.Scaling.DefaultType != "" {
			rows = append(rows, row("Server Type", styles.ValueStyle.Render(c.Scaling.DefaultType)))
		}

		rows = append(rows,
			"",
			styles.SubtitleStyle.Render("Scaling Rules"),
			"",
			row("Min Nodes", styles.ValueStyle.Render(fmt.Sprintf("%d", c.Scaling.MinNodes))),
			row("Max Nodes", styles.ValueStyle.Render(fmt.Sprintf("%d", c.Scaling.MaxNodes))),
			row("Cooldown", styles.ValueStyle.Render(fmt.Sprintf("%d min", c.Scaling.CooldownMinutes))),
		)
	}

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func row(label, value string) string {
	return styles.LabelStyle.Render(label) + " " + value
}

func metricsStatus(available bool) string {
	if available {
		return styles.HealthyStyle.Render("● available")
	}
	return styles.WarningStyle.Render("● not installed")
}
