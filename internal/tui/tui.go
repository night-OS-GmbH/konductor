package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/night-OS-GmbH/konductor/internal/config"
	"github.com/night-OS-GmbH/konductor/internal/k8s"
	"github.com/night-OS-GmbH/konductor/internal/tui/styles"
	"github.com/night-OS-GmbH/konductor/internal/tui/views/ctxswitcher"
	"github.com/night-OS-GmbH/konductor/internal/tui/views/dashboard"
	"github.com/night-OS-GmbH/konductor/internal/tui/views/namespaces"
	"github.com/night-OS-GmbH/konductor/internal/tui/views/nodes"
	"github.com/night-OS-GmbH/konductor/internal/tui/views/pods"
	"github.com/night-OS-GmbH/konductor/internal/tui/views/scaling"
	"github.com/night-OS-GmbH/konductor/pkg/version"
)

type tab int

const (
	tabDashboard tab = iota
	tabNodes
	tabNamespaces
	tabPods
	tabScaling
)

var tabList = []struct {
	name string
	t    tab
}{
	{"Dashboard", tabDashboard},
	{"Nodes", tabNodes},
	{"Namespaces", tabNamespaces},
	{"Pods", tabPods},
	{"Scaling", tabScaling},
}

type keyMap struct {
	Tab      key.Binding
	ShiftTab key.Binding
	Quit     key.Binding
	Refresh  key.Binding
	Context  key.Binding
	Number1  key.Binding
	Number2  key.Binding
	Number3  key.Binding
	Number4  key.Binding
	Number5  key.Binding
}

var keys = keyMap{
	Tab:      key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "next tab")),
	ShiftTab: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "prev tab")),
	Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	Refresh:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	Context:  key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "context")),
	Number1:  key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "dashboard")),
	Number2:  key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "nodes")),
	Number3:  key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "namespaces")),
	Number4:  key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "pods")),
	Number5:  key.NewBinding(key.WithKeys("5"), key.WithHelp("5", "scaling")),
}

type model struct {
	cfg            *config.Config
	client         *k8s.Client
	activeTab      tab
	width          int
	height         int
	connected      bool
	connErr        error
	dashboardView  dashboard.Model
	nodesView      nodes.Model
	namespacesView namespaces.Model
	podsView       pods.Model
	scalingView    scaling.Model
	ctxSwitcher    ctxswitcher.Model
}

func newModel(cfg *config.Config, client *k8s.Client) model {
	var contexts []string
	var activeCtx string
	if client != nil {
		contexts = client.AvailableContexts()
		activeCtx = client.ActiveContext()
	}

	return model{
		cfg:            cfg,
		client:         client,
		activeTab:      tabDashboard,
		connected:      client != nil,
		dashboardView:  dashboard.New(),
		nodesView:      nodes.New(cfg),
		namespacesView: namespaces.New(),
		podsView:       pods.New(),
		scalingView:    scaling.New(cfg),
		ctxSwitcher:    ctxswitcher.New(contexts, activeCtx),
	}
}

func (m model) Init() tea.Cmd {
	if m.client != nil {
		return tea.Batch(
			m.fetchAllData(),
			m.fetchScalingData(),
			m.scheduleTick(),
		)
	}
	return nil
}

func (m model) fetchAllData() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		nodeList, err := m.client.GetNodes(ctx)
		if err != nil {
			return allDataMsg{err: err}
		}

		podsList, err := m.client.GetPods(ctx)
		if err != nil {
			return allDataMsg{nodes: nodeList, err: err}
		}

		nsData := k8s.AggregateNamespaces(podsList)
		alerts := k8s.GenerateAlerts(nodeList, podsList)

		var watchNS []string
		if m.cfg != nil && len(m.cfg.Clusters) > 0 {
			watchNS = m.cfg.Clusters[0].Dashboard.WatchNamespaces
		}
		deployments, _ := m.client.GetDeployments(ctx, watchNS)

		ver, _ := m.client.ServerVersion()
		hasMetrics := m.client.HasMetrics(ctx)

		return allDataMsg{
			nodes:       nodeList,
			pods:        podsList,
			namespaces:  nsData,
			deployments: deployments,
			alerts:      alerts,
			k8sVersion:  ver,
			hasMetrics:  hasMetrics,
		}
	}
}

func (m model) fetchLogs(namespace, podName, container string, tailLines int64) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		content, err := m.client.GetPodLogs(ctx, k8s.PodLogOptions{
			Namespace: namespace,
			PodName:   podName,
			Container: container,
			TailLines: tailLines,
		})
		if err != nil {
			return logsDataMsg{podName: podName, namespace: namespace, err: err}
		}
		return logsDataMsg{podName: podName, namespace: namespace, content: content}
	}
}

func (m model) fetchScalingData() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		info, err := m.client.GetScalingInfo(ctx)
		return scalingDataMsg{info: info, err: err}
	}
}

func (m model) scheduleTick() tea.Cmd {
	return tea.Tick(5*time.Second, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case allDataMsg:
		if msg.err != nil {
			m.dashboardView.SetError(msg.err)
			if msg.nodes != nil {
				m.nodesView.SetData(msg.nodes)
			} else {
				m.nodesView.SetError(msg.err)
			}
			m.podsView.SetError(msg.err)
			m.namespacesView.SetError(msg.err)
		} else {
			ctxName := ""
			if m.client != nil {
				ctxName = m.client.ActiveContext()
			}
			m.dashboardView.SetData(msg.nodes, msg.pods, msg.namespaces, msg.deployments, msg.alerts, ctxName, msg.k8sVersion, msg.hasMetrics)
			m.nodesView.SetData(msg.nodes)
			m.namespacesView.SetData(msg.namespaces, msg.nodes)
			m.podsView.SetData(msg.pods)
		}
		return m, nil

	case contextSwitchedMsg:
		if msg.err != nil {
			m.connErr = msg.err
			return m, nil
		}
		m.client = msg.client
		m.connected = true
		m.connErr = nil
		m.ctxSwitcher.SetContexts(msg.client.AvailableContexts(), msg.client.ActiveContext())
		return m, m.fetchAllData()

	case ctxswitcher.ContextSelectedMsg:
		kubeconfigPath := ""
		if m.client != nil {
			kubeconfigPath = m.client.KubeconfigPath()
		}
		return m, func() tea.Msg {
			client, err := k8s.Connect(k8s.ConnectOptions{
				Kubeconfig: kubeconfigPath,
				Context:    msg.Context,
			})
			return contextSwitchedMsg{client: client, err: err}
		}

	case logsDataMsg:
		if msg.err != nil {
			m.podsView.SetLogError(msg.err)
		} else {
			m.podsView.SetLogs(msg.content)
		}
		return m, nil

	case scalingDataMsg:
		if msg.err != nil {
			m.scalingView.SetError(msg.err)
		} else {
			m.scalingView.SetScalingData(msg.info)
		}
		return m, nil

	case pods.FetchLogsMsg:
		return m, m.fetchLogs(msg.Namespace, msg.PodName, msg.Container, msg.TailLines)

	case namespaces.NamespaceSelectedMsg:
		m.podsView.SelectNamespace(msg.Namespace)
		m.activeTab = tabPods
		return m, nil

	case tickMsg:
		var cmds []tea.Cmd
		cmds = append(cmds, m.fetchAllData(), m.fetchScalingData(), m.scheduleTick())
		// Auto-refresh logs if viewing pod logs.
		if m.activeTab == tabPods && m.podsView.InLogMode() && m.client != nil {
			ns, pod, container, tailLines := m.podsView.LogTarget()
			if pod != "" {
				cmds = append(cmds, m.fetchLogs(ns, pod, container, tailLines))
			}
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		// Context switcher overlay takes priority.
		if m.ctxSwitcher.Visible() {
			var cmd tea.Cmd
			m.ctxSwitcher, cmd = m.ctxSwitcher.Update(msg)
			return m, cmd
		}

		switch {
		case key.Matches(msg, keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, keys.Context):
			m.ctxSwitcher.Show()
			return m, nil
		case key.Matches(msg, keys.Tab):
			m.activeTab = tab((int(m.activeTab) + 1) % len(tabList))
			return m, nil
		case key.Matches(msg, keys.ShiftTab):
			m.activeTab = tab((int(m.activeTab) - 1 + len(tabList)) % len(tabList))
			return m, nil
		case key.Matches(msg, keys.Number1):
			m.activeTab = tabDashboard
			return m, nil
		case key.Matches(msg, keys.Number2):
			m.activeTab = tabNodes
			return m, nil
		case key.Matches(msg, keys.Number3):
			m.activeTab = tabNamespaces
			return m, nil
		case key.Matches(msg, keys.Number4):
			m.activeTab = tabPods
			return m, nil
		case key.Matches(msg, keys.Number5):
			m.activeTab = tabScaling
			return m, nil
		case key.Matches(msg, keys.Refresh):
			return m, m.fetchAllData()
		}
	}

	// Delegate to active tab.
	var cmd tea.Cmd
	switch m.activeTab {
	case tabNodes:
		m.nodesView, cmd = m.nodesView.Update(msg)
	case tabNamespaces:
		m.namespacesView, cmd = m.namespacesView.Update(msg)
	case tabPods:
		m.podsView, cmd = m.podsView.Update(msg)
	case tabScaling:
		m.scalingView, cmd = m.scalingView.Update(msg)
	}

	return m, cmd
}

func (m model) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	contentWidth := m.width
	contentHeight := m.height - 5

	header := m.renderHeader(contentWidth)
	tabBar := m.renderTabBar(contentWidth)

	var content string
	switch m.activeTab {
	case tabDashboard:
		content = m.dashboardView.View(contentWidth, contentHeight)
	case tabNodes:
		content = m.nodesView.View(contentWidth, contentHeight)
	case tabNamespaces:
		content = m.namespacesView.View(contentWidth, contentHeight)
	case tabPods:
		content = m.podsView.View(contentWidth, contentHeight)
	case tabScaling:
		content = m.scalingView.View(contentWidth, contentHeight)
	}

	footer := m.renderFooter()

	page := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		tabBar,
		content,
		footer,
	)

	// Overlay context switcher if visible.
	if m.ctxSwitcher.Visible() {
		overlay := m.ctxSwitcher.View(m.width, m.height)
		if overlay != "" {
			return overlay
		}
	}

	return page
}

func (m model) renderHeader(width int) string {
	logo := lipgloss.NewStyle().
		Foreground(styles.ColorPrimary).
		Bold(true).
		Render("⚡ KONDUCTOR")

	ver := lipgloss.NewStyle().
		Foreground(styles.ColorBorder).
		Render(" v" + version.Version)

	left := logo + ver

	if m.client != nil {
		sep := lipgloss.NewStyle().
			Foreground(styles.ColorTextDim).
			Render("  │  ")
		ctxName := lipgloss.NewStyle().
			Foreground(styles.ColorText).
			Render(m.client.ActiveContext())
		left += sep + ctxName
	}

	var right string
	if m.connected {
		right = styles.Badge("LIVE", styles.ColorHealthy)
	} else {
		right = styles.Badge("OFFLINE", styles.ColorCritical)
	}

	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 0 {
		gap = 0
	}

	return left + lipgloss.NewStyle().Width(gap).Render("") + right
}

func (m model) renderTabBar(width int) string {
	var renderedTabs []string
	for _, t := range tabList {
		if t.t == m.activeTab {
			renderedTabs = append(renderedTabs, styles.ActiveTabStyle.Render(t.name))
		} else {
			renderedTabs = append(renderedTabs, styles.InactiveTabStyle.Render(t.name))
		}
	}

	row := lipgloss.JoinHorizontal(lipgloss.Top, renderedTabs...)

	rowWidth := lipgloss.Width(row)
	gapW := width - rowWidth
	if gapW > 0 {
		gap := styles.TabGapStyle.Render(repeat(" ", gapW))
		row = lipgloss.JoinHorizontal(lipgloss.Top, row, gap)
	}

	return row
}

func (m model) renderFooter() string {
	helpItems := []struct{ key, desc string }{
		{"1-5", "tabs"},
		{"c", "context"},
		{"r", "refresh"},
		{"q", "quit"},
	}

	switch m.activeTab {
	case tabNodes:
		helpItems = append([]struct{ key, desc string }{{"↑↓", "select"}}, helpItems...)
	case tabNamespaces:
		helpItems = append([]struct{ key, desc string }{{"↑↓", "select"}, {"enter", "→ pods"}}, helpItems...)
	case tabPods:
		if m.podsView.InLogMode() {
			helpItems = append([]struct{ key, desc string }{{"↑↓", "scroll"}, {"esc", "back"}, {"l", "live"}, {"f", "full"}}, helpItems...)
		} else {
			helpItems = append([]struct{ key, desc string }{{"↑↓", "select"}, {"←→", "ns"}, {"s", "sort"}, {"enter", "logs"}}, helpItems...)
		}
	}

	var parts []string
	for _, item := range helpItems {
		k := styles.KeyStyle.Render(item.key)
		d := styles.KeyDescStyle.Render(item.desc)
		parts = append(parts, fmt.Sprintf("%s %s", k, d))
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, joinWithSep(parts, "  │  ")...)
}

func joinWithSep(items []string, sep string) []string {
	if len(items) == 0 {
		return nil
	}
	result := []string{items[0]}
	for _, item := range items[1:] {
		result = append(result, styles.KeyDescStyle.Render("  │  "), item)
	}
	return result
}

func repeat(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}

func Run(cfg *config.Config) error {
	kubeconfig := ""
	k8sContext := ""
	if len(cfg.Clusters) > 0 {
		kubeconfig = cfg.Clusters[0].Kubeconfig
	}

	client, err := k8s.Connect(k8s.ConnectOptions{
		Kubeconfig: kubeconfig,
		Context:    k8sContext,
	})
	if err != nil {
		fmt.Printf("Warning: Could not connect to cluster: %v\n", err)
		fmt.Println("Starting in offline mode. Press 'r' to retry.")
	}

	p := tea.NewProgram(
		newModel(cfg, client),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)

	_, err = p.Run()
	return err
}
