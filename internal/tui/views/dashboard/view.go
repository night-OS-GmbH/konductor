package dashboard

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/night-OS-GmbH/konductor/internal/k8s"
	"github.com/night-OS-GmbH/konductor/internal/tui/styles"
)

var sparkBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

type nodeHistory struct {
	cpu []float64
	mem []float64
}

type Model struct {
	nodes       []k8s.NodeInfo
	pods        []k8s.PodInfo
	namespaces  []k8s.NamespaceInfo
	deployments []k8s.DeploymentInfo
	alerts      []k8s.Alert
	history     map[string]*nodeHistory
	maxHistory  int
	context     string
	k8sVersion  string
	hasMetrics  bool
	err         error
}

func New() Model {
	return Model{
		history:    make(map[string]*nodeHistory),
		maxHistory: 40,
	}
}

func (m *Model) SetData(nodes []k8s.NodeInfo, pods []k8s.PodInfo, namespaces []k8s.NamespaceInfo, deployments []k8s.DeploymentInfo, alerts []k8s.Alert, context, k8sVersion string, hasMetrics bool) {
	m.nodes = nodes
	m.pods = pods
	m.namespaces = namespaces
	m.deployments = deployments
	m.alerts = alerts
	m.context = context
	m.k8sVersion = k8sVersion
	m.hasMetrics = hasMetrics
	m.err = nil

	// Append to history for sparklines.
	for _, n := range nodes {
		h, ok := m.history[n.Name]
		if !ok {
			h = &nodeHistory{}
			m.history[n.Name] = h
		}
		h.cpu = append(h.cpu, n.CPUPercent)
		h.mem = append(h.mem, n.MemoryPercent)
		if len(h.cpu) > m.maxHistory {
			h.cpu = h.cpu[len(h.cpu)-m.maxHistory:]
		}
		if len(h.mem) > m.maxHistory {
			h.mem = h.mem[len(h.mem)-m.maxHistory:]
		}
	}
}

func (m *Model) SetError(err error) {
	m.err = err
}

func (m Model) View(width, height int) string {
	if m.err != nil {
		return styles.PanelStyle.Width(width - 2).Render(
			styles.CriticalStyle.Render("Error: " + m.err.Error()))
	}
	if m.nodes == nil {
		return styles.PanelStyle.Width(width - 2).Render(
			styles.InfoStyle.Render("Loading cluster data..."))
	}

	contentWidth := width - 2

	// Node tiles.
	nodeTiles := m.renderNodeTiles(contentWidth)

	// Deployments panel (full width).
	deploymentsPanel := m.renderDeployments(contentWidth)

	// Bottom section: Namespace bars (left) + Pod health (right).
	bottomLeft := m.renderNamespaceBars((contentWidth - 1) / 2)
	bottomRight := m.renderPodHealth((contentWidth - 1) / 2)

	// Equalize bottom panel heights.
	leftH := lipgloss.Height(bottomLeft)
	rightH := lipgloss.Height(bottomRight)
	maxH := leftH
	if rightH > maxH {
		maxH = rightH
	}
	if leftH < maxH {
		bottomLeft = lipgloss.NewStyle().Height(maxH).Render(bottomLeft)
	}
	if rightH < maxH {
		bottomRight = lipgloss.NewStyle().Height(maxH).Render(bottomRight)
	}

	bottomRow := lipgloss.JoinHorizontal(lipgloss.Top, bottomLeft, bottomRight)

	// Alert ticker at the bottom.
	alertTicker := m.renderAlertTicker(contentWidth)

	parts := []string{nodeTiles, deploymentsPanel, bottomRow}
	if alertTicker != "" {
		parts = append(parts, alertTicker)
	}

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// --- Node Tiles ---

func (m Model) renderNodeTiles(width int) string {
	if len(m.nodes) == 0 {
		return ""
	}

	// Responsive: fit as many as possible with a minimum tile width.
	minTileW := 36
	cols := width / minTileW
	if cols < 1 {
		cols = 1
	}
	if cols > len(m.nodes) {
		cols = len(m.nodes)
	}

	tileWidth := width / cols

	// Count pods per node.
	podsPerNode := make(map[string]int)
	for _, pod := range m.pods {
		if pod.Node != "" {
			podsPerNode[pod.Node]++
		}
	}

	// Build rows of tiles.
	var rows []string
	for i := 0; i < len(m.nodes); i += cols {
		var tileParts []string
		for j := 0; j < cols && i+j < len(m.nodes); j++ {
			node := m.nodes[i+j]
			podCount := podsPerNode[node.Name]
			hist := m.history[node.Name]
			tile := m.renderNodeTile(node, hist, podCount, tileWidth)
			tileParts = append(tileParts, tile)
		}

		// Equalize tile heights in this row.
		maxH := 0
		for _, t := range tileParts {
			h := lipgloss.Height(t)
			if h > maxH {
				maxH = h
			}
		}
		for k, t := range tileParts {
			if lipgloss.Height(t) < maxH {
				tileParts[k] = lipgloss.NewStyle().Height(maxH).Render(t)
			}
		}

		row := lipgloss.JoinHorizontal(lipgloss.Top, tileParts...)
		rows = append(rows, row)
	}

	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m Model) renderNodeTile(node k8s.NodeInfo, hist *nodeHistory, podCount, tileWidth int) string {
	// Determine health level for border color.
	borderColor := styles.ColorHealthy
	statusIcon := styles.HealthyStyle.Render("●")

	maxPercent := node.CPUPercent
	if node.MemoryPercent > maxPercent {
		maxPercent = node.MemoryPercent
	}

	if node.Status != "Ready" {
		borderColor = styles.ColorCritical
		statusIcon = styles.CriticalStyle.Render("✕")
	} else if maxPercent > 85 {
		borderColor = styles.ColorCritical
		statusIcon = styles.CriticalStyle.Render("▲")
	} else if maxPercent > 70 {
		borderColor = styles.ColorWarning
		statusIcon = styles.WarningStyle.Render("▲")
	}

	tileStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(tileWidth - 2).
		Padding(0, 1)

	innerWidth := tileWidth - 6
	barWidth := innerWidth - 8
	if barWidth < 10 {
		barWidth = 10
	}

	// Title line: name + status icon.
	name := node.Name
	if len(name) > innerWidth-4 {
		name = name[:innerWidth-6] + ".."
	}
	titleLine := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorText).Render(name) +
		lipgloss.NewStyle().Width(innerWidth-len(name)).Align(lipgloss.Right).Render(statusIcon)

	// CPU bar.
	cpuLabel := lipgloss.NewStyle().Width(4).Foreground(styles.ColorTextDim).Render("CPU")
	cpuBar := styles.ProgressBar(node.CPUPercent, barWidth)

	// MEM bar.
	memLabel := lipgloss.NewStyle().Width(4).Foreground(styles.ColorTextDim).Render("MEM")
	memBar := styles.ProgressBar(node.MemoryPercent, barWidth)

	// Sparkline (CPU history) — 3 rows tall.
	sparkHeight := 3
	sparkWidth := innerWidth
	sparkLabel := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render("cpu history")
	var sparkLines string
	if hist != nil && len(hist.cpu) > 0 {
		sparkColor := styles.ColorHealthy
		if node.CPUPercent > 85 {
			sparkColor = styles.ColorCritical
		} else if node.CPUPercent > 70 {
			sparkColor = styles.ColorWarning
		}
		sparkLines = renderMultilineSparkline(hist.cpu, sparkWidth, sparkHeight, sparkColor)
	} else {
		var emptyRows []string
		emptyRow := lipgloss.NewStyle().Foreground(styles.ColorBorder).Render(
			strings.Repeat(string(sparkBlocks[0]), sparkWidth))
		for i := 0; i < sparkHeight; i++ {
			emptyRows = append(emptyRows, emptyRow)
		}
		sparkLines = strings.Join(emptyRows, "\n")
	}

	// Footer: role + pod count.
	role := "worker"
	for _, r := range node.Roles {
		if r == "control-plane" {
			role = "control-plane"
			break
		}
	}
	roleStr := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render(role)
	podStr := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render(fmt.Sprintf("%d pods", podCount))
	footerGap := innerWidth - lipgloss.Width(roleStr) - lipgloss.Width(podStr)
	if footerGap < 1 {
		footerGap = 1
	}
	footerLine := roleStr + strings.Repeat(" ", footerGap) + podStr

	content := strings.Join([]string{
		titleLine,
		cpuLabel + " " + cpuBar,
		memLabel + " " + memBar,
		"",
		sparkLabel,
		sparkLines,
		footerLine,
	}, "\n")

	return tileStyle.Render(content)
}

// --- Sparkline (multi-line) ---

func renderMultilineSparkline(values []float64, width, height int, color lipgloss.Color) string {
	// Determine which data points to use.
	dataLen := len(values)
	if dataLen > width {
		dataLen = width
	}
	padLen := width - dataLen
	startIdx := 0
	if len(values) > width {
		startIdx = len(values) - width
	}

	// Normalize values to 0.0–1.0.
	cols := make([]float64, width)
	for i := startIdx; i < len(values); i++ {
		v := values[i] / 100.0
		if v > 1.0 {
			v = 1.0
		}
		if v < 0 {
			v = 0
		}
		cols[padLen+i-startIdx] = v
	}

	// Render row by row, top to bottom.
	lines := make([]string, height)
	for row := 0; row < height; row++ {
		rowBottom := float64(height-1-row) / float64(height)
		rowTop := float64(height-row) / float64(height)
		rowChars := make([]rune, width)
		for c, val := range cols {
			if val <= rowBottom {
				rowChars[c] = ' '
			} else if val >= rowTop {
				rowChars[c] = sparkBlocks[7]
			} else {
				fraction := (val - rowBottom) / (rowTop - rowBottom)
				idx := int(fraction * 8)
				if idx > 7 {
					idx = 7
				}
				if idx < 1 {
					idx = 1
				}
				rowChars[c] = sparkBlocks[idx]
			}
		}
		lines[row] = lipgloss.NewStyle().Foreground(color).Render(string(rowChars))
	}

	return strings.Join(lines, "\n")
}

// --- Namespace Bars ---

func (m Model) renderNamespaceBars(width int) string {
	var totalClusterMem int64
	for _, n := range m.nodes {
		totalClusterMem += n.MemoryCapacity.Value()
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorText).MarginBottom(1).Render("Namespaces")

	if len(m.namespaces) == 0 {
		return styles.PanelStyle.Width(width).Render(
			title + "\n" + lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render("No data"))
	}

	nameW := 18
	barW := width - nameW - 14
	if barW < 10 {
		barW = 10
	}

	var lines []string
	lines = append(lines, title)

	maxNS := 10
	if len(m.namespaces) < maxNS {
		maxNS = len(m.namespaces)
	}

	// Find max pods for relative bar scaling.
	maxPods := 1
	for _, ns := range m.namespaces[:maxNS] {
		if ns.PodCount > maxPods {
			maxPods = ns.PodCount
		}
	}

	for _, ns := range m.namespaces[:maxNS] {
		name := ns.Name
		if len(name) > nameW-1 {
			name = name[:nameW-3] + ".."
		}

		// Bar proportional to pod count (relative to biggest namespace).
		barFill := ns.PodCount * barW / maxPods
		if barFill < 1 && ns.PodCount > 0 {
			barFill = 1
		}
		barEmpty := barW - barFill

		// Bar color based on health.
		barColor := styles.ColorHealthy
		if ns.FailedPods > 0 || ns.WarningPods > 2 {
			barColor = styles.ColorCritical
		} else if ns.PendingPods > 0 || ns.WarningPods > 0 {
			barColor = styles.ColorWarning
		}

		bar := lipgloss.NewStyle().Foreground(barColor).Render(strings.Repeat("█", barFill)) +
			lipgloss.NewStyle().Foreground(styles.ColorBorder).Render(strings.Repeat("░", barEmpty))

		podStr := lipgloss.NewStyle().Width(5).Align(lipgloss.Right).Foreground(styles.ColorTextDim).Render(
			fmt.Sprintf("%dp", ns.PodCount))

		nameCol := lipgloss.NewStyle().Width(nameW).Foreground(styles.ColorText).Render(name)
		lines = append(lines, nameCol+bar+" "+podStr)
	}

	content := strings.Join(lines, "\n")
	return styles.PanelStyle.Width(width).Render(content)
}

// --- Pod Health ---

func (m Model) renderPodHealth(width int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorText).MarginBottom(1).Render("Pod Health")

	var running, pending, failed, crashloop, succeeded, total int
	for _, pod := range m.pods {
		total++
		switch {
		case pod.IsCrashLoop:
			crashloop++
		case pod.Phase == "Failed":
			failed++
		case pod.Phase == "Pending":
			pending++
		case pod.Phase == "Succeeded":
			succeeded++
		case pod.Phase == "Running":
			running++
		}
	}

	// Stacked bar.
	barW := width - 6
	if barW < 20 {
		barW = 20
	}
	activePods := running + pending + failed + crashloop
	if activePods == 0 {
		activePods = 1
	}

	runW := running * barW / activePods
	pendW := pending * barW / activePods
	crashW := crashloop * barW / activePods
	failW := failed * barW / activePods
	// Ensure at least 1 char for non-zero values.
	if pending > 0 && pendW == 0 {
		pendW = 1
	}
	if crashloop > 0 && crashW == 0 {
		crashW = 1
	}
	if failed > 0 && failW == 0 {
		failW = 1
	}
	// Recalculate runW to fill remaining.
	runW = barW - pendW - crashW - failW
	if runW < 0 {
		runW = 0
	}

	bar := lipgloss.NewStyle().Foreground(styles.ColorHealthy).Render(strings.Repeat("█", runW)) +
		lipgloss.NewStyle().Foreground(styles.ColorWarning).Render(strings.Repeat("█", pendW)) +
		lipgloss.NewStyle().Foreground(styles.ColorCritical).Render(strings.Repeat("█", crashW+failW))

	// Stats with colored dots.
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	statLines := []string{
		fmt.Sprintf("  %s %s",
			styles.HealthyStyle.Render("●"),
			lipgloss.NewStyle().Foreground(styles.ColorText).Render(fmt.Sprintf("%d Running", running))),
		fmt.Sprintf("  %s %s",
			styles.WarningStyle.Render("●"),
			dim.Render(fmt.Sprintf("%d Pending", pending))),
		fmt.Sprintf("  %s %s",
			styles.CriticalStyle.Render("●"),
			dim.Render(fmt.Sprintf("%d CrashLoop", crashloop))),
		fmt.Sprintf("  %s %s",
			styles.CriticalStyle.Render("●"),
			dim.Render(fmt.Sprintf("%d Failed", failed))),
	}

	if succeeded > 0 {
		statLines = append(statLines, fmt.Sprintf("  %s %s",
			dim.Render("●"),
			dim.Render(fmt.Sprintf("%d Completed", succeeded))))
	}

	// Cluster summary line.
	summaryLine := dim.Render(fmt.Sprintf("\n  %d total across %d namespaces", total, len(m.namespaces)))

	var lines []string
	lines = append(lines, title)
	lines = append(lines, bar)
	lines = append(lines, "")
	lines = append(lines, statLines...)
	lines = append(lines, summaryLine)

	content := strings.Join(lines, "\n")
	return styles.PanelStyle.Width(width).Render(content)
}

// --- Deployment Cards ---

func (m Model) renderDeployments(width int) string {
	if len(m.deployments) == 0 {
		return ""
	}

	// Count stats.
	var totalDeploys, healthyDeploys, degradedDeploys, failedDeploys int
	for _, d := range m.deployments {
		totalDeploys++
		switch d.Status {
		case k8s.DeployStatusHealthy:
			healthyDeploys++
		case k8s.DeployStatusDegraded, k8s.DeployStatusProgressing:
			degradedDeploys++
		case k8s.DeployStatusFailed:
			failedDeploys++
		}
	}

	// Title with summary badge.
	titleStr := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorText).Render("Deployments")
	var summaryBadge string
	if failedDeploys > 0 {
		summaryBadge = "  " + styles.Badge(fmt.Sprintf(" %d/%d ", healthyDeploys, totalDeploys), styles.ColorCritical)
	} else if degradedDeploys > 0 {
		summaryBadge = "  " + styles.Badge(fmt.Sprintf(" %d/%d ", healthyDeploys, totalDeploys), styles.ColorWarning)
	} else {
		summaryBadge = "  " + styles.Badge(fmt.Sprintf(" %d/%d ", healthyDeploys, totalDeploys), styles.ColorHealthy)
	}
	title := titleStr + summaryBadge

	// Responsive grid: cards with rounded borders need more space.
	minCardW := 26
	cols := width / minCardW
	if cols < 1 {
		cols = 1
	}
	if cols > len(m.deployments) {
		cols = len(m.deployments)
	}
	cardW := width / cols

	var rows []string
	for i := 0; i < len(m.deployments); i += cols {
		var tileParts []string
		for j := 0; j < cols && i+j < len(m.deployments); j++ {
			d := m.deployments[i+j]
			tileParts = append(tileParts, renderDeployCard(d, cardW))
		}
		// Pad incomplete row.
		for len(tileParts) < cols {
			tileParts = append(tileParts, lipgloss.NewStyle().Width(cardW).Render(""))
		}
		row := lipgloss.JoinHorizontal(lipgloss.Top, tileParts...)
		rows = append(rows, row)
	}

	tilesContent := lipgloss.JoinVertical(lipgloss.Left, rows...)

	return lipgloss.JoinVertical(lipgloss.Left, title, "", tilesContent)
}

func renderDeployCard(d k8s.DeploymentInfo, cardW int) string {
	// Border + status color.
	borderColor := styles.ColorHealthy
	var badgeLabel string
	var badgeColor lipgloss.Color
	switch d.Status {
	case k8s.DeployStatusHealthy:
		borderColor = styles.ColorHealthy
		badgeLabel = "HEALTHY"
		badgeColor = styles.ColorHealthy
	case k8s.DeployStatusProgressing:
		borderColor = styles.ColorInfo
		badgeLabel = "ROLLING"
		badgeColor = styles.ColorInfo
	case k8s.DeployStatusDegraded:
		borderColor = styles.ColorWarning
		badgeLabel = "DEGRADED"
		badgeColor = styles.ColorWarning
	case k8s.DeployStatusFailed:
		borderColor = styles.ColorCritical
		badgeLabel = "FAILED"
		badgeColor = styles.ColorCritical
	}

	// Card border with padding.
	cardStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Width(cardW - 2). // -2 for border
		Padding(1, 2)

	innerW := cardW - 8 // border(2) + padding(4)
	if innerW < 8 {
		innerW = 8
	}

	// Name.
	name := d.Name
	if len(name) > innerW {
		name = name[:innerW-2] + ".."
	}
	nameStr := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorText).Render(name)

	// Status badge + replica count on same line.
	badge := styles.Badge(badgeLabel, badgeColor)
	replicaStr := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render(
		fmt.Sprintf("%d/%d", d.ReadyReplicas, d.DesiredReplicas))
	statusLine := badge + "  " + replicaStr

	// Replica health mini-bar.
	barW := innerW
	replicaPercent := 0.0
	if d.DesiredReplicas > 0 {
		replicaPercent = float64(d.ReadyReplicas) / float64(d.DesiredReplicas) * 100
	}
	bar := styles.MiniBar(replicaPercent, barW, borderColor)

	content := lipgloss.JoinVertical(lipgloss.Left,
		nameStr,
		"",
		statusLine,
		bar,
	)

	return cardStyle.Render(content)
}

// --- Alert Ticker ---

func (m Model) renderAlertTicker(width int) string {
	if len(m.alerts) == 0 {
		return ""
	}

	// Build a compact single-line ticker of critical/warning alerts.
	var parts []string
	for _, alert := range m.alerts {
		if alert.Level < k8s.AlertWarning {
			continue
		}
		var icon string
		if alert.Level == k8s.AlertCritical {
			icon = styles.CriticalStyle.Render("▲")
		} else {
			icon = styles.WarningStyle.Render("▲")
		}

		resource := alert.Resource
		if alert.Namespace != "" {
			resource = alert.Namespace + "/" + alert.Resource
		}
		if len(resource) > 30 {
			resource = resource[:28] + ".."
		}

		parts = append(parts, fmt.Sprintf("%s %s %s",
			icon,
			lipgloss.NewStyle().Foreground(styles.ColorText).Render(resource),
			lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render(alert.Message)))
	}

	if len(parts) == 0 {
		return ""
	}

	ticker := strings.Join(parts, "  │  ")

	// Truncate if too wide.
	if lipgloss.Width(ticker) > width-4 {
		// Show first few alerts that fit.
		ticker = ""
		for i, part := range parts {
			if i > 0 {
				candidate := ticker + "  │  " + part
				if lipgloss.Width(candidate) > width-12 {
					ticker += lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render(
						fmt.Sprintf("  +%d more", len(parts)-i))
					break
				}
				ticker = candidate
			} else {
				ticker = part
			}
		}
	}

	return lipgloss.NewStyle().
		Foreground(styles.ColorTextDim).
		Width(width).
		Padding(0, 1).
		Render(ticker)
}
