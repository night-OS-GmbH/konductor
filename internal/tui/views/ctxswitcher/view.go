package ctxswitcher

import (
	"sort"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/night-OS-GmbH/konductor/internal/tui/styles"
)

// ContextSelectedMsg is emitted when the user picks a context.
type ContextSelectedMsg struct {
	Context string
}

type Model struct {
	contexts []string
	active   string
	cursor   int
	visible  bool
}

func New(contexts []string, active string) Model {
	sorted := make([]string, len(contexts))
	copy(sorted, contexts)
	sort.Strings(sorted)
	return Model{
		contexts: sorted,
		active:   active,
	}
}

func (m *Model) SetContexts(contexts []string, active string) {
	sorted := make([]string, len(contexts))
	copy(sorted, contexts)
	sort.Strings(sorted)
	m.contexts = sorted
	m.active = active
}

func (m *Model) Show()    { m.visible = true }
func (m *Model) Hide()    { m.visible = false }
func (m Model) Visible() bool { return m.visible }

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.visible {
		return m, nil
	}

	if msg, ok := msg.(tea.KeyMsg); ok {
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("esc", "c"))):
			m.visible = false
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
			if m.cursor < len(m.contexts)-1 {
				m.cursor++
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
			if m.cursor > 0 {
				m.cursor--
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
			if m.cursor < len(m.contexts) {
				selected := m.contexts[m.cursor]
				m.visible = false
				return m, func() tea.Msg {
					return ContextSelectedMsg{Context: selected}
				}
			}
		}
	}

	return m, nil
}

func (m Model) View(width, height int) string {
	if !m.visible || len(m.contexts) == 0 {
		return ""
	}

	panelW := 50
	if panelW > width-4 {
		panelW = width - 4
	}
	innerW := panelW - 6

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(styles.ColorText).
		Render("Switch Context")

	hint := lipgloss.NewStyle().
		Foreground(styles.ColorTextDim).
		Render("j/k navigate  ·  enter select  ·  esc cancel")

	var items []string
	for i, ctx := range m.contexts {
		icon := "  "
		nameStyle := lipgloss.NewStyle().Foreground(styles.ColorTextDim)

		if ctx == m.active {
			icon = styles.HealthyStyle.Render("● ")
			nameStyle = lipgloss.NewStyle().Foreground(styles.ColorText)
		}

		name := ctx
		if len(name) > innerW-4 {
			name = name[:innerW-6] + ".."
		}

		line := icon + nameStyle.Render(name)

		if i == m.cursor {
			line = lipgloss.NewStyle().
				Background(styles.ColorBgActive).
				Width(innerW).
				Render(line)
		}

		items = append(items, line)
	}

	content := title + "\n\n"
	for _, item := range items {
		content += item + "\n"
	}
	content += "\n" + hint

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.ColorPrimary).
		Padding(1, 2).
		Width(panelW).
		Render(content)

	// Center the panel.
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, panel)
}
