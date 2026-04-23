package main

// BubbleTea UI models, styles, and interactive components.

import (
	"fmt"
	"path/filepath"
	"runtime"
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
	title    string
	projects []string
	stackMap map[string]string
	filter   string
	cursor   int
	offset   int
	height   int
	selected string
	canceled bool
}

func newProjectPickerModel(title string, projects []string, stackMap map[string]string, initialFilter string) *projectPickerModel {
	return &projectPickerModel{
		title:    title,
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

	title := m.title
	if title == "" {
		title = "Select a project"
	}

	b.WriteString("\n")
	b.WriteString(" " + titleStyle.Render("🚀 Liftoff"))
	b.WriteString("\n\n")
	b.WriteString(" " + promptStyle.Render(title))
	if len(m.projects) > 0 {
		b.WriteString("  " + mutedStyle.Render(fmt.Sprintf("(%d)", len(m.projects))))
	}
	b.WriteString("\n\n")

	b.WriteString(" ")
	if m.filter == "" {
		b.WriteString(mutedStyle.Render("🔎  type to filter..."))
	} else {
		b.WriteString(promptStyle.Render("🔎  ") + m.filter + mutedStyle.Render("▌"))
	}
	b.WriteString("\n\n")

	if len(filtered) == 0 {
		b.WriteString(" " + warnStyle.Render("⚠  no results for \""+m.filter+"\""))
		b.WriteString("\n\n")
		b.WriteString(" " + mutedStyle.Render("↑↓ navigate  ·  esc cancel"))
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
	if maxName > 30 {
		maxName = 30
	}

	sepLen := maxName + 20
	if sepLen < 40 {
		sepLen = 40
	}
	b.WriteString(mutedStyle.Render(fmt.Sprintf("  %-*s  %s", maxName, "Project", "Stack")))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n")

	for i := m.offset; i < end; i++ {
		name := filtered[i]
		displayName := name
		if len(displayName) > maxName {
			if maxName > 3 {
				displayName = displayName[:maxName-3] + "..."
			} else {
				displayName = displayName[:maxName]
			}
		}
		stack := "-"
		if s, ok := m.stackMap[filtered[i]]; ok && strings.TrimSpace(s) != "" {
			stack = s
		}
		if i == m.cursor {
			b.WriteString(selectedStyle.Render("❯ "))
			b.WriteString(lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%-*s", maxName, displayName)))
			b.WriteString("  ")
			b.WriteString(previewStyle.Render(stack))
		} else {
			b.WriteString("  ")
			b.WriteString(fmt.Sprintf("%-*s", maxName, displayName))
			b.WriteString("  ")
			b.WriteString(mutedStyle.Render(stack))
		}
		b.WriteString("\n")
	}

	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n\n")

	var statusText string
	if len(filtered) > m.height {
		statusText = fmt.Sprintf("%d–%d of %d  ·  ", m.offset+1, end, len(filtered))
	} else {
		statusText = fmt.Sprintf("%d results  ·  ", len(filtered))
	}
	b.WriteString(" " + mutedStyle.Render(statusText+"↑↓ navigate  ·  ⏎ launch  ·  esc cancel"))
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
	// Show a realistic example path for the current OS.
	if runtime.GOOS == "windows" {
		ti.Placeholder = filepath.Join(homePath, "Projects")
	} else {
		ti.Placeholder = "~/Projects"
	}
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

	const sepLen = 52

	b.WriteString("\n")
	b.WriteString(" " + titleStyle.Render("🚀 Liftoff"))
	b.WriteString("\n\n")
	b.WriteString(" " + promptStyle.Render("📁  Edit directories"))
	b.WriteString("\n\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n\n")

	if runtime.GOOS == "windows" {
		b.WriteString(" " + mutedStyle.Render("One or more full paths, comma-separated."))
	} else {
		b.WriteString(" " + mutedStyle.Render("One or more paths, comma-separated. Relative to ~ or absolute."))
	}
	b.WriteString("\n\n")
	b.WriteString(" " + promptStyle.Render("  "))
	b.WriteString(m.input.View())
	b.WriteString("\n\n")

	if m.errorText != "" {
		b.WriteString(" " + warnStyle.Render("⚠  "+m.errorText))
		b.WriteString("\n\n")
	}

	if m.selected != "" {
		b.WriteString(" " + successStyle.Render("✓  "+m.selected))
		b.WriteString("\n\n")
	}

	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n\n")
	b.WriteString(" " + mutedStyle.Render("⏎ confirm  ·  esc cancel"))
	b.WriteString("\n")

	return b.String()
}

// --- Project checklist (multi-selection for launchpads with order) ---

// projectChecklistModel allows toggling projects on/off and assigning a launch order.
// Left/right arrows change the batch number for the focused checked project.
type projectChecklistModel struct {
	title    string
	projects []string
	checked  map[string]bool
	order    map[string]int // batch order per checked project (1-based)
	filter   string
	cursor   int
	offset   int
	height   int
	confirm  bool
	canceled bool
}

func newProjectChecklistModel(title string, projects []string, initial []launchpadEntry) *projectChecklistModel {
	checked := make(map[string]bool, len(initial))
	order := make(map[string]int, len(initial))
	for _, e := range initial {
		checked[e.Name] = true
		o := e.Order
		if o < 1 {
			o = 1
		}
		order[e.Name] = o
	}
	return &projectChecklistModel{
		title:    title,
		projects: projects,
		checked:  checked,
		order:    order,
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
				if m.checked[name] && m.order[name] == 0 {
					m.order[name] = 1
				}
			}
		case "left":
			filtered := m.filteredProjects()
			if len(filtered) > 0 {
				name := filtered[m.cursor]
				if m.checked[name] && m.order[name] > 1 {
					m.order[name]--
				}
			}
		case "right":
			filtered := m.filteredProjects()
			if len(filtered) > 0 {
				name := filtered[m.cursor]
				if m.checked[name] {
					m.order[name]++
				}
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

	b.WriteString("\n")
	b.WriteString(" " + titleStyle.Render("🚀 Liftoff"))
	b.WriteString("\n\n")
	b.WriteString(" " + promptStyle.Render(m.title))
	if len(m.projects) > 0 {
		b.WriteString("  " + mutedStyle.Render(fmt.Sprintf("(%d)", len(m.projects))))
	}
	b.WriteString("\n\n")

	b.WriteString(" ")
	if m.filter == "" {
		b.WriteString(mutedStyle.Render("🔎  type to filter..."))
	} else {
		b.WriteString(promptStyle.Render("🔎  ") + m.filter + mutedStyle.Render("▌"))
	}
	b.WriteString("\n\n")

	const sepLen = 52
	if len(filtered) == 0 {
		b.WriteString(" " + warnStyle.Render("⚠  no results for \""+m.filter+"\""))
		b.WriteString("\n\n")
		b.WriteString(" " + mutedStyle.Render(fmt.Sprintf("%d selected  ·  ↑↓ navigate  ·  esc cancel", m.selectedCount())))
		b.WriteString("\n")
		return b.String()
	}

	end := m.offset + m.height
	if end > len(filtered) {
		end = len(filtered)
	}

	b.WriteString(mutedStyle.Render(fmt.Sprintf("  %-5s  Project", "Batch")))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n")

	for i := m.offset; i < end; i++ {
		name := filtered[i]
		if m.checked[name] {
			o := m.order[name]
			if o < 1 {
				o = 1
			}
			orderStr := fmt.Sprintf("[%d]", o)
			if i == m.cursor {
				b.WriteString(selectedStyle.Render("❯ "))
				b.WriteString(checkedStyle.Render(fmt.Sprintf("%-5s  ", orderStr)))
				b.WriteString(lipgloss.NewStyle().Bold(true).Render(name))
				b.WriteString(mutedStyle.Render("  ← →"))
			} else {
				b.WriteString("  ")
				b.WriteString(checkedStyle.Render(fmt.Sprintf("%-5s  ", orderStr)))
				b.WriteString(successStyle.Render(name))
			}
		} else {
			if i == m.cursor {
				b.WriteString(selectedStyle.Render("❯ "))
				b.WriteString(mutedStyle.Render("[ ]    "))
				b.WriteString(lipgloss.NewStyle().Bold(true).Render(name))
			} else {
				b.WriteString("  ")
				b.WriteString(mutedStyle.Render("[ ]    "))
				b.WriteString(name)
			}
		}
		b.WriteString("\n")
	}

	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n\n")

	var statusText string
	if len(filtered) > m.height {
		statusText = fmt.Sprintf("%d–%d of %d  ·  ", m.offset+1, end, len(filtered))
	} else {
		statusText = fmt.Sprintf("%d selected  ·  ", m.selectedCount())
	}
	b.WriteString(" " + mutedStyle.Render(statusText+"space select  ·  ←→ order  ·  ↑↓ navigate  ·  ⏎ save  ·  esc cancel"))
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

// selectedEntries returns the selected projects as ordered launchpadEntries,
// maintaining the original discovery order within each batch.
func (m *projectChecklistModel) selectedEntries() []launchpadEntry {
	entries := make([]launchpadEntry, 0)
	for _, project := range m.projects {
		if m.checked[project] {
			o := m.order[project]
			if o < 1 {
				o = 1
			}
			entries = append(entries, launchpadEntry{Name: project, Order: o})
		}
	}
	return entries
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

// --- Simple menu (fixed item list, no filter) ---

// simpleMenuModel is a minimal list picker for short fixed-option menus.
type simpleMenuModel struct {
	title    string
	items    []string
	cursor   int
	selected string
	canceled bool
}

func newSimpleMenuModel(title string, items []string) *simpleMenuModel {
	return &simpleMenuModel{title: title, items: items}
}

func (m *simpleMenuModel) Init() tea.Cmd { return nil }

func (m *simpleMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.canceled = true
			return m, tea.Quit
		case "enter":
			if len(m.items) > 0 {
				m.selected = m.items[m.cursor]
			}
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		}
	}
	return m, nil
}

func (m *simpleMenuModel) View() string {
	var b strings.Builder

	const sepLen = 52

	b.WriteString("\n")
	b.WriteString(" " + titleStyle.Render("🚀 Liftoff"))
	b.WriteString("\n\n")
	b.WriteString(" " + promptStyle.Render(m.title))
	b.WriteString("\n\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n\n")

	for i, item := range m.items {
		if i == m.cursor {
			b.WriteString("  " + selectedStyle.Render("❯ ") + lipgloss.NewStyle().Bold(true).Render(item))
		} else {
			b.WriteString("    " + item)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n\n")
	b.WriteString(" " + mutedStyle.Render("↑↓ navigate  ·  ⏎ select  ·  esc cancel"))
	b.WriteString("\n")

	return b.String()
}
