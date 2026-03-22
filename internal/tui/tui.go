package tui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/night-OS-GmbH/konductor/internal/cluster"
	"github.com/night-OS-GmbH/konductor/internal/cluster/components"
	"github.com/night-OS-GmbH/konductor/internal/config"
	"github.com/night-OS-GmbH/konductor/internal/hetzner"
	"github.com/night-OS-GmbH/konductor/internal/installer"
	"github.com/night-OS-GmbH/konductor/internal/k8s"
	"github.com/night-OS-GmbH/konductor/internal/operator"
	"github.com/night-OS-GmbH/konductor/internal/tui/styles"
	"github.com/night-OS-GmbH/konductor/internal/tui/views/ctxswitcher"
	"github.com/night-OS-GmbH/konductor/internal/tui/views/dashboard"
	"github.com/night-OS-GmbH/konductor/internal/tui/views/namespaces"
	"github.com/night-OS-GmbH/konductor/internal/tui/views/nodes"
	"github.com/night-OS-GmbH/konductor/internal/tui/views/pods"
	"github.com/night-OS-GmbH/konductor/internal/tui/views/scaling"
	"github.com/night-OS-GmbH/konductor/pkg/version"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
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
	{"Cluster", tabScaling},
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
	Number5:  key.NewBinding(key.WithKeys("5"), key.WithHelp("5", "cluster")),
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

	// Hetzner provider for image management (lazy-initialized).
	hetznerProvider *hetzner.HetznerProvider

	// pendingPoolMsg stores a pool creation request while waiting for image check/creation.
	pendingPoolMsg *scaling.CreateNodePoolMsg
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
			m.fetchClusterHealth(),
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

func (m model) fetchClusterHealth() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		clientset := m.client.Clientset()
		checker := cluster.NewChecker(clientset)

		// Build a dynamic client for component checkers that need it.
		dynClient, dynErr := buildDynamicClient(m.client.KubeconfigPath(), m.client.ActiveContext())

		// Register all known components.
		checker.AddComponent(components.NewMetricsServer(clientset, dynClient))

		if dynErr == nil {
			checker.AddComponent(components.NewHetznerCCM(clientset, dynClient))
		}

		// Register operator component via installer.
		inst, instErr := installer.NewInstaller(m.client.KubeconfigPath(), m.client.ActiveContext())
		if instErr == nil {
			checker.AddComponent(components.NewKonductorOperator(inst, ""))
		}

		health, err := checker.Check(ctx)
		if err != nil {
			return clusterHealthMsg{err: err}
		}

		return clusterHealthMsg{health: scaling.HealthFromCluster(health)}
	}
}

func (m model) installComponent(component string, opts map[string]string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		clientset := m.client.Clientset()
		dynClient, dynErr := buildDynamicClient(m.client.KubeconfigPath(), m.client.ActiveContext())

		var err error

		switch component {
		case "metrics-server":
			if dynErr != nil {
				return installResultMsg{component: component, err: dynErr}
			}
			comp := components.NewMetricsServer(clientset, dynClient)
			if opts["action"] == "update" {
				err = comp.Update(ctx)
			} else {
				err = comp.Install(ctx, opts)
			}

		case "hetzner-ccm":
			if dynErr != nil {
				return installResultMsg{component: component, err: dynErr}
			}
			comp := components.NewHetznerCCM(clientset, dynClient)
			if opts["action"] == "update" {
				err = comp.Update(ctx)
			} else {
				err = comp.Install(ctx, opts)
			}

		case "konductor-operator":
			inst, instErr := installer.NewInstaller(m.client.KubeconfigPath(), m.client.ActiveContext())
			if instErr != nil {
				return installResultMsg{component: component, err: instErr}
			}
			comp := components.NewKonductorOperator(inst, "")
			if opts["action"] == "update" {
				err = comp.Update(ctx)
			} else {
				// Read talos config from path if provided.
				if path, ok := opts["talos_config_path"]; ok && path != "" {
					data, readErr := os.ReadFile(path)
					if readErr != nil {
						return installResultMsg{component: component, err: fmt.Errorf("reading talos config: %w", readErr)}
					}
					opts["talos_config"] = string(data)
				}
				err = comp.Install(ctx, opts)
			}

		default:
			err = fmt.Errorf("unknown component: %s", component)
		}

		return installResultMsg{component: component, err: err}
	}
}

func (m model) updateNodePool(msg scaling.UpdateNodePoolMsg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		dynClient, err := buildDynamicClient(m.client.KubeconfigPath(), m.client.ActiveContext())
		if err != nil {
			return installResultMsg{component: "nodepool-update", err: err}
		}

		gvr := schema.GroupVersionResource{Group: "konductor.io", Version: "v1alpha1", Resource: "nodepools"}

		// Fetch current pool.
		pool, err := dynClient.Resource(gvr).Get(ctx, msg.PoolName, metav1.GetOptions{})
		if err != nil {
			return installResultMsg{component: "nodepool-update", err: fmt.Errorf("fetching pool: %w", err)}
		}

		spec, _ := pool.Object["spec"].(map[string]interface{})
		if spec == nil {
			return installResultMsg{component: "nodepool-update", err: fmt.Errorf("pool has no spec")}
		}

		switch msg.Field {
		case "minNodes":
			val := parseInt64(msg.Value)
			spec["minNodes"] = val
		case "maxNodes":
			val := parseInt64(msg.Value)
			spec["maxNodes"] = val
		case "enabled":
			scaling, _ := spec["scaling"].(map[string]interface{})
			if scaling == nil {
				scaling = map[string]interface{}{}
				spec["scaling"] = scaling
			}
			scaling["enabled"] = msg.Value == "on"
		}

		_, err = dynClient.Resource(gvr).Update(ctx, pool, metav1.UpdateOptions{})
		if err != nil {
			return installResultMsg{component: "nodepool-update", err: fmt.Errorf("updating pool: %w", err)}
		}

		return installResultMsg{component: "nodepool-update", err: nil}
	}
}

func parseInt64(s string) int64 {
	var result int64
	for _, c := range s {
		if c >= '0' && c <= '9' {
			result = result*10 + int64(c-'0')
		}
	}
	return result
}

func (m model) createNodePool(msg scaling.CreateNodePoolMsg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		dynClient, err := buildDynamicClient(m.client.KubeconfigPath(), m.client.ActiveContext())
		if err != nil {
			return installResultMsg{component: "nodepool", err: err}
		}

		nodePool := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "konductor.io/v1alpha1",
				"kind":       "NodePool",
				"metadata": map[string]interface{}{
					"name": msg.Name,
				},
				"spec": map[string]interface{}{
					"provider": "hetzner",
					"providerConfig": map[string]interface{}{
						"serverType": msg.ServerType,
						"location":   msg.Location,
					},
					"minNodes": int64(msg.MinNodes),
					"maxNodes": int64(msg.MaxNodes),
					"scaling": map[string]interface{}{
						"enabled": msg.Enabled,
						"scaleUp": map[string]interface{}{
							"cpuThresholdPercent":        int64(80),
							"memoryThresholdPercent":     int64(80),
							"pendingPodsThreshold":       int64(1),
							"stabilizationWindowSeconds": int64(60),
							"step":                       int64(1),
						},
						"scaleDown": map[string]interface{}{
							"cpuThresholdPercent":        int64(30),
							"memoryThresholdPercent":     int64(30),
							"stabilizationWindowSeconds": int64(600),
							"step":                       int64(1),
						},
						"cooldownSeconds": int64(300),
					},
					"talos": map[string]interface{}{
						"configSecretRef": "konductor-secrets",
						"version":         m.detectTalosVersion(),
					},
				},
			},
		}

		gvr := schema.GroupVersionResource{
			Group:    "konductor.io",
			Version:  "v1alpha1",
			Resource: "nodepools",
		}

		_, err = dynClient.Resource(gvr).Create(ctx, nodePool, metav1.CreateOptions{})
		if err != nil {
			return installResultMsg{component: "nodepool", err: fmt.Errorf("creating NodePool: %w", err)}
		}

		return installResultMsg{component: "nodepool", err: nil}
	}
}

// deleteNodePool deletes a NodePool CRD from the cluster.
func (m model) deleteNodePool(poolName string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		dynClient, err := buildDynamicClient(m.client.KubeconfigPath(), m.client.ActiveContext())
		if err != nil {
			return installResultMsg{component: "delete-pool", err: err}
		}

		gvr := schema.GroupVersionResource{
			Group:    "konductor.io",
			Version:  "v1alpha1",
			Resource: "nodepools",
		}

		if err := dynClient.Resource(gvr).Delete(ctx, poolName, metav1.DeleteOptions{}); err != nil {
			return installResultMsg{component: "delete-pool", err: fmt.Errorf("deleting NodePool %q: %w", poolName, err)}
		}

		return scalingDataMsg{info: nil, err: nil} // trigger data refresh
	}
}

// detectTalosVersion auto-detects the Talos version from the connected cluster.
// Returns the version string (e.g. "v1.11.5") or empty string on failure.
func (m model) detectTalosVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ver, err := m.client.GetTalosVersion(ctx)
	if err != nil || ver == "" {
		return ""
	}
	return ver
}

// getHetznerProvider creates a Hetzner provider, trying multiple token sources:
// 1. Local config (~/.konductor/config.yaml hetzner.token)
// 2. HCLOUD_TOKEN environment variable
// 3. konductor-secrets Secret in the cluster (set during onboarding)
func (m model) getHetznerProvider() (*hetzner.HetznerProvider, error) {
	token := ""
	if len(m.cfg.Clusters) > 0 {
		token = m.cfg.Clusters[0].Hetzner.GetToken()
	}
	if token == "" {
		token = os.Getenv("HCLOUD_TOKEN")
	}
	// Fall back to reading the token from the cluster secret.
	if token == "" && m.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		secret, err := m.client.Clientset().CoreV1().Secrets("konductor-system").Get(ctx, "konductor-secrets", metav1.GetOptions{})
		if err == nil {
			if t, ok := secret.Data["hcloud-token"]; ok {
				token = string(t)
			}
		}
	}
	if token == "" {
		return nil, fmt.Errorf("no Hetzner Cloud token configured")
	}
	client, err := hetzner.NewClient(token)
	if err != nil {
		return nil, err
	}
	return hetzner.NewProvider(client), nil
}

// checkTalosImage checks if a Talos snapshot exists on Hetzner Cloud.
func (m model) checkTalosImage(talosVersion, arch string) tea.Cmd {
	return func() tea.Msg {
		prov, err := m.getHetznerProvider()
		if err != nil {
			return imageCheckMsg{err: err, talosVersion: talosVersion, arch: arch}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		img, err := prov.FindTalosImage(ctx, talosVersion, arch)
		if err != nil {
			return imageCheckMsg{err: err, talosVersion: talosVersion, arch: arch}
		}
		if img != nil {
			return imageCheckMsg{exists: true, talosVersion: talosVersion, arch: arch, imageID: img.ID}
		}
		return imageCheckMsg{exists: false, talosVersion: talosVersion, arch: arch}
	}
}

// createTalosImage creates a Talos snapshot on Hetzner Cloud with progress updates.
func (m model) createTalosImage(talosVersion, arch string) tea.Cmd {
	return func() tea.Msg {
		prov, err := m.getHetznerProvider()
		if err != nil {
			return imageCreateMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		result, err := prov.CreateTalosImage(ctx, hetzner.ImageCreateOpts{
			TalosVersion: talosVersion,
			Arch:         arch,
		})
		if err != nil {
			return imageCreateMsg{err: err}
		}
		return imageCreateMsg{imageID: result.ImageID}
	}
}

// discoverNodes discovers existing K8s nodes and suggests pools for import.
func (m model) discoverNodes() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		clientset := m.client.Clientset()
		discovered, err := operator.DiscoverNodes(ctx, clientset)
		if err != nil {
			return importDetectMsg{err: err}
		}

		pools := operator.SuggestPools(discovered)
		return importDetectMsg{pools: pools}
	}
}

// importNodes creates NodePool CRs, labels nodes, and creates NodeClaim CRs for all pools.
func (m model) importNodes(pools []operator.SuggestedPool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		dynClient, err := buildDynamicClient(m.client.KubeconfigPath(), m.client.ActiveContext())
		if err != nil {
			return importResultMsg{err: err}
		}

		clientset := m.client.Clientset()
		for _, pool := range pools {
			if err := operator.ImportNodes(ctx, dynClient, clientset, pool); err != nil {
				return importResultMsg{err: err}
			}
		}

		return importResultMsg{}
	}
}

// buildDynamicClient creates a dynamic Kubernetes client from kubeconfig.
func buildDynamicClient(kubeconfigPath, kubeContext string) (dynamic.Interface, error) {
	loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: kubeContext}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, overrides)
	restCfg, err := cc.ClientConfig()
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(restCfg)
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
		return m, tea.Batch(m.fetchAllData(), m.fetchClusterHealth())

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

	case clusterHealthMsg:
		if msg.err != nil {
			// Health check failed — don't overwrite other errors.
		} else {
			m.scalingView.SetHealthData(msg.health)
		}
		return m, nil

	case scaling.InstallComponentMsg:
		// Spawn background install goroutine.
		return m, m.installComponent(msg.Component, msg.Opts)

	case installResultMsg:
		m.scalingView.UpdateWizardProgress(scaling.InstallProgressMsg{
			Component: msg.component,
			Done:      true,
			Err:       msg.err,
		})
		// Refresh health data after install.
		if msg.err == nil && m.client != nil {
			return m, tea.Batch(m.fetchClusterHealth(), m.fetchScalingData())
		}
		return m, nil

	case scaling.UpdateNodePoolMsg:
		return m, m.updateNodePool(msg)

	case scaling.CreateNodePoolMsg:
		// Check if we have a Hetzner token for image pre-check.
		// If not, skip the image check and create the pool directly.
		_, provErr := m.getHetznerProvider()
		if provErr != nil {
			// No token available — create pool without image check.
			return m, m.createNodePool(msg)
		}
		// Token available — check if the Talos image exists first.
		m.pendingPoolMsg = &msg
		arch := hetzner.ArchFromServerType(msg.ServerType)
		talosVersion := m.detectTalosVersion()
		m.scalingView.ShowImageProgress("Checking Talos image...")
		return m, m.checkTalosImage(talosVersion, arch)

	case imageCheckMsg:
		if msg.err != nil {
			m.scalingView.UpdateWizardProgress(scaling.InstallProgressMsg{
				Component: "image",
				Message:   fmt.Sprintf("Image check failed: %s", msg.err),
				Done:      true,
				Err:       msg.err,
			})
			return m, nil
		}
		if msg.exists {
			// Image already exists, proceed to create pool.
			m.scalingView.ShowImageProgress("")
			poolMsg := m.pendingPoolMsg
			m.pendingPoolMsg = nil
			if poolMsg != nil {
				return m, m.createNodePool(*poolMsg)
			}
			return m, nil
		}
		// Image doesn't exist — create it automatically.
		m.scalingView.ShowImageProgress("Creating Talos image (this takes 3-5 minutes)...")
		return m, m.createTalosImage(msg.talosVersion, msg.arch)

	case imageProgressMsg:
		m.scalingView.ShowImageProgress(fmt.Sprintf("[%d/%d] %s", msg.step, msg.total, msg.message))
		return m, nil

	case imageCreateMsg:
		if msg.err != nil {
			m.scalingView.UpdateWizardProgress(scaling.InstallProgressMsg{
				Component: "image",
				Message:   fmt.Sprintf("Image creation failed: %s", msg.err),
				Done:      true,
				Err:       msg.err,
			})
			m.pendingPoolMsg = nil
			return m, nil
		}
		// Image created, proceed to create the pool.
		m.scalingView.ShowImageProgress("Image created, creating pool...")
		poolMsg := m.pendingPoolMsg
		m.pendingPoolMsg = nil
		if poolMsg != nil {
			return m, m.createNodePool(*poolMsg)
		}
		return m, nil

	case scaling.DeleteNodePoolMsg:
		return m, m.deleteNodePool(msg.PoolName)

	case scaling.ImportNodesMsg:
		// Trigger node discovery.
		return m, m.discoverNodes()

	case importDetectMsg:
		// Forward discovered pools to the import wizard.
		m.scalingView.UpdateImportDetect(msg.pools, msg.err)
		return m, nil

	case scaling.ImportConfirmMsg:
		// User confirmed import — run it.
		return m, m.importNodes(msg.Pools)

	case importResultMsg:
		// Import complete — update wizard and refresh data.
		m.scalingView.UpdateImportResult(msg.err)
		if msg.err == nil && m.client != nil {
			return m, tea.Batch(m.fetchScalingData(), m.fetchClusterHealth())
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
		cmds = append(cmds, m.fetchAllData(), m.fetchScalingData(), m.fetchClusterHealth(), m.scheduleTick())
		// Auto-refresh logs if viewing pod logs.
		if m.activeTab == tabPods && m.podsView.InLogMode() && m.client != nil {
			ns, pod, container, tailLines := m.podsView.LogTarget()
			if pod != "" {
				cmds = append(cmds, m.fetchLogs(ns, pod, container, tailLines))
			}
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		// Wizard overlay takes priority over everything.
		if m.activeTab == tabScaling && m.scalingView.WizardVisible() {
			var cmd tea.Cmd
			m.scalingView, cmd = m.scalingView.Update(msg)
			return m, cmd
		}

		// Context switcher overlay takes priority.
		if m.ctxSwitcher.Visible() {
			var cmd tea.Cmd
			m.ctxSwitcher, cmd = m.ctxSwitcher.Update(msg)
			return m, cmd
		}

		// When a view is capturing text input, only allow quit and let
		// everything else pass through to the active tab.
		inputActive := (m.activeTab == tabScaling && (m.scalingView.InPoolEdit() || m.scalingView.WizardVisible()))

		switch {
		case key.Matches(msg, keys.Quit):
			if inputActive {
				// Don't quit during text input — let 'q' be typed.
				break
			}
			return m, tea.Quit
		case key.Matches(msg, keys.Context):
			if inputActive {
				break
			}
			m.ctxSwitcher.Show()
			return m, nil
		case key.Matches(msg, keys.Tab):
			if inputActive {
				break
			}
			m.activeTab = tab((int(m.activeTab) + 1) % len(tabList))
			return m, nil
		case key.Matches(msg, keys.ShiftTab):
			if inputActive {
				break
			}
			m.activeTab = tab((int(m.activeTab) - 1 + len(tabList)) % len(tabList))
			return m, nil
		case key.Matches(msg, keys.Number1), key.Matches(msg, keys.Number2),
			key.Matches(msg, keys.Number3), key.Matches(msg, keys.Number4),
			key.Matches(msg, keys.Number5):
			if inputActive {
				break // Let numbers pass to the edit field.
			}
			switch {
			case key.Matches(msg, keys.Number1):
				m.activeTab = tabDashboard
			case key.Matches(msg, keys.Number2):
				m.activeTab = tabNodes
			case key.Matches(msg, keys.Number3):
				m.activeTab = tabNamespaces
			case key.Matches(msg, keys.Number4):
				m.activeTab = tabPods
			case key.Matches(msg, keys.Number5):
				m.activeTab = tabScaling
			}
			return m, nil
		case key.Matches(msg, keys.Refresh):
			if inputActive {
				break
			}
			return m, tea.Batch(m.fetchAllData(), m.fetchClusterHealth())
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

	// Overlay wizard if visible (takes priority over context switcher).
	if m.activeTab == tabScaling && m.scalingView.WizardVisible() {
		return m.scalingView.WizardView(m.width, m.height)
	}

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
		Render("KONDUCTOR")

	ver := lipgloss.NewStyle().
		Foreground(styles.ColorBorder).
		Render(" v" + version.Version)

	left := logo + ver

	if m.client != nil {
		sep := lipgloss.NewStyle().
			Foreground(styles.ColorTextDim).
			Render("  |  ")
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
		helpItems = append([]struct{ key, desc string }{{"j/k", "select"}}, helpItems...)
	case tabNamespaces:
		helpItems = append([]struct{ key, desc string }{{"j/k", "select"}, {"enter", "-> pods"}}, helpItems...)
	case tabPods:
		if m.podsView.InLogMode() {
			helpItems = append([]struct{ key, desc string }{{"j/k", "scroll"}, {"esc", "back"}, {"l", "live"}, {"f", "full"}}, helpItems...)
		} else {
			helpItems = append([]struct{ key, desc string }{{"j/k", "select"}, {"<->", "ns"}, {"s", "sort"}, {"enter", "logs"}}, helpItems...)
		}
	case tabScaling:
		if m.scalingView.WizardVisible() {
			helpItems = append([]struct{ key, desc string }{{"esc", "cancel"}, {"enter", "confirm"}}, helpItems...)
		} else if m.scalingView.InPoolEdit() {
			helpItems = append([]struct{ key, desc string }{{"tab", "field"}, {"space", "toggle"}, {"enter", "apply"}, {"esc", "cancel"}}, helpItems...)
		} else if m.scalingView.InPoolDetail() {
			helpItems = append([]struct{ key, desc string }{{"e", "edit"}, {"esc", "back"}}, helpItems...)
		} else {
			helpItems = append([]struct{ key, desc string }{{"j/k", "select"}, {"enter", "detail"}, {"s", "settings"}, {"i", "import"}, {"n", "new"}}, helpItems...)
		}
	}

	var parts []string
	for _, item := range helpItems {
		k := styles.KeyStyle.Render(item.key)
		d := styles.KeyDescStyle.Render(item.desc)
		parts = append(parts, fmt.Sprintf("%s %s", k, d))
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, joinWithSep(parts, "  |  ")...)
}

func joinWithSep(items []string, sep string) []string {
	if len(items) == 0 {
		return nil
	}
	result := []string{items[0]}
	for _, item := range items[1:] {
		result = append(result, styles.KeyDescStyle.Render("  |  "), item)
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
