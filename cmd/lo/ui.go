package main

// BubbleTea UI models, styles, and interactive components.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- Shared styles ---

var (
	titleStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("45"))
	promptStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	mutedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	warnStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	successStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	previewStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))
	checkedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
)

// --- Project picker (single selection) ---

// projectPickerModel is an interactive fuzzy-filter list for choosing one project.
type projectPickerModel struct {
	projects []string
	stackMap map[string]string
	filter   string
	cursor   int
	offset   int
	height   int
	selected string
	canceled bool
}

func newProjectPickerModel(projects []string, stackMap map[string]string, initialFilter string) *projectPickerModel {
	return &projectPickerModel{
		projects: projects,
		stackMap: stackMap,
		filter:   strings.TrimSpace(initialFilter),
		height:   12,
	}
}

func (m *projectPickerModel) Init() tea.Cmd {
	return nil
}

func (m *projectPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Reserve lines for header, help, and footer.
		available := msg.Height - 9
		if available < 5 {
			available = 5
		}
		if available > 20 {
			available = 20
		}
		m.height = available
		m.ensureCursorBounds()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.canceled = true
			return m, tea.Quit
		case "enter":
			filtered := m.filteredProjects()
			if len(filtered) > 0 {
				m.selected = filtered[m.cursor]
				return m, tea.Quit
			}
			return m, nil
		case "up", "k", "ctrl+p":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j", "ctrl+n":
			if m.cursor < len(m.filteredProjects())-1 {
				m.cursor++
			}
		case "backspace", "ctrl+h":
			runes := []rune(m.filter)
			if len(runes) > 0 {
				m.filter = string(runes[:len(runes)-1])
				m.cursor = 0
				m.offset = 0
			}
		default:
			runes := []rune(msg.String())
			if len(runes) == 1 && runes[0] >= 32 {
				m.filter += string(runes)
				m.cursor = 0
				m.offset = 0
			}
		}

		m.ensureCursorBounds()
		return m, nil
	}

	return m, nil
}

func (m *projectPickerModel) View() string {
	filtered := m.filteredProjects()
	m.ensureCursorBounds()

	var b strings.Builder
	b.WriteString(titleStyle.Render("🚀 Liftoff"))
	b.WriteString(titleStyle.Render("\n\n📚 Select a project"))
	b.WriteString("\n")
	if m.filter == "" {
		b.WriteString(mutedStyle.Render("🔎 Filter: (none)"))
	} else {
		b.WriteString(promptStyle.Render("🔎 Filter: "))
		b.WriteString(m.filter)
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("󰍉 Type to filter | ↑/↓ (or j/k) move | Enter launch | Esc cancel"))
	b.WriteString("\n\n")

	if len(filtered) == 0 {
		b.WriteString(warnStyle.Render("⚠️ No projects match this filter."))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render("0 results"))
		b.WriteString("\n")
		return b.String()
	}

	end := m.offset + m.height
	if end > len(filtered) {
		end = len(filtered)
	}

	maxName := len("Project")
	for i := m.offset; i < end; i++ {
		if l := len(filtered[i]); l > maxName {
			maxName = l
		}
	}
	if maxName > 25 {
		maxName = 25
	}

	nameHeader := fmt.Sprintf("%-*s", maxName, "Project")
	b.WriteString(mutedStyle.Render("   " + nameHeader + "  Stack"))
	b.WriteString("\n")

	for i := m.offset; i < end; i++ {
		name := filtered[i]
		if len(name) > maxName {
			if maxName > 3 {
				name = name[:maxName-3] + "..."
			} else {
				name = name[:maxName]
			}
		}
		stack := "-"
		if s, ok := m.stackMap[filtered[i]]; ok && strings.TrimSpace(s) != "" {
			stack = s
		}
		line := fmt.Sprintf("  %-*s  %s", maxName, name, stack)
		if i == m.cursor {
			line = selectedStyle.Render("❯ " + fmt.Sprintf("%-*s  %s", maxName, name, stack))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	if len(filtered) > m.height {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(fmt.Sprintf("%d results | showing %d-%d", len(filtered), m.offset+1, end)))
	} else {
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(fmt.Sprintf("%d results", len(filtered))))
	}
	b.WriteString("\n")
	return b.String()
}

func (m *projectPickerModel) filteredProjects() []string {
	return filterProjects(m.projects, m.filter)
}

func (m *projectPickerModel) ensureCursorBounds() {
	filtered := m.filteredProjects()
	if len(filtered) == 0 {
		m.cursor = 0
		m.offset = 0
		return
	}

	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(filtered) {
		m.cursor = len(filtered) - 1
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.height {
		m.offset = m.cursor - m.height + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

// --- Projects directory prompt ---

// projectsDirPromptModel allows the user to type one or more project root directories.
type projectsDirPromptModel struct {
	homePath  string
	input     textinput.Model
	selected  string
	selecteds []string
	canceled  bool
	errorText string
}

func newProjectsDirPromptModel(homePath, initialValue string) *projectsDirPromptModel {
	ti := textinput.New()
	ti.Placeholder = "~/Projects"
	ti.Focus()
	ti.CharLimit = 2048
	ti.Width = 50
	ti.SetValue(strings.TrimSpace(initialValue))
	ti.CursorEnd()

	return &projectsDirPromptModel{
		homePath: homePath,
		input:    ti,
	}
}

func (m *projectsDirPromptModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m *projectsDirPromptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.canceled = true
			return m, tea.Quit
		case "enter":
			resolved, err := resolveProjectsPaths(m.homePath, m.input.Value())
			if err != nil {
				m.errorText = err.Error()
				return m, nil
			}
			m.selecteds = resolved
			m.selected = strings.Join(resolved, ", ")
			m.errorText = ""
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *projectsDirPromptModel) View() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("📁 Configure projects directory"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Type one or more paths (comma-separated), relative to ~ or absolute."))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("Esc cancels."))
	b.WriteString("\n\n")
	b.WriteString(promptStyle.Render("Path: "))
	b.WriteString(m.input.View())
	b.WriteString("\n")

	if m.errorText != "" {
		b.WriteString(warnStyle.Render(m.errorText))
		b.WriteString("\n")
	}

	if m.selected != "" {
		b.WriteString(successStyle.Render("✅ Using: " + m.selected))
		b.WriteString("\n")
	}

	if strings.TrimSpace(m.input.Value()) != "" {
		b.WriteString(mutedStyle.Render("Current: " + m.input.Value()))
		b.WriteString("\n")
	}

	return b.String()
}

// --- Project checklist (multi-selection for launchpads) ---

// projectChecklistModel allows toggling multiple projects on/off.
type projectChecklistModel struct {
	title    string
	projects []string
	checked  map[string]bool
	filter   string
	cursor   int
	offset   int
	height   int
	confirm  bool
	canceled bool
}

func newProjectChecklistModel(title string, projects []string, initiallyChecked map[string]bool) *projectChecklistModel {
	checked := make(map[string]bool, len(initiallyChecked))
	for k, v := range initiallyChecked {
		checked[k] = v
	}

	return &projectChecklistModel{
		title:    title,
		projects: projects,
		checked:  checked,
		height:   12,
	}
}

func (m *projectChecklistModel) Init() tea.Cmd {
	return nil
}

func (m *projectChecklistModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		available := msg.Height - 10
		if available < 5 {
			available = 5
		}
		if available > 24 {
			available = 24
		}
		m.height = available
		m.ensureCursorBounds()
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.canceled = true
			return m, tea.Quit
		case "enter":
			m.confirm = true
			return m, tea.Quit
		case "up", "k", "ctrl+p":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j", "ctrl+n":
			if m.cursor < len(m.filteredProjects())-1 {
				m.cursor++
			}
		case " ":
			filtered := m.filteredProjects()
			if len(filtered) > 0 {
				name := filtered[m.cursor]
				m.checked[name] = !m.checked[name]
			}
		case "backspace", "ctrl+h":
			runes := []rune(m.filter)
			if len(runes) > 0 {
				m.filter = string(runes[:len(runes)-1])
				m.cursor = 0
				m.offset = 0
			}
		default:
			runes := []rune(msg.String())
			if len(runes) == 1 && runes[0] >= 32 {
				m.filter += string(runes)
				m.cursor = 0
				m.offset = 0
			}
		}

		m.ensureCursorBounds()
		return m, nil
	}

	return m, nil
}

func (m *projectChecklistModel) View() string {
	filtered := m.filteredProjects()
	m.ensureCursorBounds()

	var b strings.Builder
	b.WriteString(titleStyle.Render(m.title))
	b.WriteString("\n")
	if m.filter == "" {
		b.WriteString(mutedStyle.Render("🔎 Filter: (none)"))
	} else {
		b.WriteString(promptStyle.Render("🔎 Filter: "))
		b.WriteString(m.filter)
	}
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render("󰍉 Type to filter | ↑/↓ move | Space toggle | Enter save | Esc cancel"))
	b.WriteString("\n\n")

	if len(filtered) == 0 {
		b.WriteString(warnStyle.Render("⚠️ No projects match this filter."))
		b.WriteString("\n")
		b.WriteString(mutedStyle.Render(fmt.Sprintf("selected: %d", m.selectedCount())))
		b.WriteString("\n")
		return b.String()
	}

	end := m.offset + m.height
	if end > len(filtered) {
		end = len(filtered)
	}

	for i := m.offset; i < end; i++ {
		name := filtered[i]
		box := "[ ]"
		if m.checked[name] {
			box = checkedStyle.Render("[x]")
		}
		prefix := "  "
		if i == m.cursor {
			prefix = selectedStyle.Render("❯ ")
		}
		b.WriteString(prefix)
		b.WriteString(fmt.Sprintf("%s %s\n", box, name))
	}

	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(fmt.Sprintf("selected: %d", m.selectedCount())))
	if len(filtered) > m.height {
		b.WriteString(" | ")
		b.WriteString(mutedStyle.Render(fmt.Sprintf("showing %d-%d of %d", m.offset+1, end, len(filtered))))
	}
	b.WriteString("\n")
	return b.String()
}

func (m *projectChecklistModel) filteredProjects() []string {
	return filterProjects(m.projects, m.filter)
}

func (m *projectChecklistModel) selectedCount() int {
	count := 0
	for _, checked := range m.checked {
		if checked {
			count++
		}
	}
	return count
}

func (m *projectChecklistModel) selectedProjects() []string {
	selected := make([]string, 0)
	for _, project := range m.projects {
		if m.checked[project] {
			selected = append(selected, project)
		}
	}
	return selected
}

func (m *projectChecklistModel) ensureCursorBounds() {
	filtered := m.filteredProjects()
	if len(filtered) == 0 {
		m.cursor = 0
		m.offset = 0
		return
	}

	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(filtered) {
		m.cursor = len(filtered) - 1
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.height {
		m.offset = m.cursor - m.height + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}
