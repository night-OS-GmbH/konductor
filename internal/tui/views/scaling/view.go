package scaling

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/night-OS-GmbH/konductor/internal/cluster"
	"github.com/night-OS-GmbH/konductor/internal/config"
	"github.com/night-OS-GmbH/konductor/internal/k8s"
	"github.com/night-OS-GmbH/konductor/internal/operator"
	"github.com/night-OS-GmbH/konductor/internal/tui/styles"
)

// ClusterHealthData holds the cluster health information for display.
type ClusterHealthData struct {
	Connected    bool
	K8sVersion   string
	TalosVersion string
	Components   []ComponentDisplay
}

// ComponentDisplay represents a single component's display state.
type ComponentDisplay struct {
	Name        string
	Status      string // "running", "not_installed", "outdated", "degraded"
	Version     string
	Latest      string
	Installable bool
	Description string
}

// InstallComponentMsg is emitted when the user confirms installation of a component.
type InstallComponentMsg struct {
	Component string
	Opts      map[string]string
}

// CreateNodePoolMsg is emitted when the user confirms NodePool creation.
type CreateNodePoolMsg struct {
	ServerType string
	Location   string
	MinNodes   int32
	MaxNodes   int32
	Enabled    bool
}

// ImportNodesMsg is emitted when the user triggers node import detection.
type ImportNodesMsg struct{}

// ImportConfirmMsg is emitted when the user confirms import of suggested pools.
type ImportConfirmMsg struct {
	Pools []operator.SuggestedPool
}

// InstallProgressMsg reports progress/completion of a component installation.
type InstallProgressMsg struct {
	Component string
	Message   string
	Done      bool
	Err       error
}

// Model is the Cluster tab view, combining health dashboard and autoscaling info.
type Model struct {
	cfg          *config.Config
	scaling      *k8s.ScalingInfo
	health       *ClusterHealthData
	selected     int
	selectedPool int
	wizard       *WizardModel
	err          error
}

// New creates a new scaling/cluster view Model.
func New(cfg *config.Config) Model {
	return Model{cfg: cfg}
}

// SetScalingData updates the autoscaling information.
func (m *Model) SetScalingData(info *k8s.ScalingInfo) {
	m.scaling = info
	m.err = nil
	// Clamp selectedPool to valid range.
	if info != nil && m.selectedPool >= len(info.Pools) {
		if len(info.Pools) > 0 {
			m.selectedPool = len(info.Pools) - 1
		} else {
			m.selectedPool = 0
		}
	}
}

// SetHealthData updates the cluster health information.
func (m *Model) SetHealthData(data *ClusterHealthData) {
	m.health = data
}

// SetError records an error for display.
func (m *Model) SetError(err error) {
	m.err = err
}

// WizardVisible returns whether the wizard overlay is active.
func (m Model) WizardVisible() bool {
	return m.wizard != nil && m.wizard.visible
}

// WizardView renders the wizard overlay at the given dimensions.
func (m Model) WizardView(width, height int) string {
	if m.wizard == nil {
		return ""
	}
	return m.wizard.View(width, height)
}

// UpdateWizardProgress forwards a progress message to the wizard.
func (m *Model) UpdateWizardProgress(msg InstallProgressMsg) {
	if m.wizard == nil {
		return
	}
	m.wizard.progressMsg = msg.Message
	m.wizard.progressErr = msg.Err
	m.wizard.done = msg.Done
	if msg.Done {
		m.wizard.step = stepDone
	}
}

// UpdateImportDetect forwards discovered pools to the import wizard.
func (m *Model) UpdateImportDetect(pools []operator.SuggestedPool, err error) {
	if m.wizard == nil {
		return
	}
	if err != nil {
		m.wizard.progressErr = err
		m.wizard.step = stepDone
		return
	}
	m.wizard.importPools = pools
	m.wizard.step = stepImportConfirm
}

// UpdateImportResult forwards import completion to the wizard.
func (m *Model) UpdateImportResult(err error) {
	if m.wizard == nil {
		return
	}
	m.wizard.progressErr = err
	m.wizard.done = true
	m.wizard.step = stepDone
	if err == nil {
		m.wizard.progressMsg = "Nodes imported successfully."
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	// If wizard is active, delegate to it.
	if m.WizardVisible() {
		return m.updateWizard(msg)
	}

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	components := m.componentList()

	switch {
	case key.Matches(keyMsg, key.NewBinding(key.WithKeys("j", "down"))):
		if m.selected < len(components)-1 {
			m.selected++
		}
	case key.Matches(keyMsg, key.NewBinding(key.WithKeys("k", "up"))):
		if m.selected > 0 {
			m.selected--
		}
	case key.Matches(keyMsg, key.NewBinding(key.WithKeys("enter"))):
		if m.selected < len(components) {
			comp := components[m.selected]
			if comp.Status == "not_installed" && comp.Installable {
				m.wizard = NewWizard(comp.Name)
				return m, nil
			}
		}
	case key.Matches(keyMsg, key.NewBinding(key.WithKeys("u"))):
		// Update/reinstall selected component (works for running, outdated, degraded).
		if m.selected < len(components) {
			comp := components[m.selected]
			if comp.Status == "running" || comp.Status == "outdated" || comp.Status == "degraded" {
				return m, func() tea.Msg {
					return InstallComponentMsg{
						Component: comp.Name,
						Opts:      map[string]string{"action": "update"},
					}
				}
			}
		}
	case key.Matches(keyMsg, key.NewBinding(key.WithKeys("n"))):
		// Create NodePool — only when operator is installed but no pools exist.
		if m.scaling != nil && m.scaling.Installed && len(m.scaling.Pools) == 0 {
			m.wizard = NewNodePoolWizard()
			return m, nil
		}
	case key.Matches(keyMsg, key.NewBinding(key.WithKeys("i"))):
		// If operator installed, trigger import detection.
		if m.scaling != nil && m.scaling.Installed {
			m.wizard = NewImportWizard()
			return m, func() tea.Msg { return ImportNodesMsg{} }
		}
		// Otherwise: setup all — install all missing components.
		for _, comp := range components {
			if comp.Status == "not_installed" && comp.Installable {
				m.wizard = NewWizard(comp.Name)
				return m, nil
			}
		}
	case key.Matches(keyMsg, key.NewBinding(key.WithKeys("h", "left"))):
		// Navigate pool selection left (up).
		if m.scaling != nil && len(m.scaling.Pools) > 1 && m.selectedPool > 0 {
			m.selectedPool--
		}
	case key.Matches(keyMsg, key.NewBinding(key.WithKeys("l", "right"))):
		// Navigate pool selection right (down).
		if m.scaling != nil && m.selectedPool < len(m.scaling.Pools)-1 {
			m.selectedPool++
		}
	}

	return m, nil
}

func (m Model) updateWizard(msg tea.Msg) (Model, tea.Cmd) {
	wizard, cmd := m.wizard.Update(msg)
	m.wizard = &wizard

	// Check if wizard wants to close.
	if !m.wizard.visible {
		m.wizard = nil
		return m, cmd
	}

	return m, cmd
}

func (m Model) componentList() []ComponentDisplay {
	if m.health == nil {
		return nil
	}
	return m.health.Components
}

func (m Model) View(width, height int) string {
	if m.err != nil {
		return styles.PanelStyle.Width(width - 2).Render(
			styles.CriticalStyle.Render("Error: " + m.err.Error()))
	}

	leftW := (width - 3) / 2
	rightW := width - leftW - 3

	leftPanel := m.viewHealthPanel(leftW, height)
	rightPanel := m.viewScalingPanel(rightW, height)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, " ", rightPanel)
}

// --- Health Panel (Left) ---

func (m Model) viewHealthPanel(panelW, height int) string {
	if m.health == nil {
		content := lipgloss.JoinVertical(lipgloss.Left,
			styles.TitleStyle.Render("Cluster Health"),
			"",
			styles.InfoStyle.Render("Checking components..."),
		)
		return styles.PanelStyle.Width(panelW).Render(content)
	}

	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	bright := lipgloss.NewStyle().Foreground(styles.ColorText)

	// Title with connection status.
	var connBadge string
	if m.health.Connected {
		connBadge = styles.Badge("CONNECTED", styles.ColorHealthy)
	} else {
		connBadge = styles.Badge("OFFLINE", styles.ColorCritical)
	}

	title := styles.TitleStyle.Render("Cluster Health") + "  " + connBadge

	// Version info.
	var versionLines []string
	if m.health.K8sVersion != "" {
		versionLines = append(versionLines, row("Kubernetes", bright.Render(m.health.K8sVersion)))
	}
	if m.health.TalosVersion != "" {
		versionLines = append(versionLines, row("Talos OS", bright.Render(m.health.TalosVersion)))
	}

	// Component list.
	components := m.health.Components

	// Count statuses for summary.
	var running, notInstalled, outdated, degraded int
	for _, c := range components {
		switch c.Status {
		case "running":
			running++
		case "not_installed":
			notInstalled++
		case "outdated":
			outdated++
		case "degraded":
			degraded++
		}
	}

	summaryParts := []string{
		styles.HealthyStyle.Render(fmt.Sprintf("%d healthy", running)),
	}
	if notInstalled > 0 {
		summaryParts = append(summaryParts, dim.Render(fmt.Sprintf("%d missing", notInstalled)))
	}
	if outdated > 0 {
		summaryParts = append(summaryParts, styles.WarningStyle.Render(fmt.Sprintf("%d outdated", outdated)))
	}
	if degraded > 0 {
		summaryParts = append(summaryParts, styles.CriticalStyle.Render(fmt.Sprintf("%d degraded", degraded)))
	}
	summary := strings.Join(summaryParts, "  ")

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	lines = append(lines, versionLines...)
	if len(versionLines) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, styles.SubtitleStyle.Render("Components")+"  "+summary)
	lines = append(lines, "")

	// Render each component row.
	nameW := panelW - 12 // leave room for icon, status, version
	if nameW < 20 {
		nameW = 20
	}

	for i, comp := range components {
		selected := i == m.selected
		compLine := m.renderComponentRow(comp, nameW, selected)
		lines = append(lines, compLine)

		// Show description for selected component.
		if selected && comp.Description != "" {
			desc := dim.Render("  " + comp.Description)
			lines = append(lines, desc)
		}
	}

	lines = append(lines, "")
	lines = append(lines, dim.Render("j/k select  enter install  u update"))

	content := strings.Join(lines, "\n")
	return styles.PanelStyle.Width(panelW).Render(content)
}

func (m Model) renderComponentRow(comp ComponentDisplay, nameW int, selected bool) string {
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	bright := lipgloss.NewStyle().Foreground(styles.ColorText)

	// Status icon.
	var icon string
	switch comp.Status {
	case "running":
		icon = styles.HealthyStyle.Render("●")
	case "outdated":
		icon = styles.WarningStyle.Render("▲")
	case "not_installed":
		icon = dim.Render("○")
	case "degraded":
		icon = styles.CriticalStyle.Render("✕")
	default:
		icon = dim.Render("?")
	}

	// Name.
	name := comp.Name
	if len(name) > nameW-4 {
		name = name[:nameW-6] + ".."
	}
	nameStyle := dim
	if selected {
		nameStyle = bright.Bold(true)
	} else if comp.Status == "running" {
		nameStyle = bright
	}

	// Status text.
	var statusText string
	switch comp.Status {
	case "running":
		statusText = styles.HealthyStyle.Render("Running")
	case "outdated":
		versionStr := comp.Version
		if comp.Latest != "" {
			versionStr = comp.Version + " -> " + comp.Latest
		}
		statusText = styles.WarningStyle.Render("Outdated") + "  " + dim.Render(versionStr)
	case "not_installed":
		statusText = dim.Render("Not installed")
		if comp.Installable {
			statusText += "  " + styles.KeyStyle.Render("[Enter]")
		}
	case "degraded":
		statusText = styles.CriticalStyle.Render("Degraded")
	}

	// Version (only for running components).
	versionStr := ""
	if comp.Status == "running" && comp.Version != "" {
		versionStr = "  " + dim.Render(comp.Version)
	}

	line := fmt.Sprintf("  %s %s  %s%s",
		icon,
		lipgloss.NewStyle().Width(22).Render(nameStyle.Render(name)),
		statusText,
		versionStr,
	)

	if selected {
		line = lipgloss.NewStyle().
			Background(styles.ColorBgActive).
			Render(line)
	}

	return line
}

// --- Scaling Panel (Right) ---

func (m Model) viewScalingPanel(panelW, height int) string {
	// Operator not installed.
	if m.scaling == nil || !m.scaling.Installed {
		return m.viewNotInstalled(panelW, height)
	}

	// No pools configured.
	if len(m.scaling.Pools) == 0 {
		return m.viewNoPool(panelW, height)
	}

	return m.viewMultiPoolDashboard(panelW, height)
}

func (m Model) viewNotInstalled(panelW, height int) string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		styles.TitleStyle.Render("Autoscaling"),
		"",
		lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render("Konductor Operator is not installed in this cluster."),
		"",
		lipgloss.NewStyle().Foreground(styles.ColorText).Render("Select it in the component list and press Enter to install."),
		"",
		lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render("The operator runs in-cluster and manages node scaling automatically."),
	)
	return styles.PanelStyle.Width(panelW).Render(content)
}

func (m Model) viewNoPool(panelW, height int) string {
	content := lipgloss.JoinVertical(lipgloss.Left,
		styles.TitleStyle.Render("Autoscaling"),
		"",
		styles.HealthyStyle.Render("● Operator installed"),
		"",
		lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render("No NodePool configured yet."),
		"",
		lipgloss.NewStyle().Foreground(styles.ColorText).Render("Press "+styles.KeyStyle.Render("i")+" to import existing nodes."),
		lipgloss.NewStyle().Foreground(styles.ColorText).Render("Press "+styles.KeyStyle.Render("n")+" to create a new NodePool."),
	)
	return styles.PanelStyle.Width(panelW).Render(content)
}

func (m Model) viewMultiPoolDashboard(panelW, height int) string {
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	bright := lipgloss.NewStyle().Foreground(styles.ColorText)

	pools := m.scaling.Pools

	// Count total nodes.
	var totalNodes int32
	for _, p := range pools {
		totalNodes += p.CurrentNodes
	}

	title := styles.TitleStyle.Render("Autoscaling")
	poolSummary := dim.Render(fmt.Sprintf("%d pools, %d nodes", len(pools), totalNodes))

	var lines []string
	lines = append(lines, title+"  "+poolSummary)
	lines = append(lines, "")
	lines = append(lines, styles.SubtitleStyle.Render("Pools"))
	lines = append(lines, "")

	// Pool list rows.
	for i, pool := range pools {
		selected := i == m.selectedPool

		// Status icon.
		var icon string
		switch pool.Phase {
		case "Active", "":
			if pool.ReadyNodes == pool.CurrentNodes && pool.CurrentNodes > 0 {
				icon = styles.HealthyStyle.Render("●")
			} else if pool.CurrentNodes == 0 {
				icon = dim.Render("○")
			} else {
				icon = styles.WarningStyle.Render("●")
			}
		case "Scaling":
			icon = styles.WarningStyle.Render("◌")
		case "Degraded":
			icon = styles.CriticalStyle.Render("●")
		default:
			icon = dim.Render("●")
		}

		// Pool name.
		nameStyle := dim
		if selected {
			nameStyle = bright.Bold(true)
		}

		// Ready count.
		readyStr := fmt.Sprintf("%d/%d ready", pool.ReadyNodes, pool.CurrentNodes)

		// Role badge.
		var roleBadge string
		if pool.Role == "control-plane" {
			roleBadge = styles.InfoStyle.Render("CP")
		} else {
			roleBadge = dim.Render(pool.ServerType)
		}

		// Scaling status.
		var scalingStr string
		if pool.ScalingEnabled {
			scalingStr = styles.HealthyStyle.Render("scaling: on")
		} else {
			scalingStr = dim.Render("scaling: off")
		}

		poolLine := fmt.Sprintf("  %s %-20s  %-12s  %-6s  %s",
			icon,
			nameStyle.Render(pool.Name),
			dim.Render(readyStr),
			roleBadge,
			scalingStr,
		)

		if selected {
			poolLine = lipgloss.NewStyle().
				Background(styles.ColorBgActive).
				Render(poolLine)
		}

		lines = append(lines, poolLine)
	}

	lines = append(lines, "")
	lines = append(lines, dim.Render("h/l select pool  n new pool"))

	// Divider.
	lines = append(lines, "")
	lines = append(lines, dim.Render(strings.Repeat("─", panelW-6)))
	lines = append(lines, "")

	// Show details for the selected pool.
	if m.selectedPool < len(pools) {
		poolDetail := m.viewPoolDetail(pools[m.selectedPool], panelW)
		lines = append(lines, poolDetail...)
	}

	content := strings.Join(lines, "\n")
	return styles.PanelStyle.Width(panelW).Render(content)
}

func (m Model) viewPoolDetail(pool k8s.NodePoolInfo, panelW int) []string {
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	bright := lipgloss.NewStyle().Foreground(styles.ColorText)

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

	var lines []string
	lines = append(lines, bright.Bold(true).Render(pool.Name)+"  "+phaseBadge)
	lines = append(lines, "")
	lines = append(lines, row("Provider", bright.Render(pool.Provider+" / "+pool.ServerType)))
	lines = append(lines, row("Location", bright.Render(pool.Location)))
	if pool.Role == "control-plane" {
		lines = append(lines, row("Role", styles.InfoStyle.Render("control-plane")))
	} else {
		lines = append(lines, row("Role", dim.Render("worker")))
	}
	lines = append(lines, "")
	lines = append(lines, row("Current", bright.Render(nodeStr)))
	lines = append(lines, row("Range", bright.Render(fmt.Sprintf("%d - %d", pool.MinNodes, pool.MaxNodes))))
	lines = append(lines, row("Capacity", nodeBar))
	lines = append(lines, row("Last Scale", lastScale))
	lines = append(lines, "")
	lines = append(lines, row("Scale Up", dim.Render(fmt.Sprintf("CPU > %d%% or MEM > %d%% for %ds",
		pool.ScaleUp.CPUPercent, pool.ScaleUp.MemoryPercent, pool.ScaleUp.StabilizationSeconds))))
	lines = append(lines, row("Scale Down", dim.Render(fmt.Sprintf("CPU < %d%% and MEM < %d%% for %ds",
		pool.ScaleDown.CPUPercent, pool.ScaleDown.MemoryPercent, pool.ScaleDown.StabilizationSeconds))))
	lines = append(lines, row("Cooldown", dim.Render(fmt.Sprintf("%ds", pool.CooldownSeconds))))

	// Show claims for this pool.
	poolClaims := m.claimsForPool(pool.Name)
	if len(poolClaims) > 0 {
		lines = append(lines, "")
		claimTitle := styles.SubtitleStyle.Render("Managed Nodes")
		claimCount := dim.Render(fmt.Sprintf("  %d claims", len(poolClaims)))
		lines = append(lines, claimTitle+claimCount)
		lines = append(lines, "")

		for _, claim := range poolClaims {
			var phaseStr string
			switch claim.Phase {
			case "Ready":
				phaseStr = styles.HealthyStyle.Render("● Ready")
			case "Pending":
				phaseStr = dim.Render("● Pending")
			case "Provisioning":
				phaseStr = styles.InfoStyle.Render("● Provisioning")
			case "Failed":
				phaseStr = styles.CriticalStyle.Render("● Failed")
			default:
				phaseStr = dim.Render("● " + claim.Phase)
			}

			name := claim.Name
			if len(name) > 20 {
				name = name[:18] + ".."
			}

			lines = append(lines, fmt.Sprintf("  %s  %s", bright.Render(name), phaseStr))
			if claim.Failure != "" {
				lines = append(lines, "    "+styles.CriticalStyle.Render("-> "+claim.Failure))
			}
		}
	}

	return lines
}

func (m Model) claimsForPool(poolName string) []k8s.NodeClaimInfo {
	if m.scaling == nil {
		return nil
	}
	var result []k8s.NodeClaimInfo
	for _, c := range m.scaling.Claims {
		if c.Pool == poolName {
			result = append(result, c)
		}
	}
	return result
}

// HealthFromCluster converts cluster.ClusterHealth to the TUI display type.
func HealthFromCluster(ch *cluster.ClusterHealth) *ClusterHealthData {
	data := &ClusterHealthData{
		Connected:    ch.Connected,
		K8sVersion:   ch.K8sVersion,
		TalosVersion: ch.TalosVersion,
	}

	for _, comp := range ch.Components {
		display := ComponentDisplay{
			Name:        comp.Name,
			Version:     comp.Version,
			Latest:      comp.LatestVersion,
			Installable: comp.Installable,
			Description: comp.Description,
		}

		switch {
		case !comp.Installed:
			display.Status = "not_installed"
		case comp.Installed && comp.Healthy && !comp.NeedsUpdate:
			display.Status = "running"
		case comp.Installed && comp.Healthy && comp.NeedsUpdate:
			display.Status = "outdated"
		case comp.Installed && !comp.Healthy:
			display.Status = "degraded"
		}

		data.Components = append(data.Components, display)
	}

	return data
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
