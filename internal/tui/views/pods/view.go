package pods

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

// FetchLogsMsg is emitted when the user wants to view logs for a pod.
type FetchLogsMsg struct {
	Namespace string
	PodName   string
	Container string
	TailLines int64
}

type viewMode int

const (
	modeList   viewMode = iota
	modeDetail          // Pod detail + logs
	modeFull            // Fullscreen logs
)

type sortColumn int

const (
	sortName     sortColumn = iota
	sortStatus
	sortRestarts
	sortCPU
	sortMemory
	sortAge
	sortNode
	sortColumnCount // sentinel
)

func (s sortColumn) String() string {
	switch s {
	case sortName:
		return "NAME"
	case sortStatus:
		return "STATUS"
	case sortRestarts:
		return "RESTART"
	case sortCPU:
		return "CPU"
	case sortMemory:
		return "MEMORY"
	case sortAge:
		return "AGE"
	case sortNode:
		return "NODE"
	}
	return ""
}

type Model struct {
	allPods     []k8s.PodInfo
	namespaces  []string
	selectedNS  int
	selectedPod int
	err         error
	sortBy      sortColumn
	sortAsc     bool

	// Detail/log state.
	mode        viewMode
	logPod      *k8s.PodInfo
	logContent  string
	logViewport viewport.Model
	autoScroll  bool
	logErr      error
	logLoading  bool
}

func New() Model {
	vp := viewport.New(80, 20)
	vp.Style = lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	return Model{
		namespaces:  []string{"All"},
		autoScroll:  true,
		logViewport: vp,
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
	filtered := m.filteredPods()
	if m.selectedPod >= len(filtered) {
		m.selectedPod = max(0, len(filtered)-1)
	}

	// Update the logPod reference if we're in detail mode.
	if m.logPod != nil && m.mode != modeList {
		for i := range pods {
			if pods[i].Name == m.logPod.Name && pods[i].Namespace == m.logPod.Namespace {
				m.logPod = &pods[i]
				break
			}
		}
	}
}

func (m *Model) SetError(err error) {
	m.err = err
}

// SelectNamespace switches the namespace filter to the given namespace.
func (m *Model) SelectNamespace(ns string) {
	for i, n := range m.namespaces {
		if n == ns {
			m.selectedNS = i
			m.selectedPod = 0
			return
		}
	}
}

func (m *Model) SetLogs(content string) {
	m.logContent = content
	m.logLoading = false
	m.logErr = nil
	m.logViewport.SetContent(content)
	if m.autoScroll {
		m.logViewport.GotoBottom()
	}
}

func (m *Model) SetLogError(err error) {
	m.logErr = err
	m.logLoading = false
}

// InLogMode returns true if viewing logs (for tick-based refresh).
func (m Model) InLogMode() bool {
	return m.mode == modeDetail || m.mode == modeFull
}

// LogTarget returns the current log target for fetching.
func (m Model) LogTarget() (string, string, string, int64) {
	if m.logPod == nil {
		return "", "", "", 0
	}
	return m.logPod.Namespace, m.logPod.Name, "", 500
}

func (m Model) filteredPods() []k8s.PodInfo {
	var result []k8s.PodInfo
	if m.selectedNS == 0 {
		result = make([]k8s.PodInfo, len(m.allPods))
		copy(result, m.allPods)
	} else {
		ns := m.namespaces[m.selectedNS]
		for _, p := range m.allPods {
			if p.Namespace == ns {
				result = append(result, p)
			}
		}
	}

	// Sort.
	less := func(i, j int) bool {
		a, b := result[i], result[j]
		var cmp int
		switch m.sortBy {
		case sortName:
			cmp = strings.Compare(a.Name, b.Name)
		case sortStatus:
			cmp = strings.Compare(a.Phase, b.Phase)
		case sortRestarts:
			cmp = int(a.Restarts) - int(b.Restarts)
		case sortCPU:
			cmp = a.CPUUsage.Cmp(b.CPUUsage)
		case sortMemory:
			cmp = a.MemUsage.Cmp(b.MemUsage)
		case sortAge:
			if a.Age < b.Age {
				cmp = -1
			} else if a.Age > b.Age {
				cmp = 1
			}
		case sortNode:
			cmp = strings.Compare(a.Node, b.Node)
		default:
			cmp = strings.Compare(a.Name, b.Name)
		}
		if m.sortAsc {
			return cmp < 0
		}
		return cmp > 0
	}

	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if less(j, i) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.mode {
		case modeList:
			return m.updateList(msg)
		case modeDetail:
			return m.updateDetail(msg)
		case modeFull:
			return m.updateFull(msg)
		}
	}

	// Pass through to viewport if in log mode.
	if m.mode == modeDetail || m.mode == modeFull {
		var cmd tea.Cmd
		m.logViewport, cmd = m.logViewport.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m Model) updateList(msg tea.KeyMsg) (Model, tea.Cmd) {
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
	case key.Matches(msg, key.NewBinding(key.WithKeys("s"))):
		// Cycle sort column.
		m.sortBy = (m.sortBy + 1) % sortColumnCount
		m.selectedPod = 0
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("S"))):
		// Toggle sort direction.
		m.sortAsc = !m.sortAsc
		m.selectedPod = 0
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
		if m.selectedPod < len(filtered) && len(filtered) > 0 {
			pod := filtered[m.selectedPod]
			m.logPod = &pod
			m.mode = modeDetail
			m.logLoading = true
			m.logContent = ""
			m.autoScroll = true
			m.logViewport.SetContent("")
			return m, func() tea.Msg {
				return FetchLogsMsg{
					Namespace: pod.Namespace,
					PodName:   pod.Name,
					TailLines: 500,
				}
			}
		}
	}
	return m, nil
}

func (m Model) updateDetail(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
		m.mode = modeList
		m.logPod = nil
		m.logContent = ""
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("f", "F"))):
		m.mode = modeFull
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("l", "L"))):
		m.autoScroll = !m.autoScroll
		if m.autoScroll {
			m.logViewport.GotoBottom()
		}
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("G"))):
		m.logViewport.GotoBottom()
		m.autoScroll = true
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("g"))):
		m.logViewport.GotoTop()
		m.autoScroll = false
		return m, nil
	default:
		var cmd tea.Cmd
		m.logViewport, cmd = m.logViewport.Update(msg)
		if !m.logViewport.AtBottom() {
			m.autoScroll = false
		}
		return m, cmd
	}
}

func (m Model) updateFull(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("esc", "f", "F"))):
		m.mode = modeDetail
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("l", "L"))):
		m.autoScroll = !m.autoScroll
		if m.autoScroll {
			m.logViewport.GotoBottom()
		}
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("G"))):
		m.logViewport.GotoBottom()
		m.autoScroll = true
		return m, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("g"))):
		m.logViewport.GotoTop()
		m.autoScroll = false
		return m, nil
	default:
		var cmd tea.Cmd
		m.logViewport, cmd = m.logViewport.Update(msg)
		if !m.logViewport.AtBottom() {
			m.autoScroll = false
		}
		return m, cmd
	}
}

func (m Model) View(width, height int) string {
	switch m.mode {
	case modeDetail:
		return m.viewDetail(width, height)
	case modeFull:
		return m.viewFull(width, height)
	default:
		return m.viewList(width, height)
	}
}

// --- List View ---

func (m Model) viewList(width, height int) string {
	if m.err != nil {
		return styles.PanelStyle.Width(width - 2).Render(
			styles.CriticalStyle.Render("Error: " + m.err.Error()))
	}
	if m.allPods == nil {
		return styles.PanelStyle.Width(width - 2).Render(
			styles.InfoStyle.Render("Loading pod data..."))
	}

	filtered := m.filteredPods()
	nsLabel := m.namespaces[m.selectedNS]

	title := styles.TitleStyle.Render("Pods")
	nsSelector := fmt.Sprintf("  %s %s %s  %s",
		styles.KeyStyle.Render("◀ h"),
		styles.InfoStyle.Render(nsLabel),
		styles.KeyStyle.Render("l ▶"),
		lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render(fmt.Sprintf("(%d pods)", len(filtered))))

	// Sort indicator.
	arrow := "▼"
	if m.sortAsc {
		arrow = "▲"
	}
	sortInfo := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render(
		fmt.Sprintf("  sort: %s %s  (s cycle, S reverse)", m.sortBy.String(), arrow))

	header := title + nsSelector + sortInfo
	table := m.renderTable(filtered, width-6, height-6)
	content := header + "\n\n" + table
	return styles.PanelStyle.Width(width - 2).Render(content)
}

// --- Detail View (pod info + logs) ---

func (m Model) viewDetail(width, height int) string {
	if m.logPod == nil {
		return styles.PanelStyle.Width(width - 2).Render("No pod selected")
	}

	pod := m.logPod
	innerW := width - 6

	// --- Pod info panel (top quarter) ---
	infoHeight := 6
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	bright := lipgloss.NewStyle().Foreground(styles.ColorText)

	// Title line.
	nameStr := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorText).Render(pod.Name)
	nsStr := dim.Render(pod.Namespace + "/")

	var scrollBadge string
	if m.autoScroll {
		scrollBadge = styles.Badge("LIVE", styles.ColorHealthy)
	} else {
		scrollBadge = styles.Badge("PAUSED", styles.ColorWarning)
	}

	titleLeft := nsStr + nameStr
	titleGap := innerW - lipgloss.Width(titleLeft) - lipgloss.Width(scrollBadge)
	if titleGap < 0 {
		titleGap = 0
	}
	titleLine := titleLeft + lipgloss.NewStyle().Width(titleGap).Render("") + scrollBadge

	// Status badge.
	var statusBadge string
	switch {
	case pod.IsCrashLoop:
		statusBadge = styles.Badge("CRASHLOOP", styles.ColorCritical)
	case pod.Phase == "Failed":
		statusBadge = styles.Badge("FAILED", styles.ColorCritical)
	case pod.IsNotReady:
		statusBadge = styles.Badge("NOT READY", styles.ColorWarning)
	case pod.Phase == "Pending":
		statusBadge = styles.Badge("PENDING", styles.ColorWarning)
	case pod.Phase == "Running":
		statusBadge = styles.Badge("RUNNING", styles.ColorHealthy)
	default:
		statusBadge = styles.Badge(pod.Status, styles.ColorInfo)
	}

	// Info columns.
	col1W := 30
	col2W := 30
	col3W := innerW - col1W - col2W
	if col3W < 10 {
		col3W = 10
	}

	label := func(l, v string) string {
		return dim.Render(l+": ") + bright.Render(v)
	}

	col1 := lipgloss.NewStyle().Width(col1W).Render(
		label("Ready", pod.Ready) + "\n" + label("Restarts", fmt.Sprintf("%d", pod.Restarts)))
	col2 := lipgloss.NewStyle().Width(col2W).Render(
		label("CPU", k8s.FormatResourcePair(pod.CPUUsage, pod.CPULimit, k8s.FormatCPU)) + "\n" +
			label("Memory", k8s.FormatResourcePair(pod.MemUsage, pod.MemLimit, k8s.FormatMemory)))
	col3 := lipgloss.NewStyle().Width(col3W).Render(
		label("Node", truncate(pod.Node, col3W-8)) + "\n" + label("Age", formatAge(pod.Age)))

	infoRow := lipgloss.JoinHorizontal(lipgloss.Top, col1, col2, col3)

	_ = infoHeight
	infoPanel := lipgloss.JoinVertical(lipgloss.Left,
		titleLine,
		"",
		statusBadge,
		"",
		infoRow,
	)

	// --- Separator ---
	sep := lipgloss.NewStyle().Foreground(styles.ColorBorder).Render(strings.Repeat("─", innerW))

	// --- Logs panel (remaining space) ---
	logsPanelHeight := height - lipgloss.Height(infoPanel) - 5 // sep + padding + footer
	if logsPanelHeight < 3 {
		logsPanelHeight = 3
	}

	m.logViewport.Width = innerW
	m.logViewport.Height = logsPanelHeight

	var logsContent string
	if m.logLoading {
		logsContent = styles.InfoStyle.Render("Loading logs...")
	} else if m.logErr != nil {
		logsContent = styles.CriticalStyle.Render("Error: " + m.logErr.Error())
	} else if m.logContent == "" {
		logsContent = dim.Render("No logs available")
	} else {
		logsContent = m.logViewport.View()
	}

	// Footer hints.
	hints := dim.Render("esc back  ·  l live/pause  ·  f fullscreen  ·  g/G top/bottom  ·  ↑↓ scroll")

	return styles.PanelStyle.Width(width - 2).Padding(0, 2).Render(
		infoPanel + "\n" + sep + "\n" + logsContent + "\n" + hints)
}

// --- Fullscreen logs ---

func (m Model) viewFull(width, height int) string {
	if m.logPod == nil {
		return ""
	}

	innerW := width - 6
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)

	// Title.
	nsStr := dim.Render(m.logPod.Namespace + "/")
	nameStr := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorText).Render(m.logPod.Name)
	var scrollBadge string
	if m.autoScroll {
		scrollBadge = styles.Badge("LIVE", styles.ColorHealthy)
	} else {
		scrollBadge = styles.Badge("PAUSED", styles.ColorWarning)
	}
	titleLeft := nsStr + nameStr
	titleGap := innerW - lipgloss.Width(titleLeft) - lipgloss.Width(scrollBadge)
	if titleGap < 0 {
		titleGap = 0
	}
	titleLine := titleLeft + lipgloss.NewStyle().Width(titleGap).Render("") + scrollBadge

	sep := lipgloss.NewStyle().Foreground(styles.ColorBorder).Render(strings.Repeat("─", innerW))

	vpHeight := height - 5
	if vpHeight < 3 {
		vpHeight = 3
	}
	m.logViewport.Width = innerW
	m.logViewport.Height = vpHeight

	var logsContent string
	if m.logLoading {
		logsContent = styles.InfoStyle.Render("Loading logs...")
	} else if m.logErr != nil {
		logsContent = styles.CriticalStyle.Render("Error: " + m.logErr.Error())
	} else if m.logContent == "" {
		logsContent = dim.Render("No logs available")
	} else {
		logsContent = m.logViewport.View()
	}

	hints := dim.Render("esc/f back  ·  l live/pause  ·  g/G top/bottom  ·  ↑↓ scroll")

	return styles.PanelStyle.Width(width - 2).Padding(0, 2).Render(
		titleLine + "\n" + sep + "\n" + logsContent + "\n" + hints)
}

// --- Table rendering (unchanged) ---

func (m Model) renderTable(pods []k8s.PodInfo, width, maxRows int) string {
	if len(pods) == 0 {
		return lipgloss.NewStyle().Foreground(styles.ColorTextDim).Render("  No pods in this namespace")
	}

	showNS := m.selectedNS == 0

	nameW := 32
	nsW := 16
	readyW := 7
	statusW := 16
	restartW := 9
	cpuW := 14
	memW := 14
	ageW := 6
	if !showNS {
		nsW = 0
		nameW = 38
	}
	nodeW := width - nameW - nsW - readyW - statusW - restartW - cpuW - memW - ageW
	if nodeW < 6 {
		nodeW = 6
	}

	accent := lipgloss.NewStyle().Foreground(styles.ColorTextAccent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	bright := lipgloss.NewStyle().Foreground(styles.ColorText)

	var headerParts string
	headerParts += lipgloss.NewStyle().Width(nameW).Render(accent.Render("NAME"))
	if showNS {
		headerParts += lipgloss.NewStyle().Width(nsW).Render(accent.Render("NAMESPACE"))
	}
	headerParts += lipgloss.NewStyle().Width(readyW).Render(accent.Render("READY"))
	headerParts += lipgloss.NewStyle().Width(statusW).Render(accent.Render("STATUS"))
	headerParts += lipgloss.NewStyle().Width(restartW).Render(accent.Render("RESTART"))
	headerParts += lipgloss.NewStyle().Width(cpuW).Render(accent.Render("CPU"))
	headerParts += lipgloss.NewStyle().Width(memW).Render(accent.Render("MEMORY"))
	headerParts += lipgloss.NewStyle().Width(ageW).Render(accent.Render("AGE"))
	headerParts += lipgloss.NewStyle().Width(nodeW).Render(accent.Render("NODE"))

	var lines []string
	lines = append(lines, headerParts)

	visibleRows := maxRows - 2
	if visibleRows < 5 {
		visibleRows = 5
	}
	startIdx := 0
	if m.selectedPod >= startIdx+visibleRows {
		startIdx = m.selectedPod - visibleRows + 1
	}
	endIdx := startIdx + visibleRows
	if endIdx > len(pods) {
		endIdx = len(pods)
		startIdx = max(0, endIdx-visibleRows)
	}

	for i := startIdx; i < endIdx; i++ {
		pod := pods[i]
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
		case pod.IsNotReady:
			statusStr = styles.WarningStyle.Render("● NotReady")
		case pod.Phase == "Pending":
			statusStr = styles.WarningStyle.Render("● Pending")
		case pod.Phase == "Running":
			statusStr = styles.HealthyStyle.Render("● Running")
		case pod.Phase == "Succeeded":
			statusStr = dim.Render("● Done")
		default:
			statusStr = dim.Render("● " + pod.Status)
		}

		var restartStr string
		if pod.Restarts > 3 {
			restartStr = styles.WarningStyle.Render(fmt.Sprintf("%d ⚠", pod.Restarts))
		} else {
			restartStr = dim.Render(fmt.Sprintf("%d", pod.Restarts))
		}

		cpuStr := k8s.FormatResourcePair(pod.CPUUsage, pod.CPULimit, k8s.FormatCPU)
		if pod.IsThrottled {
			cpuStr = styles.WarningStyle.Render(cpuStr)
		} else {
			cpuStr = dim.Render(cpuStr)
		}

		memStr := k8s.FormatResourcePair(pod.MemUsage, pod.MemLimit, k8s.FormatMemory)
		if pod.IsOOMRisk {
			memStr = styles.CriticalStyle.Render(memStr)
		} else {
			memStr = dim.Render(memStr)
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
		row += lipgloss.NewStyle().Width(readyW).Render(dim.Render(pod.Ready))
		row += lipgloss.NewStyle().Width(statusW).Render(statusStr)
		row += lipgloss.NewStyle().Width(restartW).Render(restartStr)
		row += lipgloss.NewStyle().Width(cpuW).Render(cpuStr)
		row += lipgloss.NewStyle().Width(memW).Render(memStr)
		row += lipgloss.NewStyle().Width(ageW).Render(dim.Render(formatAge(pod.Age)))
		row += lipgloss.NewStyle().Width(nodeW).Render(dim.Render(truncate(pod.Node, nodeW-1)))

		if selected {
			row = lipgloss.NewStyle().Background(styles.ColorBgActive).Width(width).Render(row)
		}

		lines = append(lines, row)
	}

	if len(pods) > visibleRows {
		lines = append(lines, dim.Render(fmt.Sprintf("  %d/%d pods (↑↓ scroll, enter logs)", min(m.selectedPod+1, len(pods)), len(pods))))
	}

	return strings.Join(lines, "\n")
}

func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		if maxLen > 3 {
			return s[:maxLen-2] + ".."
		}
		return s[:maxLen]
	}
	return s
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
