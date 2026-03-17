package overview

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/night-OS-GmbH/konductor/internal/k8s"
	"github.com/night-OS-GmbH/konductor/internal/tui/styles"
)

type Model struct {
	nodes      []k8s.NodeInfo
	pods       []k8s.PodInfo
	namespaces []k8s.NamespaceInfo
	alerts     []k8s.Alert
	context    string
	k8sVersion string
	hasMetrics bool
	err        error
}

func New() Model {
	return Model{}
}

func (m *Model) SetData(nodes []k8s.NodeInfo, pods []k8s.PodInfo, namespaces []k8s.NamespaceInfo, alerts []k8s.Alert, context, k8sVersion string, hasMetrics bool) {
	m.nodes = nodes
	m.pods = pods
	m.namespaces = namespaces
	m.alerts = alerts
	m.context = context
	m.k8sVersion = k8sVersion
	m.hasMetrics = hasMetrics
	m.err = nil
}

func (m *Model) SetError(err error) {
	m.err = err
}

func (m Model) View(width, height int) string {
	if m.err != nil {
		return styles.PanelStyle.Width(width - 2).Render(
			styles.CriticalStyle.Render("Error: " + m.err.Error()),
		)
	}
	if m.nodes == nil {
		return styles.PanelStyle.Width(width - 2).Render(
			styles.InfoStyle.Render("Loading cluster data..."),
		)
	}

	contentWidth := width - 2

	topRow := m.renderTopRow(contentWidth)
	alertsPanel := m.renderAlerts(contentWidth)
	nsPanel := m.renderNamespaces(contentWidth)

	return lipgloss.JoinVertical(lipgloss.Left, topRow, alertsPanel, nsPanel)
}

func (m Model) renderTopRow(width int) string {
	panelWidth := (width - 1) / 2

	healthContent := m.renderHealthContent()
	resourcesContent := m.renderResourcesContent(panelWidth)

	// Render both panels and equalize height.
	leftPanel := styles.PanelStyle.Width(panelWidth).Render(healthContent)
	rightPanel := styles.PanelStyle.Width(panelWidth).Render(resourcesContent)

	leftH := lipgloss.Height(leftPanel)
	rightH := lipgloss.Height(rightPanel)
	maxH := leftH
	if rightH > maxH {
		maxH = rightH
	}

	leftPanel = styles.PanelStyle.Width(panelWidth).Height(maxH - 2).Render(healthContent)
	rightPanel = styles.PanelStyle.Width(panelWidth).Height(maxH - 2).Render(resourcesContent)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
}

func (m Model) renderHealthContent() string {
	var readyNodes, totalNodes, runningPods, totalPods, pendingPods, failedPods int
	totalNodes = len(m.nodes)
	for _, n := range m.nodes {
		if n.Status == "Ready" {
			readyNodes++
		}
	}
	totalPods = len(m.pods)
	for _, p := range m.pods {
		switch p.Phase {
		case "Running":
			runningPods++
		case "Pending":
			pendingPods++
		case "Failed":
			failedPods++
		}
	}

	var statusLine string
	if readyNodes == totalNodes && failedPods == 0 && pendingPods == 0 {
		statusLine = styles.HealthyStyle.Render("● Healthy")
	} else if readyNodes < totalNodes {
		statusLine = styles.CriticalStyle.Render(fmt.Sprintf("● %d node(s) NotReady", totalNodes-readyNodes))
	} else if failedPods > 0 {
		statusLine = styles.CriticalStyle.Render(fmt.Sprintf("● %d pod(s) Failed", failedPods))
	} else {
		statusLine = styles.WarningStyle.Render(fmt.Sprintf("● %d pod(s) Pending", pendingPods))
	}

	labelStyle := lipgloss.NewStyle().Width(12).Foreground(styles.ColorTextDim)
	valStyle := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorText)

	lines := []string{
		styles.TitleStyle.Render("Cluster Health"),
		statusLine,
		"",
		labelStyle.Render("Nodes") + valStyle.Render(fmt.Sprintf("%d Ready / %d Total", readyNodes, totalNodes)),
		labelStyle.Render("Pods") + valStyle.Render(fmt.Sprintf("%d Running / %d Total", runningPods, totalPods)),
		labelStyle.Render("K8s") + valStyle.Render(m.k8sVersion),
		labelStyle.Render("Context") + styles.InfoStyle.Render(m.context),
	}

	if m.hasMetrics {
		lines = append(lines, labelStyle.Render("Metrics")+styles.HealthyStyle.Render("● available"))
	} else {
		lines = append(lines, labelStyle.Render("Metrics")+styles.WarningStyle.Render("● not installed"))
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderResourcesContent(panelWidth int) string {
	var totalCPU, usedCPU, totalMem, usedMem int64
	for _, n := range m.nodes {
		totalCPU += n.CPUCapacity.MilliValue()
		usedCPU += n.CPUUsage.MilliValue()
		totalMem += n.MemoryCapacity.Value()
		usedMem += n.MemoryUsage.Value()
	}

	cpuPercent := 0.0
	if totalCPU > 0 {
		cpuPercent = float64(usedCPU) / float64(totalCPU) * 100
	}
	memPercent := 0.0
	if totalMem > 0 {
		memPercent = float64(usedMem) / float64(totalMem) * 100
	}

	barWidth := panelWidth - 16
	if barWidth < 15 {
		barWidth = 15
	}

	labelStyle := lipgloss.NewStyle().Width(10).Bold(true).Foreground(styles.ColorText)
	detailStyle := lipgloss.NewStyle().Foreground(styles.ColorTextDim)

	var podCapacity int64
	for _, n := range m.nodes {
		podCapacity += n.PodCapacity.Value()
	}

	lines := []string{
		styles.TitleStyle.Render("Resources"),
		"",
		labelStyle.Render("CPU") + styles.ProgressBar(cpuPercent, barWidth),
		lipgloss.NewStyle().Width(10).Render("") + detailStyle.Render(
			fmt.Sprintf("%.1f / %.1f cores", float64(usedCPU)/1000, float64(totalCPU)/1000)),
		"",
		labelStyle.Render("Memory") + styles.ProgressBar(memPercent, barWidth),
		lipgloss.NewStyle().Width(10).Render("") + detailStyle.Render(
			fmt.Sprintf("%.1f / %.1f GiB", float64(usedMem)/(1024*1024*1024), float64(totalMem)/(1024*1024*1024))),
		"",
		labelStyle.Render("Pods") + lipgloss.NewStyle().Foreground(styles.ColorText).Render(
			fmt.Sprintf("%d / %d", len(m.pods), podCapacity)),
	}

	return strings.Join(lines, "\n")
}

func (m Model) renderAlerts(width int) string {
	title := styles.TitleStyle.Render(fmt.Sprintf("Alerts (%d)", len(m.alerts)))

	if len(m.alerts) == 0 {
		content := title + "\n\n" + styles.HealthyStyle.Render("  No alerts — cluster is healthy")
		return styles.PanelStyle.Width(width).Render(content)
	}

	var lines []string
	lines = append(lines, title)

	maxAlerts := 8
	if len(m.alerts) < maxAlerts {
		maxAlerts = len(m.alerts)
	}

	// Use fixed-width columns with lipgloss for proper alignment.
	levelW := 8
	resW := width - levelW - 10
	if resW > width/2 {
		resW = width / 2
	}

	for _, alert := range m.alerts[:maxAlerts] {
		var icon string
		var levelStr string
		switch alert.Level {
		case k8s.AlertCritical:
			icon = styles.CriticalStyle.Render("▲")
			levelStr = lipgloss.NewStyle().Width(levelW).Render(styles.CriticalStyle.Render("CRIT"))
		case k8s.AlertWarning:
			icon = styles.WarningStyle.Render("▲")
			levelStr = lipgloss.NewStyle().Width(levelW).Render(styles.WarningStyle.Render("WARN"))
		default:
			icon = styles.InfoStyle.Render("●")
			levelStr = lipgloss.NewStyle().Width(levelW).Render(styles.InfoStyle.Render("INFO"))
		}

		resource := alert.Resource
		if alert.Namespace != "" {
			resource = alert.Namespace + "/" + alert.Resource
		}
		if len(resource) > resW-2 {
			resource = resource[:resW-4] + ".."
		}

		resCol := lipgloss.NewStyle().Width(resW).Foreground(styles.ColorText).Render(resource)
		msgCol := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render(alert.Message)

		lines = append(lines, fmt.Sprintf("  %s %s%s  %s", icon, levelStr, resCol, msgCol))
	}

	if len(m.alerts) > maxAlerts {
		lines = append(lines, lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render(
			fmt.Sprintf("  ... and %d more", len(m.alerts)-maxAlerts)))
	}

	content := strings.Join(lines, "\n")
	return styles.PanelStyle.Width(width).Render(content)
}

func (m Model) renderNamespaces(width int) string {
	title := styles.TitleStyle.Render("Top Namespaces")

	if len(m.namespaces) == 0 {
		return styles.PanelStyle.Width(width).Render(title + "\n\n" + styles.InfoStyle.Render("  No data"))
	}

	var totalClusterCPU, totalClusterMem int64
	for _, n := range m.nodes {
		totalClusterCPU += n.CPUCapacity.MilliValue()
		totalClusterMem += n.MemoryCapacity.Value()
	}

	// Fixed column widths.
	nameW := 20
	podsW := 10
	barW := (width - nameW - podsW - 30) / 2
	if barW < 12 {
		barW = 12
	}
	cpuValW := 12
	memValW := 12

	var lines []string
	lines = append(lines, title)

	// Header row using lipgloss widths.
	accent := lipgloss.NewStyle().Foreground(styles.ColorTextAccent).Bold(true)
	headerRow := lipgloss.NewStyle().Width(nameW).Render(accent.Render("NAMESPACE")) +
		lipgloss.NewStyle().Width(podsW).Render(accent.Render("PODS")) +
		lipgloss.NewStyle().Width(barW+cpuValW).Render(accent.Render("CPU")) +
		lipgloss.NewStyle().Width(barW+memValW).Render(accent.Render("MEMORY"))
	lines = append(lines, headerRow)

	maxNS := 8
	if len(m.namespaces) < maxNS {
		maxNS = len(m.namespaces)
	}

	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)

	for _, ns := range m.namespaces[:maxNS] {
		cpuPercent := 0.0
		if totalClusterCPU > 0 {
			cpuPercent = float64(ns.CPUUsage.MilliValue()) / float64(totalClusterCPU) * 100
		}
		memPercent := 0.0
		if totalClusterMem > 0 {
			memPercent = float64(ns.MemUsage.Value()) / float64(totalClusterMem) * 100
		}

		name := ns.Name
		if len(name) > nameW-1 {
			name = name[:nameW-3] + ".."
		}

		podStr := fmt.Sprintf("%d", ns.PodCount)
		if ns.WarningPods > 0 {
			podStr += styles.WarningStyle.Render(fmt.Sprintf(" (%d!)", ns.WarningPods))
		}

		cpuBar := styles.ProgressBar(cpuPercent, barW) + " " + dim.Render(k8s.FormatCPU(ns.CPUUsage))
		memBar := styles.ProgressBar(memPercent, barW) + " " + dim.Render(k8s.FormatMemory(ns.MemUsage))

		row := lipgloss.NewStyle().Width(nameW).Foreground(styles.ColorText).Render(name) +
			lipgloss.NewStyle().Width(podsW).Render(podStr) +
			lipgloss.NewStyle().Width(barW + cpuValW).Render(cpuBar) +
			lipgloss.NewStyle().Width(barW + memValW).Render(memBar)

		lines = append(lines, row)
	}

	content := strings.Join(lines, "\n")
	return styles.PanelStyle.Width(width).Render(content)
}
