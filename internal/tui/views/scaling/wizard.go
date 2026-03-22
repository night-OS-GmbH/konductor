package scaling

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/night-OS-GmbH/konductor/internal/operator"
	"github.com/night-OS-GmbH/konductor/internal/tui/styles"
)

type wizardStep int

const (
	stepConfirm    wizardStep = iota // "Install X? [Yes/No]"
	stepTokenInput                   // For operator/CCM: enter Hetzner token
	stepConfigPath                   // For operator: enter config path
	stepInstalling                   // Progress indicator
	stepDone                         // Result display

	// NodePool wizard steps.
	stepNPServerType // Select server type
	stepNPLocation   // Select location
	stepNPMinMax     // Set min/max nodes
	stepNPConfirm    // Review and confirm

	// Import wizard steps.
	stepImportDetect  // "Detecting nodes..."
	stepImportConfirm // "Found X nodes, import as Y pools?"
	stepImportProgress // "Importing..."
)

// WizardModel manages the state of the component installation wizard overlay.
type WizardModel struct {
	visible   bool
	component string
	step      wizardStep

	// Input state.
	tokenInput string
	configPath string
	cursor     int // for confirm step: 0=yes, 1=no

	// Progress state.
	progressMsg string
	progressErr error
	done        bool

	// NodePool wizard state.
	npName       string
	npNameManual bool // true if user manually edited the name
	npServerType string
	npLocation   string
	npMinNodes   string
	npMaxNodes   string
	npEnabled    bool
	npField      int // which field is being edited (0=name, 1=serverType, 2=location, 3=min, 4=max, 5=enabled)

	// Import wizard state.
	importPools []operator.SuggestedPool
}

// NewWizard creates a new wizard for installing the given component.
func NewWizard(component string) *WizardModel {
	w := &WizardModel{
		visible:   true,
		component: component,
	}

	// Components that need extra configuration start at a different step.
	switch component {
	case "konductor-operator":
		w.step = stepTokenInput
	case "hetzner-ccm":
		w.step = stepTokenInput
	default:
		// Simple components go straight to confirm.
		w.step = stepConfirm
	}

	return w
}

func (w WizardModel) Update(msg tea.Msg) (WizardModel, tea.Cmd) {
	if !w.visible {
		return w, nil
	}

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return w, nil
	}

	switch w.step {
	case stepConfirm:
		return w.updateConfirm(keyMsg)
	case stepTokenInput:
		return w.updateTokenInput(keyMsg)
	case stepConfigPath:
		return w.updateConfigPath(keyMsg)
	case stepInstalling, stepImportDetect, stepImportProgress:
		// No user input during installation/detection/import.
		return w, nil
	case stepDone:
		return w.updateDone(keyMsg)
	case stepNPServerType, stepNPLocation, stepNPMinMax:
		return w.updateNodePool(keyMsg)
	case stepNPConfirm:
		return w.updateNPConfirm(keyMsg)
	case stepImportConfirm:
		return w.updateImportConfirm(keyMsg)
	}

	return w, nil
}

func (w WizardModel) updateConfirm(msg tea.KeyMsg) (WizardModel, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
		w.visible = false
		return w, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("h", "left"))):
		if w.cursor > 0 {
			w.cursor--
		}
	case key.Matches(msg, key.NewBinding(key.WithKeys("l", "right"))):
		if w.cursor < 1 {
			w.cursor++
		}
	case key.Matches(msg, key.NewBinding(key.WithKeys("y"))):
		w.cursor = 0
		return w.confirmInstall()
	case key.Matches(msg, key.NewBinding(key.WithKeys("n"))):
		w.visible = false
		return w, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
		if w.cursor == 0 {
			return w.confirmInstall()
		}
		w.visible = false
		return w, nil
	}
	return w, nil
}

func (w WizardModel) confirmInstall() (WizardModel, tea.Cmd) {
	w.step = stepInstalling
	w.progressMsg = "Installing " + w.component + "..."

	opts := make(map[string]string)
	if w.tokenInput != "" {
		opts["hcloud_token"] = w.tokenInput
	}
	if w.configPath != "" {
		opts["talos_config_path"] = w.configPath
	}

	return w, func() tea.Msg {
		return InstallComponentMsg{
			Component: w.component,
			Opts:      opts,
		}
	}
}

func (w WizardModel) updateTokenInput(msg tea.KeyMsg) (WizardModel, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
		w.visible = false
		return w, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
		if w.tokenInput == "" {
			return w, nil
		}
		// Operator needs config path next; CCM goes to confirm.
		if w.component == "konductor-operator" {
			w.step = stepConfigPath
		} else {
			w.step = stepConfirm
			w.cursor = 0
		}
		return w, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("backspace"))):
		if len(w.tokenInput) > 0 {
			w.tokenInput = w.tokenInput[:len(w.tokenInput)-1]
		}
	default:
		// Accept printable characters.
		if len(msg.Runes) > 0 {
			w.tokenInput += string(msg.Runes)
		}
	}
	return w, nil
}

func (w WizardModel) updateConfigPath(msg tea.KeyMsg) (WizardModel, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
		w.visible = false
		return w, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
		if w.configPath == "" {
			return w, nil
		}
		w.step = stepConfirm
		w.cursor = 0
		return w, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("backspace"))):
		if len(w.configPath) > 0 {
			w.configPath = w.configPath[:len(w.configPath)-1]
		}
	default:
		if len(msg.Runes) > 0 {
			w.configPath += string(msg.Runes)
		}
	}
	return w, nil
}

func (w WizardModel) updateDone(msg tea.KeyMsg) (WizardModel, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("esc", "enter"))):
		w.visible = false
		return w, nil
	}
	return w, nil
}

// View renders the wizard overlay.
func (w WizardModel) View(width, height int) string {
	if !w.visible {
		return ""
	}

	panelW := 60
	if panelW > width-4 {
		panelW = width - 4
	}

	var content string
	switch w.step {
	case stepConfirm:
		content = w.viewConfirm(panelW)
	case stepTokenInput:
		content = w.viewTokenInput(panelW)
	case stepConfigPath:
		content = w.viewConfigPath(panelW)
	case stepInstalling:
		content = w.viewInstalling(panelW)
	case stepDone:
		content = w.viewDone(panelW)
	case stepNPServerType, stepNPLocation, stepNPMinMax:
		content = w.viewNodePoolForm(panelW)
	case stepNPConfirm:
		content = w.viewNPConfirm(panelW)
	case stepImportDetect:
		content = w.viewImportDetect(panelW)
	case stepImportConfirm:
		content = w.viewImportConfirm(panelW)
	case stepImportProgress:
		content = w.viewImportProgress(panelW)
	}

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.ColorPrimary).
		Padding(1, 2).
		Width(panelW).
		Render(content)

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}

func (w WizardModel) viewConfirm(panelW int) string {
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(styles.ColorText).
		Render("Install " + w.component + "?")

	// Show summary of what will be done.
	var details []string
	switch w.component {
	case "konductor-operator":
		details = append(details, "This will deploy the Konductor operator to manage")
		details = append(details, "node pool scaling automatically in your cluster.")
		if w.tokenInput != "" {
			details = append(details, "")
			details = append(details, dim.Render("Hetzner Token: ")+maskToken(w.tokenInput))
			details = append(details, dim.Render("Talos Config:  ")+w.configPath)
		}
	case "hetzner-ccm":
		details = append(details, "This will deploy the Hetzner Cloud Controller Manager")
		details = append(details, "for load balancers, node lifecycle, and networking.")
		if w.tokenInput != "" {
			details = append(details, "")
			details = append(details, dim.Render("Hetzner Token: ")+maskToken(w.tokenInput))
		}
	case "metrics-server":
		details = append(details, "This will deploy metrics-server for CPU/memory metrics.")
		details = append(details, "Required for kubectl top, HPA, and the Konductor dashboard.")
	default:
		details = append(details, "This will install "+w.component+" into your cluster.")
	}

	// Yes/No buttons.
	yesStyle := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Padding(0, 2)
	noStyle := lipgloss.NewStyle().Foreground(styles.ColorTextDim).Padding(0, 2)
	if w.cursor == 0 {
		yesStyle = lipgloss.NewStyle().
			Background(styles.ColorHealthy).
			Foreground(lipgloss.Color("#000000")).
			Bold(true).
			Padding(0, 2)
	} else {
		noStyle = lipgloss.NewStyle().
			Background(styles.ColorCritical).
			Foreground(lipgloss.Color("#000000")).
			Bold(true).
			Padding(0, 2)
	}

	buttons := yesStyle.Render("Yes") + "  " + noStyle.Render("No")

	lines := []string{title, ""}
	for _, d := range details {
		lines = append(lines, dim.Render(d))
	}
	lines = append(lines, "")
	lines = append(lines, buttons)
	lines = append(lines, "")
	lines = append(lines, dim.Render("y/n confirm  esc cancel"))

	return strings.Join(lines, "\n")
}

func (w WizardModel) viewTokenInput(panelW int) string {
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	bright := lipgloss.NewStyle().Foreground(styles.ColorText)

	title := bright.Bold(true).Render("Hetzner Cloud API Token")

	description := dim.Render("Enter your Hetzner Cloud API token. This is required for")
	description2 := dim.Render("the component to interact with Hetzner Cloud.")

	// Masked input field.
	inputW := panelW - 8
	if inputW < 20 {
		inputW = 20
	}

	masked := maskToken(w.tokenInput)
	cursor := lipgloss.NewStyle().
		Foreground(styles.ColorPrimary).
		Bold(true).
		Render("_")

	inputContent := masked + cursor

	// Pad to fill input width.
	inputBox := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(styles.ColorBorder).
		Padding(0, 1).
		Width(inputW).
		Render(inputContent)

	hint := dim.Render("enter continue  esc cancel")

	return strings.Join([]string{
		title,
		"",
		description,
		description2,
		"",
		inputBox,
		"",
		hint,
	}, "\n")
}

func (w WizardModel) viewConfigPath(panelW int) string {
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	bright := lipgloss.NewStyle().Foreground(styles.ColorText)

	title := bright.Bold(true).Render("Talos Worker Config Path")

	description := dim.Render("Enter the path to your Talos worker machine configuration.")
	description2 := dim.Render("This is typically ~/.talos/config or a custom path.")

	inputW := panelW - 8
	if inputW < 20 {
		inputW = 20
	}

	cursor := lipgloss.NewStyle().
		Foreground(styles.ColorPrimary).
		Bold(true).
		Render("_")

	inputContent := w.configPath + cursor

	inputBox := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(styles.ColorBorder).
		Padding(0, 1).
		Width(inputW).
		Render(inputContent)

	hint := dim.Render("enter continue  esc cancel")

	return strings.Join([]string{
		title,
		"",
		description,
		description2,
		"",
		inputBox,
		"",
		hint,
	}, "\n")
}

func (w WizardModel) viewInstalling(panelW int) string {
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(styles.ColorText).
		Render("Installing " + w.component)

	spinner := styles.InfoStyle.Render("◌ ") + dim.Render(w.progressMsg)

	return strings.Join([]string{
		title,
		"",
		spinner,
		"",
		dim.Render("Please wait..."),
	}, "\n")
}

func (w WizardModel) viewDone(panelW int) string {
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)

	if w.progressErr != nil {
		title := styles.CriticalStyle.Render("Operation Failed")
		return strings.Join([]string{
			title,
			"",
			styles.CriticalStyle.Render("Error: " + w.progressErr.Error()),
			"",
			dim.Render("enter/esc close"),
		}, "\n")
	}

	title := styles.HealthyStyle.Render("Operation Complete")
	msg := w.progressMsg
	if msg == "" {
		msg = w.component + " has been installed successfully."
	}

	return strings.Join([]string{
		title,
		"",
		styles.HealthyStyle.Render("●") + " " + dim.Render(msg),
		"",
		dim.Render("enter/esc close"),
	}, "\n")
}

// --- NodePool Wizard ---

// NewNodePoolWizard creates a wizard for creating a new NodePool.
func NewNodePoolWizard() *WizardModel {
	return &WizardModel{
		visible:      true,
		component:    "nodepool",
		step:         stepNPServerType,
		npName:       "workers-cpx31-nbg1",
		npServerType: "cpx31",
		npLocation:   "nbg1",
		npMinNodes:   "3",
		npMaxNodes:   "10",
		npEnabled:    false, // Safe default: start with scaling off.
	}
}

// autoGeneratePoolName updates the pool name from serverType and location,
// unless the user has manually edited it.
func (w *WizardModel) autoGeneratePoolName() {
	if w.npNameManual {
		return
	}
	w.npName = "workers-" + w.npServerType + "-" + w.npLocation
}

func (w WizardModel) updateNodePool(msg tea.KeyMsg) (WizardModel, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
		w.visible = false
		return w, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("tab", "down", "j"))):
		if w.npField < 5 {
			w.npField++
		}
		return w, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("shift+tab", "up", "k"))):
		if w.npField > 0 {
			w.npField--
		}
		return w, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
		if w.npField == 5 {
			// Toggle enabled.
			w.npEnabled = !w.npEnabled
			return w, nil
		}
		// Move to confirm.
		w.step = stepNPConfirm
		w.cursor = 0
		return w, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("backspace"))):
		w.deleteFieldChar()
		return w, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys(" "))):
		if w.npField == 5 {
			w.npEnabled = !w.npEnabled
			return w, nil
		}
	}

	// Character input for text fields.
	if len(msg.Runes) > 0 && w.npField < 5 {
		w.appendFieldChar(string(msg.Runes))
		// Auto-update name when serverType or location changes.
		if w.npField == 1 || w.npField == 2 {
			w.autoGeneratePoolName()
		}
	}

	return w, nil
}

func (w *WizardModel) appendFieldChar(ch string) {
	switch w.npField {
	case 0:
		w.npName += ch
		w.npNameManual = true
	case 1:
		w.npServerType += ch
	case 2:
		w.npLocation += ch
	case 3:
		w.npMinNodes += ch
	case 4:
		w.npMaxNodes += ch
	}
}

func (w *WizardModel) deleteFieldChar() {
	switch w.npField {
	case 0:
		if len(w.npName) > 0 {
			w.npName = w.npName[:len(w.npName)-1]
			w.npNameManual = true
		}
	case 1:
		if len(w.npServerType) > 0 {
			w.npServerType = w.npServerType[:len(w.npServerType)-1]
		}
	case 2:
		if len(w.npLocation) > 0 {
			w.npLocation = w.npLocation[:len(w.npLocation)-1]
		}
	case 3:
		if len(w.npMinNodes) > 0 {
			w.npMinNodes = w.npMinNodes[:len(w.npMinNodes)-1]
		}
	case 4:
		if len(w.npMaxNodes) > 0 {
			w.npMaxNodes = w.npMaxNodes[:len(w.npMaxNodes)-1]
		}
	}
}

func (w WizardModel) updateNPConfirm(msg tea.KeyMsg) (WizardModel, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
		w.step = stepNPServerType
		return w, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("y", "enter"))):
		w.step = stepInstalling
		w.progressMsg = "Creating NodePool..."

		minN := parseInt32(w.npMinNodes, 3)
		maxN := parseInt32(w.npMaxNodes, 10)

		return w, func() tea.Msg {
			return CreateNodePoolMsg{
				Name:       w.npName,
				ServerType: w.npServerType,
				Location:   w.npLocation,
				MinNodes:   minN,
				MaxNodes:   maxN,
				Enabled:    w.npEnabled,
			}
		}
	case key.Matches(msg, key.NewBinding(key.WithKeys("n"))):
		w.visible = false
		return w, nil
	}
	return w, nil
}

func (w WizardModel) viewNodePoolForm(panelW int) string {
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	bright := lipgloss.NewStyle().Foreground(styles.ColorText)
	cursor := lipgloss.NewStyle().Foreground(styles.ColorPrimary).Bold(true).Render("_")

	title := bright.Bold(true).Render("Create NodePool")

	fields := []struct {
		label string
		value string
	}{
		{"Name", w.npName},
		{"Server Type", w.npServerType},
		{"Location", w.npLocation},
		{"Min Nodes", w.npMinNodes},
		{"Max Nodes", w.npMaxNodes},
	}

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	lines = append(lines, dim.Render("Configure your autoscaling node pool."))
	lines = append(lines, dim.Render("Scaling starts disabled (observe-only) by default."))
	lines = append(lines, "")

	labelW := 14
	for i, f := range fields {
		label := lipgloss.NewStyle().Width(labelW).Foreground(styles.ColorTextDim).Render(f.label + ":")
		val := f.value
		if i == w.npField {
			val = bright.Render(val) + cursor
			label = lipgloss.NewStyle().Width(labelW).Foreground(styles.ColorPrimary).Bold(true).Render(f.label + ":")
		} else {
			val = bright.Render(val)
		}
		lines = append(lines, "  "+label+" "+val)
	}

	// Scaling enabled toggle.
	enabledLabel := lipgloss.NewStyle().Width(labelW).Foreground(styles.ColorTextDim).Render("Scaling:")
	var enabledVal string
	if w.npEnabled {
		enabledVal = styles.WarningStyle.Render("[x]") + " " + styles.WarningStyle.Render("live -- scaling actions will execute")
	} else {
		enabledVal = styles.HealthyStyle.Render("[x]") + " " + dim.Render("disabled -- observe-only, no scaling actions")
	}
	if w.npField == 5 {
		enabledLabel = lipgloss.NewStyle().Width(labelW).Foreground(styles.ColorPrimary).Bold(true).Render("Scaling:")
	}
	lines = append(lines, "  "+enabledLabel+" "+enabledVal)

	lines = append(lines, "")
	lines = append(lines, dim.Render("tab/j/k navigate  enter confirm  space toggle  esc cancel"))

	return strings.Join(lines, "\n")
}

func (w WizardModel) viewNPConfirm(panelW int) string {
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	bright := lipgloss.NewStyle().Foreground(styles.ColorText)

	title := bright.Bold(true).Render("Create NodePool?")

	var modeStr string
	if w.npEnabled {
		modeStr = styles.WarningStyle.Render("Live (scaling active)")
	} else {
		modeStr = styles.HealthyStyle.Render("Disabled (observe-only)")
	}

	lines := []string{
		title,
		"",
		dim.Render("Server Type:  ") + bright.Render(w.npServerType),
		dim.Render("Location:     ") + bright.Render(w.npLocation),
		dim.Render("Min Nodes:    ") + bright.Render(w.npMinNodes),
		dim.Render("Max Nodes:    ") + bright.Render(w.npMaxNodes),
		dim.Render("Mode:         ") + modeStr,
		"",
		dim.Render("y/enter create  n/esc cancel"),
	}

	return strings.Join(lines, "\n")
}

// --- Import Wizard ---

// NewImportWizard creates a wizard for importing existing nodes.
func NewImportWizard() *WizardModel {
	return &WizardModel{
		visible:     true,
		component:   "import",
		step:        stepImportDetect,
		progressMsg: "Detecting existing nodes...",
	}
}

func (w WizardModel) updateImportConfirm(msg tea.KeyMsg) (WizardModel, tea.Cmd) {
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
		w.visible = false
		return w, nil
	case key.Matches(msg, key.NewBinding(key.WithKeys("y", "enter"))):
		w.step = stepImportProgress
		w.progressMsg = "Importing nodes..."
		pools := w.importPools
		return w, func() tea.Msg {
			return ImportConfirmMsg{Pools: pools}
		}
	case key.Matches(msg, key.NewBinding(key.WithKeys("n"))):
		w.visible = false
		return w, nil
	}
	return w, nil
}

func (w WizardModel) viewImportDetect(panelW int) string {
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(styles.ColorText).
		Render("Import Existing Nodes")

	spinner := styles.InfoStyle.Render("◌ ") + dim.Render(w.progressMsg)

	return strings.Join([]string{
		title,
		"",
		spinner,
		"",
		dim.Render("Please wait..."),
	}, "\n")
}

func (w WizardModel) viewImportConfirm(panelW int) string {
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)
	bright := lipgloss.NewStyle().Foreground(styles.ColorText)

	title := bright.Bold(true).Render("Import Existing Nodes")

	if len(w.importPools) == 0 {
		return strings.Join([]string{
			title,
			"",
			dim.Render("No nodes found to import."),
			"",
			dim.Render("enter/esc close"),
		}, "\n")
	}

	var lines []string
	lines = append(lines, title)
	lines = append(lines, "")
	lines = append(lines, dim.Render("Found existing nodes to import as pools:"))
	lines = append(lines, "")

	for _, pool := range w.importPools {
		nodeCount := len(pool.Nodes)
		var readyCount int
		for _, n := range pool.Nodes {
			if n.Ready {
				readyCount++
			}
		}

		var roleIndicator string
		if pool.Role == "control-plane" {
			roleIndicator = styles.InfoStyle.Render("CP")
		} else {
			roleIndicator = dim.Render("WK")
		}

		poolLine := fmt.Sprintf("  %s  %-24s  %d nodes (%d ready)  %s",
			roleIndicator,
			bright.Render(pool.Name),
			nodeCount,
			readyCount,
			dim.Render("scaling: off"),
		)
		lines = append(lines, poolLine)
	}

	lines = append(lines, "")
	lines = append(lines, dim.Render("Pools will be created with scaling disabled (safe default)."))
	lines = append(lines, dim.Render("You can enable scaling per pool after import."))
	lines = append(lines, "")
	lines = append(lines, dim.Render("y/enter import  n/esc cancel"))

	return strings.Join(lines, "\n")
}

func (w WizardModel) viewImportProgress(panelW int) string {
	dim := lipgloss.NewStyle().Foreground(styles.ColorTextDim)

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(styles.ColorText).
		Render("Importing Nodes")

	spinner := styles.InfoStyle.Render("◌ ") + dim.Render(w.progressMsg)

	return strings.Join([]string{
		title,
		"",
		spinner,
		"",
		dim.Render("Creating NodePool CRs, labeling nodes, creating NodeClaims..."),
	}, "\n")
}

func parseInt32(s string, fallback int32) int32 {
	var result int32
	for _, c := range s {
		if c >= '0' && c <= '9' {
			result = result*10 + int32(c-'0')
		}
	}
	if result == 0 {
		return fallback
	}
	return result
}

// maskToken replaces all but the last 4 characters of a token with asterisks.
func maskToken(token string) string {
	if len(token) <= 4 {
		return strings.Repeat("*", len(token))
	}
	return strings.Repeat("*", len(token)-4) + token[len(token)-4:]
}
