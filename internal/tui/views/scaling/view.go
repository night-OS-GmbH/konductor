package scaling

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
	cfg     *config.Config
	scaling *k8s.ScalingInfo
	err     error
}

func New(cfg *config.Config) Model {
	return Model{cfg: cfg}
}

func (m *Model) SetScalingData(info *k8s.ScalingInfo) {
	m.scaling = info
	m.err = nil
}

func (m *Model) SetError(err error) {
	m.err = err
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	return m, nil
}

func (m Model) View(width, height int) string {
	if m.err != nil {
		return styles.PanelStyle.Width(width - 2).Render(
			styles.CriticalStyle.Render("Error: " + m.err.Error()))
	}

	// Operator not installed.
	if m.scaling == nil || !m.scaling.Installed {
		return m.viewNotInstalled(width, height)
	}

	// No NodePool configured.
	if m.scaling.Pool == nil {
		return m.viewNoPool(width, height)
	}

	return m.viewDashboard(width, height)
}

// --- Not Installed ---

func (m Model) viewNotInstalled(width, height int) string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		styles.TitleStyle.Render("Autoscaling"),
		"",
		lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render("Konductor Operator is not installed in this cluster."),
		"",
		lipgloss.NewStyle().Foreground(styles.ColorText).Render("Install it with:"),
		"",
		lipgloss.NewStyle().Foreground(styles.ColorPrimary).Bold(true).Render(
			"  konductor operator install --hetzner-token=<TOKEN> --talos-config=<PATH>"),
		"",
		lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render("The operator runs in-cluster and manages node scaling automatically."),
	)
	return styles.PanelStyle.Width(width - 2).Render(content)
}

// --- No Pool ---

func (m Model) viewNoPool(width, height int) string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		styles.TitleStyle.Render("Autoscaling"),
		"",
		styles.HealthyStyle.Render("● Operator installed"),
		"",
		lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render("No NodePool configured yet. Create one with:"),
		"",
		lipgloss.NewStyle().Foreground(styles.ColorPrimary).Bold(true).Render(
			"  kubectl apply -f nodepool.yaml"),
	)
	return styles.PanelStyle.Width(width - 2).Render(content)
}

// --- Dashboard ---

func (m Model) viewDashboard(width, height int) string {
	pool := m.scaling.Pool
	claims := m.scaling.Claims
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	bright := lipgloss.NewStyle().Foreground(styles.ColorText)

	panelW := (width - 3) / 2

	// --- Left Panel: Pool Status ---
	title := styles.TitleStyle.Render("Autoscaling")

	// Phase badge.
	var phaseBadge string
	switch pool.Phase {
	case "Active", "":
		phaseBadge = styles.Badge("ACTIVE", styles.ColorHealthy)
	case "Scaling":
		phaseBadge = styles.Badge("SCALING", styles.ColorWarning)
	case "Degraded":
		phaseBadge = styles.Badge("DEGRADED", styles.ColorCritical)
	case "Pending":
		phaseBadge = styles.Badge("PENDING", styles.ColorInfo)
	default:
		phaseBadge = styles.Badge(pool.Phase, styles.ColorTextDim)
	}

	// Node counts.
	nodeStr := fmt.Sprintf("%d / %d ready", pool.ReadyNodes, pool.CurrentNodes)
	if pool.CurrentNodes != pool.DesiredNodes {
		nodeStr += fmt.Sprintf(" (desired: %d)", pool.DesiredNodes)
	}

	// Node bar.
	barW := panelW - 22
	if barW < 10 {
		barW = 10
	}
	nodePercent := 0.0
	if pool.MaxNodes > 0 {
		nodePercent = float64(pool.CurrentNodes) / float64(pool.MaxNodes) * 100
	}
	nodeBar := styles.ProgressBar(nodePercent, barW)

	// Last scale time.
	lastScale := dim.Render("never")
	if pool.LastScaleTime != nil {
		ago := time.Since(*pool.LastScaleTime)
		lastScale = bright.Render(formatDuration(ago) + " ago")
	}

	leftContent := lipgloss.JoinVertical(lipgloss.Left,
		title + "  " + phaseBadge,
		"",
		row("Pool", bright.Render(pool.Name)),
		row("Provider", bright.Render(pool.Provider+" / "+pool.ServerType)),
		row("Location", bright.Render(pool.Location)),
		"",
		styles.SubtitleStyle.Render("Nodes"),
		"",
		row("Current", bright.Render(nodeStr)),
		row("Range", bright.Render(fmt.Sprintf("%d – %d", pool.MinNodes, pool.MaxNodes))),
		row("Capacity", nodeBar),
		row("Last Scale", lastScale),
		"",
		styles.SubtitleStyle.Render("Thresholds"),
		"",
		row("Scale Up", dim.Render(fmt.Sprintf("CPU > %d%% or MEM > %d%% for %ds",
			pool.ScaleUp.CPUPercent, pool.ScaleUp.MemoryPercent, pool.ScaleUp.StabilizationSeconds))),
		row("Scale Down", dim.Render(fmt.Sprintf("CPU < %d%% and MEM < %d%% for %ds",
			pool.ScaleDown.CPUPercent, pool.ScaleDown.MemoryPercent, pool.ScaleDown.StabilizationSeconds))),
		row("Cooldown", dim.Render(fmt.Sprintf("%ds", pool.CooldownSeconds))),
	)

	leftPanel := styles.PanelStyle.Width(panelW).Render(leftContent)

	// --- Right Panel: Node Claims ---
	claimTitle := styles.TitleStyle.Render("Managed Nodes")
	claimSubtitle := dim.Render(fmt.Sprintf("  %d claims", len(claims)))

	var claimLines []string
	claimLines = append(claimLines, claimTitle+claimSubtitle)
	claimLines = append(claimLines, "")

	if len(claims) == 0 {
		claimLines = append(claimLines, dim.Render("  No managed nodes"))
	} else {
		// Header.
		accent := lipgloss.NewStyle().Foreground(styles.ColorTextAccent).Bold(true)
		nameW := 22
		phaseW := 16
		nodeW := 20
		ageW := 10

		hdr := lipgloss.NewStyle().Width(nameW).Render(accent.Render("NAME")) +
			lipgloss.NewStyle().Width(phaseW).Render(accent.Render("PHASE")) +
			lipgloss.NewStyle().Width(nodeW).Render(accent.Render("K8S NODE")) +
			lipgloss.NewStyle().Width(ageW).Render(accent.Render("AGE"))
		claimLines = append(claimLines, hdr)

		for _, claim := range claims {
			name := claim.Name
			if len(name) > nameW-2 {
				name = name[:nameW-4] + ".."
			}

			// Phase with color.
			var phaseStr string
			switch claim.Phase {
			case "Ready":
				phaseStr = styles.HealthyStyle.Render("● Ready")
			case "Pending":
				phaseStr = dim.Render("● Pending")
			case "Provisioning":
				phaseStr = styles.InfoStyle.Render("● Provisioning")
			case "Bootstrapping":
				phaseStr = styles.InfoStyle.Render("● Bootstrapping")
			case "Draining":
				phaseStr = styles.WarningStyle.Render("● Draining")
			case "Deleting":
				phaseStr = styles.WarningStyle.Render("● Deleting")
			case "Failed":
				phaseStr = styles.CriticalStyle.Render("● Failed")
			default:
				phaseStr = dim.Render("● " + claim.Phase)
			}

			nodeName := claim.NodeName
			if nodeName == "" {
				nodeName = "–"
			}
			if len(nodeName) > nodeW-2 {
				nodeName = nodeName[:nodeW-4] + ".."
			}

			ageStr := "–"
			if claim.CreatedAt != nil {
				ageStr = formatDuration(time.Since(*claim.CreatedAt))
			}

			line := lipgloss.NewStyle().Width(nameW).Render(bright.Render(name)) +
				lipgloss.NewStyle().Width(phaseW).Render(phaseStr) +
				lipgloss.NewStyle().Width(nodeW).Render(dim.Render(nodeName)) +
				lipgloss.NewStyle().Width(ageW).Render(dim.Render(ageStr))

			claimLines = append(claimLines, line)

			// Show failure reason if present.
			if claim.Failure != "" {
				claimLines = append(claimLines,
					"  "+styles.CriticalStyle.Render("↳ "+claim.Failure))
			}
		}
	}

	rightContent := strings.Join(claimLines, "\n")
	rightPanel := styles.PanelStyle.Width(panelW).Render(rightContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, " ", rightPanel)
}

func row(label, value string) string {
	return styles.LabelStyle.Render(label) + " " + value
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
