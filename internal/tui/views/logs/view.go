package logs

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/night-OS-GmbH/konductor/internal/k8s"
	"github.com/night-OS-GmbH/konductor/internal/tui/styles"
)

// PodSelectedMsg is emitted when the user selects a pod to view logs.
type PodSelectedMsg struct {
	Namespace string
	PodName   string
}

type viewMode int

const (
	modePodSelect viewMode = iota
	modeLogView
)

type Model struct {
	allPods    []k8s.PodInfo
	namespaces []string
	selectedNS int
	selectedPod int

	// Log viewing state.
	mode       viewMode
	podName    string
	namespace  string
	container  string
	logContent string
	viewport   viewport.Model
	autoScroll bool
	tailLines  int64
	loading    bool
	err        error

	width  int
	height int
}

func New() Model {
	return Model{
		namespaces: []string{"All"},
		autoScroll: true,
		tailLines:  500,
	}
}

func (m *Model) SetData(pods []k8s.PodInfo) {
	m.allPods = pods
	m.err = nil

	nsSet := make(map[string]bool)
	for _, p := range pods {
		nsSet[p.Namespace] = true
	}
	sorted := make([]string, 0, len(nsSet))
	for ns := range nsSet {
		sorted = append(sorted, ns)
	}
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	m.namespaces = append([]string{"All"}, sorted...)
	if m.selectedNS >= len(m.namespaces) {
		m.selectedNS = 0
	}
}

func (m *Model) SetLogs(content string) {
	m.logContent = content
	m.loading = false
	m.err = nil
	m.viewport.SetContent(content)
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m *Model) SetError(err error) {
	m.err = err
	m.loading = false
}

// SelectPod pre-selects a pod for log viewing (e.g., from Pods tab).
func (m *Model) SelectPod(namespace, podName string) {
	m.namespace = namespace
	m.podName = podName
	m.mode = modeLogView
	m.loading = true
	m.logContent = ""
	m.autoScroll = true
}

// Mode returns the current view mode.
func (m Model) Mode() viewMode { return m.mode }

// LogTarget returns the current pod being viewed.
func (m Model) LogTarget() (string, string, string, int64) {
	return m.namespace, m.podName, m.container, m.tailLines
}

func (m Model) filteredPods() []k8s.PodInfo {
	if m.selectedNS == 0 {
		return m.allPods
	}
	ns := m.namespaces[m.selectedNS]
	var filtered []k8s.PodInfo
	for _, p := range m.allPods {
		if p.Namespace == ns {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - 8
		return m, nil

	case tea.KeyMsg:
		if m.mode == modeLogView {
			return m.updateLogView(msg)
		}
		return m.updatePodSelect(msg)
	}

	// Pass through to viewport in log mode.
	if m.mode == modeLogView {
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) updatePodSelect(msg tea.KeyMsg) (Model, tea.Cmd) {
	filtered := m.filteredPods()
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
		if m.selectedPod < len(filtered)-1 {
			m.selectedPod++
		}
	case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
		if m.selectedPod > 0 {
			m.selectedPod--
		}
	case key.Matches(msg, key.NewBinding(key.WithKeys("l", "right"))):
		if m.selectedNS < len(m.namespaces)-1 {
			m.selectedNS++
			m.selectedPod = 0
		}
	case key.Matches(msg, key.NewBinding(key.WithKeys("h", "left"))):
		if m.selectedNS > 0 {
			m.selectedNS--
			m.selectedPod = 0
		}
	case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
		if m.selectedPod < len(filtered) && len(filtered) > 0 {
			pod := filtered[m.selectedPod]
			m.namespace = pod.Namespace
			m.podName = pod.Name
			m.mode = modeLogView
			m.loading = true
			m.logContent = ""
			m.autoScroll = true
			return m, func() tea.Msg {
				return PodSelectedMsg{Namespace: pod.Namespace, PodName: pod.Name}
			}
		}
	}
	return m, nil
}

func (m Model) updateLogView(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
		m.mode = modePodSelect
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("f"))):
		m.autoScroll = !m.autoScroll
		if m.autoScroll {
			m.viewport.GotoBottom()
		}
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("G"))):
		m.viewport.GotoBottom()
		m.autoScroll = true
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("g"))):
		m.viewport.GotoTop()
		m.autoScroll = false
		return m, nil
	default:
		// Let viewport handle scrolling.
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		// If user scrolled up, disable auto-scroll.
		if m.viewport.AtBottom() {
			m.autoScroll = true
		} else {
			m.autoScroll = false
		}
		return m, cmd
	}
}

func (m Model) View(width, height int) string {
	// Update viewport dimensions.
	if m.viewport.Width != width-4 || m.viewport.Height != height-8 {
		m.viewport.Width = width - 4
		m.viewport.Height = height - 8
	}

	if m.mode == modeLogView {
		return m.viewLogs(width, height)
	}
	return m.viewPodSelect(width, height)
}

func (m Model) viewPodSelect(width, height int) string {
	if m.allPods == nil {
		return styles.PanelStyle.Width(width - 2).Render(
			styles.InfoStyle.Render("Loading pod data..."))
	}

	filtered := m.filteredPods()
	nsLabel := m.namespaces[m.selectedNS]

	title := styles.TitleStyle.Render("Logs")
	nsSelector := fmt.Sprintf("  %s %s %s  %s",
		styles.KeyStyle.Render("◀ h"),
		styles.InfoStyle.Render(nsLabel),
		styles.KeyStyle.Render("l ▶"),
		lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render(fmt.Sprintf("(%d pods)", len(filtered))))
	header := title + nsSelector

	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	bright := lipgloss.NewStyle().Foreground(styles.ColorText)
	accent := lipgloss.NewStyle().Foreground(styles.ColorTextAccent).Bold(true)

	nameW := 40
	nsW := 20
	statusW := 16
	ageW := 8
	showNS := m.selectedNS == 0
	if !showNS {
		nsW = 0
		nameW = 50
	}

	var headerLine string
	headerLine += lipgloss.NewStyle().Width(nameW).Render(accent.Render("NAME"))
	if showNS {
		headerLine += lipgloss.NewStyle().Width(nsW).Render(accent.Render("NAMESPACE"))
	}
	headerLine += lipgloss.NewStyle().Width(statusW).Render(accent.Render("STATUS"))
	headerLine += lipgloss.NewStyle().Width(ageW).Render(accent.Render("AGE"))

	var lines []string
	lines = append(lines, headerLine)

	visibleRows := height - 8
	if visibleRows < 5 {
		visibleRows = 5
	}
	startIdx := 0
	if m.selectedPod >= startIdx+visibleRows {
		startIdx = m.selectedPod - visibleRows + 1
	}
	endIdx := startIdx + visibleRows
	if endIdx > len(filtered) {
		endIdx = len(filtered)
		startIdx = max(0, endIdx-visibleRows)
	}

	for i := startIdx; i < endIdx; i++ {
		pod := filtered[i]
		selected := i == m.selectedPod

		name := pod.Name
		if len(name) > nameW-2 {
			name = name[:nameW-4] + ".."
		}

		var statusStr string
		switch {
		case pod.IsCrashLoop:
			statusStr = styles.CriticalStyle.Render("● CrashLoop")
		case pod.Phase == "Failed":
			statusStr = styles.CriticalStyle.Render("● Failed")
		case pod.Phase == "Pending":
			statusStr = styles.WarningStyle.Render("● Pending")
		case pod.Phase == "Running":
			statusStr = styles.HealthyStyle.Render("● Running")
		default:
			statusStr = dim.Render("● " + pod.Status)
		}

		var row string
		if selected {
			row += lipgloss.NewStyle().Width(nameW).Render(bright.Bold(true).Render(name))
		} else {
			row += lipgloss.NewStyle().Width(nameW).Render(bright.Render(name))
		}
		if showNS {
			ns := pod.Namespace
			if len(ns) > nsW-2 {
				ns = ns[:nsW-4] + ".."
			}
			row += lipgloss.NewStyle().Width(nsW).Render(dim.Render(ns))
		}
		row += lipgloss.NewStyle().Width(statusW).Render(statusStr)
		row += lipgloss.NewStyle().Width(ageW).Render(dim.Render(formatAge(pod.Age)))

		if selected {
			row = lipgloss.NewStyle().Background(styles.ColorBgActive).Width(width - 6).Render(row)
		}

		lines = append(lines, row)
	}

	if len(filtered) > visibleRows {
		lines = append(lines, dim.Render(fmt.Sprintf("  %d/%d pods", min(m.selectedPod+1, len(filtered)), len(filtered))))
	}

	table := strings.Join(lines, "\n")
	content := header + "\n\n" + table
	return styles.PanelStyle.Width(width - 2).Render(content)
}

func (m Model) viewLogs(width, height int) string {
	// Header: pod name + auto-scroll indicator.
	podLabel := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorText).Render(m.podName)
	nsLabel := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render(m.namespace + "/")

	var scrollBadge string
	if m.autoScroll {
		scrollBadge = styles.Badge("LIVE", styles.ColorHealthy)
	} else {
		scrollBadge = styles.Badge("PAUSED", styles.ColorWarning)
	}

	headerLeft := nsLabel + podLabel
	headerGap := width - 4 - lipgloss.Width(headerLeft) - lipgloss.Width(scrollBadge)
	if headerGap < 0 {
		headerGap = 0
	}
	logHeader := headerLeft + lipgloss.NewStyle().Width(headerGap).Render("") + scrollBadge

	if m.loading {
		content := logHeader + "\n\n" + styles.InfoStyle.Render("Loading logs...")
		return styles.PanelStyle.Width(width - 2).Render(content)
	}

	if m.err != nil {
		content := logHeader + "\n\n" + styles.CriticalStyle.Render("Error: "+m.err.Error())
		return styles.PanelStyle.Width(width - 2).Render(content)
	}

	if m.logContent == "" {
		content := logHeader + "\n\n" + lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render("No logs available")
		return styles.PanelStyle.Width(width - 2).Render(content)
	}

	// Separator.
	sep := lipgloss.NewStyle().Foreground(styles.ColorBorder).Render(strings.Repeat("─", width-6))

	// Footer hints.
	hints := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render(
		"esc back  ·  f auto-scroll  ·  g/G top/bottom  ·  ↑↓ scroll")

	// Recalculate viewport size.
	vpHeight := height - 7
	if vpHeight < 3 {
		vpHeight = 3
	}
	m.viewport.Width = width - 6
	m.viewport.Height = vpHeight

	return styles.PanelStyle.Width(width - 2).Padding(0, 2).Render(
		logHeader + "\n" + sep + "\n" + m.viewport.View() + "\n" + hints)
}

func formatAge(d time.Duration) string {
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
