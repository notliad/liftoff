package main

// BubbleTea UI models, styles, and interactive components.

import (
	"fmt"
	"os"
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

	b.WriteString("\n")
	b.WriteString(" " + titleStyle.Render("🚀 Liftoff"))
	b.WriteString("\n\n")
	b.WriteString(" " + promptStyle.Render("Configure projects directory"))
	b.WriteString("\n\n")
	if runtime.GOOS == "windows" {
		b.WriteString(" " + mutedStyle.Render("One or more full paths, comma-separated."))
	} else {
		b.WriteString(" " + mutedStyle.Render("One or more paths, comma-separated. Relative to ~ or absolute."))
	}
	b.WriteString("\n\n")
	b.WriteString(" " + promptStyle.Render("📁  "))
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

	b.WriteString(" " + mutedStyle.Render("⏎ confirm  ·  esc cancel"))
	b.WriteString("\n")

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

	sepLen := 44
	b.WriteString(mutedStyle.Render("  [ ] Project"))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n")

	for i := m.offset; i < end; i++ {
		name := filtered[i]
		if m.checked[name] {
			if i == m.cursor {
				b.WriteString(selectedStyle.Render("❯ "))
				b.WriteString(checkedStyle.Render("[✓] "))
				b.WriteString(lipgloss.NewStyle().Bold(true).Render(name))
			} else {
				b.WriteString("  ")
				b.WriteString(checkedStyle.Render("[✓] "))
				b.WriteString(successStyle.Render(name))
			}
		} else {
			if i == m.cursor {
				b.WriteString(selectedStyle.Render("❯ "))
				b.WriteString(mutedStyle.Render("[ ] "))
				b.WriteString(lipgloss.NewStyle().Bold(true).Render(name))
			} else {
				b.WriteString("  ")
				b.WriteString(mutedStyle.Render("[ ] "))
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
	b.WriteString(" " + mutedStyle.Render(statusText+"space toggle  ·  ↑↓ navigate  ·  ⏎ save  ·  esc cancel"))
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

// --- Settings menu ---

// settingsMenuModel shows the top-level settings options.
type settingsMenuModel struct {
	cfg      config
	cursor   int
	selected string
	canceled bool
}

func newSettingsMenuModel(cfg config) *settingsMenuModel {
	return &settingsMenuModel{cfg: cfg}
}

func (m *settingsMenuModel) Init() tea.Cmd { return nil }

func (m *settingsMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	const maxItem = 1
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.canceled = true
			return m, tea.Quit
		case "up", "k", "ctrl+p":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j", "ctrl+n":
			if m.cursor < maxItem {
				m.cursor++
			}
		case "enter":
			switch m.cursor {
			case 0:
				m.selected = "dirs"
			case 1:
				m.selected = "launchpads"
			}
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *settingsMenuModel) View() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(" " + titleStyle.Render("🚀 Liftoff"))
	b.WriteString("\n\n")
	b.WriteString(" " + promptStyle.Render("⚙  Settings"))
	b.WriteString("\n\n")

	type item struct {
		icon   string
		label  string
		detail string
	}
	items := []item{
		{
			icon:   "📁",
			label:  "Project Directories",
			detail: settingsDirsDetail(m.cfg),
		},
		{
			icon:   "🧩",
			label:  "Launchpads",
			detail: settingsLaunchpadsDetail(m.cfg),
		},
	}

	sepLen := 52
	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n")

	for i, it := range items {
		isSelected := m.cursor == i
		prefix := "  "
		if isSelected {
			prefix = selectedStyle.Render("❯ ")
		}
		var label string
		if isSelected {
			label = lipgloss.NewStyle().Bold(true).Render(it.icon + "  " + it.label)
		} else {
			label = it.icon + "  " + it.label
		}
		b.WriteString("\n")
		b.WriteString(" " + prefix + label)
		b.WriteString("\n")
		b.WriteString("      " + mutedStyle.Render(it.detail))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n\n")
	b.WriteString(" " + mutedStyle.Render("↑↓ navigate  ·  ⏎ edit  ·  esc exit"))
	b.WriteString("\n")

	return b.String()
}

func settingsDirsDetail(cfg config) string {
	if len(cfg.Dirs) == 0 {
		return "not configured"
	}
	return strings.Join(cfg.Dirs, ", ")
}

func settingsLaunchpadsDetail(cfg config) string {
	n := len(cfg.Launchpads)
	switch n {
	case 0:
		return "none"
	case 1:
		return "1 launchpad"
	default:
		return fmt.Sprintf("%d launchpads", n)
	}
}

// showSettingsMenu opens the settings menu TUI and returns the chosen action key.
// Returns "" when the user exits without selecting.
func showSettingsMenu(cfg config, inFile, outFile *os.File) (string, error) {
	model := newSettingsMenuModel(cfg)
	p := tea.NewProgram(model, tea.WithInput(inFile), tea.WithOutput(outFile), tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		return "", err
	}
	m, ok := result.(*settingsMenuModel)
	if !ok || m == nil || m.canceled {
		return "", nil
	}
	return m.selected, nil
}

// --- Launchpad settings list ---

type launchpadListItem struct {
	name  string
	count int
	isNew bool
}

// launchpadListModel lists existing launchpads and allows edit/delete/create.
type launchpadListModel struct {
	cfg       config
	items     []launchpadListItem
	cursor    int
	// inline name-input for "new launchpad"
	naming    bool
	nameInput textinput.Model
	// result
	action   string
	selected string
	back     bool
}

func newLaunchpadListModel(cfg config) *launchpadListModel {
	names := sortedMapKeys(cfg.Launchpads)
	items := make([]launchpadListItem, 0, len(names)+1)
	for _, name := range names {
		items = append(items, launchpadListItem{name: name, count: len(cfg.Launchpads[name])})
	}
	items = append(items, launchpadListItem{isNew: true, name: "＋  New launchpad"})

	ti := textinput.New()
	ti.Placeholder = "launchpad-name"
	ti.CharLimit = 64
	ti.Width = 30

	return &launchpadListModel{
		cfg:       cfg,
		items:     items,
		nameInput: ti,
	}
}

func (m *launchpadListModel) Init() tea.Cmd { return nil }

func (m *launchpadListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.naming {
		return m.updateNaming(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.back = true
			return m, tea.Quit
		case "up", "k", "ctrl+p":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j", "ctrl+n":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter":
			item := m.items[m.cursor]
			if item.isNew {
				m.naming = true
				m.nameInput.Reset()
				m.nameInput.Focus()
				return m, textinput.Blink
			}
			m.action = "edit"
			m.selected = item.name
			return m, tea.Quit
		case "d":
			item := m.items[m.cursor]
			if !item.isNew {
				m.action = "delete"
				m.selected = item.name
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m *launchpadListModel) updateNaming(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.naming = false
			m.nameInput.Blur()
			return m, nil
		case "enter":
			name := strings.TrimSpace(m.nameInput.Value())
			if name == "" {
				m.naming = false
				return m, nil
			}
			m.action = "new"
			m.selected = name
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.nameInput, cmd = m.nameInput.Update(msg)
	return m, cmd
}

func (m *launchpadListModel) View() string {
	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(" " + titleStyle.Render("🚀 Liftoff"))
	b.WriteString("\n\n")
	b.WriteString(" " + promptStyle.Render("🧩  Launchpads"))
	b.WriteString("\n\n")

	sepLen := 44
	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n")

	for i, item := range m.items {
		isSelected := m.cursor == i
		prefix := "  "
		if isSelected {
			prefix = selectedStyle.Render("❯ ")
		}

		if item.isNew {
			if isSelected {
				b.WriteString(prefix + promptStyle.Render(item.name))
			} else {
				b.WriteString(prefix + mutedStyle.Render(item.name))
			}
		} else {
			countStr := fmt.Sprintf("  (%d)", item.count)
			if isSelected {
				b.WriteString(prefix + lipgloss.NewStyle().Bold(true).Render(item.name) + mutedStyle.Render(countStr))
			} else {
				b.WriteString(prefix + item.name + mutedStyle.Render(countStr))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n\n")

	if m.naming {
		b.WriteString(" " + promptStyle.Render("Name: "))
		b.WriteString(m.nameInput.View())
		b.WriteString("\n\n")
		b.WriteString(" " + mutedStyle.Render("⏎ create  ·  esc cancel"))
	} else {
		hint := "↑↓ navigate  ·  ⏎ edit  ·  d delete  ·  esc back"
		if len(m.items) == 1 {
			hint = "⏎ create first launchpad  ·  esc back"
		}
		b.WriteString(" " + mutedStyle.Render(hint))
	}
	b.WriteString("\n")

	return b.String()
}

// showLaunchpadSettings opens the launchpad list TUI and returns the chosen action.
func showLaunchpadSettings(cfg config, inFile, outFile *os.File) (action, name string, err error) {
	model := newLaunchpadListModel(cfg)
	p := tea.NewProgram(model, tea.WithInput(inFile), tea.WithOutput(outFile), tea.WithAltScreen())
	result, runErr := p.Run()
	if runErr != nil {
		return "", "", runErr
	}
	m, ok := result.(*launchpadListModel)
	if !ok || m == nil || m.back {
		return "back", "", nil
	}
	return m.action, m.selected, nil
}
