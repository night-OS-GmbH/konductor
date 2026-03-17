package scaling

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/night-OS-GmbH/konductor/internal/tui/styles"
)

type wizardStep int

const (
	stepConfirm    wizardStep = iota // "Install X? [Yes/No]"
	stepTokenInput                   // For operator/CCM: enter Hetzner token
	stepConfigPath                   // For operator: enter config path
	stepInstalling                   // Progress indicator
	stepDone                         // Result display
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
	case stepInstalling:
		// No user input during installation.
		return w, nil
	case stepDone:
		return w.updateDone(keyMsg)
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
	case "cert-manager":
		details = append(details, "This will deploy cert-manager for automatic TLS certificate")
		details = append(details, "provisioning and renewal via Let's Encrypt.")
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
		title := styles.CriticalStyle.Render("Installation Failed")
		return strings.Join([]string{
			title,
			"",
			styles.CriticalStyle.Render("Error: " + w.progressErr.Error()),
			"",
			dim.Render("enter/esc close"),
		}, "\n")
	}

	title := styles.HealthyStyle.Render("Installation Complete")
	msg := dim.Render(w.component + " has been installed successfully.")

	return strings.Join([]string{
		title,
		"",
		styles.HealthyStyle.Render("●") + " " + msg,
		"",
		dim.Render("enter/esc close"),
	}, "\n")
}

// maskToken replaces all but the last 4 characters of a token with asterisks.
func maskToken(token string) string {
	if len(token) <= 4 {
		return strings.Repeat("*", len(token))
	}
	return strings.Repeat("*", len(token)-4) + token[len(token)-4:]
}
