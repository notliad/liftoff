package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	toml "github.com/pelletier/go-toml/v2"
	"golang.org/x/term"
)

const version = "0.4.0"

type config struct {
	ProjectsDir  string              `json:"projectsDir,omitempty"`
	ProjectsDirs []string            `json:"projectsDirs,omitempty"`
	Launchpads   map[string][]string `json:"launchpads,omitempty"`
}

type projectEntry struct {
	Name    string
	Path    string
	RootDir string
	Display string
}

type packageJSON struct {
	Scripts      map[string]string `json:"scripts"`
	Dependencies map[string]string `json:"dependencies"`
	DevDepends   map[string]string `json:"devDependencies"`
	PeerDepends  map[string]string `json:"peerDependencies"`
	OptionalDeps map[string]string `json:"optionalDependencies"`
}

type pyprojectToml struct {
	Project struct {
		Dependencies         []string            `toml:"dependencies"`
		OptionalDependencies map[string][]string `toml:"optional-dependencies"`
	} `toml:"project"`
	Tool struct {
		Poetry struct {
			Dependencies map[string]any `toml:"dependencies"`
			Group        map[string]struct {
				Dependencies map[string]any `toml:"dependencies"`
			} `toml:"group"`
		} `toml:"poetry"`
	} `toml:"tool"`
}

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

type projectsDirPromptModel struct {
	homePath  string
	input     textinput.Model
	selected  string
	selecteds []string
	canceled  bool
	errorText string
}

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

type legacyLaunchpadsFile struct {
	Pads map[string][]string `json:"pads"`
}

func newProjectPickerModel(projects []string, stackMap map[string]string, initialFilter string) *projectPickerModel {
	return &projectPickerModel{
		projects: projects,
		stackMap: stackMap,
		filter:   strings.TrimSpace(initialFilter),
		height:   12,
	}
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

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, in io.Reader, out io.Writer, errOut io.Writer) error {
	fs := flag.NewFlagSet("lo", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	showHelp := fs.Bool("help", false, "show help")
	showHelpShort := fs.Bool("h", false, "show help")
	showVersion := fs.Bool("version", false, "show version")
	showVersionShort := fs.Bool("v", false, "show version")
	editConfig := fs.Bool("edit", false, "edit projects dir")
	editConfigShort := fs.Bool("e", false, "edit")
	printConfig := fs.Bool("print-config", false, "print config")
	printConfigShort := fs.Bool("c", false, "print config")
	padMode := fs.Bool("pad", false, "launchpad mode")
	padModeShort := fs.Bool("p", false, "launchpad mode")
	listMode := fs.Bool("list", false, "list mode")
	listModeShort := fs.Bool("l", false, "list mode")

	if err := fs.Parse(args); err != nil {
		writeUsage(errOut)
		return fmt.Errorf("❌ %w", err)
	}

	if *showHelp || *showHelpShort {
		writeUsage(out)
		return nil
	}
	if *showVersion || *showVersionShort {
		fmt.Fprintf(out, "lo %s\n", version)
		return nil
	}

	isEdit := *editConfig || *editConfigShort
	isPrintConfig := *printConfig || *printConfigShort
	isPadMode := *padMode || *padModeShort
	isListMode := *listMode || *listModeShort

	cfgPath, legacyPath, err := configPaths()
	if err != nil {
		return fmt.Errorf("❌ could not resolve config path: %w", err)
	}
	if isEdit && !isPadMode {
		existingCfg, _ := loadConfig(cfgPath, legacyPath)
		cfg, err := promptProjectsDir(existingCfg, in, out)
		if err != nil {
			return err
		}
		cfg.Launchpads = existingCfg.Launchpads
		if err := saveConfig(cfgPath, cfg); err != nil {
			return fmt.Errorf("❌ failed saving config: %w", err)
		}
		fmt.Fprintf(out, "✅ Saved config to %s\n", cfgPath)
		return nil
	}

	cfg, err := loadOrInitConfig(cfgPath, legacyPath, in, out)
	if err != nil {
		return err
	}
	if err := migrateLegacyLaunchpads(cfgPath, &cfg, out); err != nil {
		return err
	}

	if isPrintConfig {
		dirs := effectiveProjectDirs(cfg)
		fmt.Fprintf(out, "📁 %s\n", strings.Join(dirs, ", "))
		return nil
	}

	remaining := fs.Args()
	if isPadMode && isListMode {
		padName := ""
		if len(remaining) > 1 {
			return errors.New("❌ usage: lo --pad --list [name]")
		}
		if len(remaining) == 1 {
			padName = strings.TrimSpace(remaining[0])
		}
		return listLaunchpadsFlow(cfg, padName, out)
	}

	projectDirs := effectiveProjectDirs(cfg)
	fmt.Fprintf(out, "\n🚀 lo | Reading: %s\n", strings.Join(projectDirs, ", "))

	projectEntries, err := listProjects(projectDirs)
	if err != nil {
		return err
	}
	if len(projectEntries) == 0 {
		return fmt.Errorf("⚠️ no projects found in %s", strings.Join(projectDirs, ", "))
	}

	if isPadMode {
		padName := ""
		if len(remaining) > 1 {
			return errors.New("❌ usage: lo --pad [name] [--edit|--list]")
		}
		if len(remaining) == 1 {
			padName = strings.TrimSpace(remaining[0])
		}

		if isListMode {
			return listLaunchpadsFlow(cfg, padName, out)
		}

		if isEdit {
			return editLaunchpadFlow(cfgPath, &cfg, projectEntries, padName, in, out)
		}
		if padName == "" {
			return errors.New("❌ launchpad name required: lo --pad <name>")
		}
		return runLaunchpadFlow(cfgPath, &cfg, projectEntries, padName, in, out, errOut)
	}

	if isListMode {
		if len(remaining) > 0 {
			return errors.New("❌ usage: lo --list")
		}
		listProjectsFlow(projectEntries, out)
		return nil
	}

	if len(remaining) > 1 {
		return errors.New("❌ usage: lo [project-name]")
	}

	query := ""
	if len(remaining) == 1 {
		query = strings.TrimSpace(remaining[0])
	}

	project, err := chooseProject(projectEntries, query, in, out)
	if err != nil {
		return err
	}

	return launchProject(project.Path, project.Name, out, errOut)
}

func writeUsage(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "Usage:")

	fmt.Fprintln(tw, "  lo [project-name]\tlaunch a project")
	fmt.Fprintln(tw, "  lo --list, -l\tlist projects")
	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "  lo --pad [name]\tcreate/run a launchpad*")
	fmt.Fprintln(tw, "  lo --pad --list [name]\tlist launchpads")
	fmt.Fprintln(tw, "  lo --pad --edit [name]\tedit a launchpad")
	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "  lo --edit, -e\tchange projects directories")
	fmt.Fprintln(tw, "  lo --print-config, -c\t display current directories")
	fmt.Fprintln(tw, "  lo --version, -v\tdisplay version")
	fmt.Fprintln(tw, "  lo --help, -h\tshow this :)")
	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "\n* A launchpad is a named group of projects that can be started together.")

	tw.Flush()
	fmt.Fprintln(w, "")
}

func configPaths() (string, string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(base, "lo")
	return filepath.Join(dir, "config.json"), filepath.Join(dir, "config"), nil
}

func legacyLaunchpadsPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "lo", "launchpads.json"), nil
}

func migrateLegacyLaunchpads(cfgPath string, cfg *config, out io.Writer) error {
	legacyPath, err := legacyLaunchpadsPath()
	if err != nil {
		return fmt.Errorf("❌ could not resolve legacy launchpads path: %w", err)
	}
	if !fileExists(legacyPath) {
		return nil
	}

	raw, err := os.ReadFile(legacyPath)
	if err != nil {
		return nil
	}
	if strings.TrimSpace(string(raw)) == "" {
		return nil
	}

	var legacy legacyLaunchpadsFile
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return nil
	}
	if len(legacy.Pads) == 0 {
		return nil
	}

	if cfg.Launchpads == nil {
		cfg.Launchpads = make(map[string][]string)
	}
	changed := false
	for name, projects := range legacy.Pads {
		if _, exists := cfg.Launchpads[name]; exists {
			continue
		}
		cfg.Launchpads[name] = projects
		changed = true
	}
	if !changed {
		return nil
	}

	if err := saveConfig(cfgPath, *cfg); err != nil {
		return fmt.Errorf("❌ could not migrate legacy launchpads: %w", err)
	}
	fmt.Fprintln(out, "✅ Migrated legacy launchpads into config.json")
	return nil
}

func loadOrInitConfig(cfgPath, legacyPath string, in io.Reader, out io.Writer) (config, error) {
	cfg, err := loadConfig(cfgPath, legacyPath)
	if err == nil {
		dirs := effectiveProjectDirs(cfg)
		if len(dirs) > 0 && allDirsExist(dirs) {
			return cfg, nil
		}
		fmt.Fprintln(out, "⚠️ Invalid config. Please choose valid directories.")
	}

	cfg, err = promptProjectsDir(cfg, in, out)
	if err != nil {
		return config{}, err
	}
	if err := saveConfig(cfgPath, cfg); err != nil {
		return config{}, fmt.Errorf("❌ failed saving config: %w", err)
	}
	fmt.Fprintf(out, "✅ Saved config to %s\n", cfgPath)
	return cfg, nil
}

func loadConfig(cfgPath, legacyPath string) (config, error) {
	var cfg config

	if data, err := os.ReadFile(cfgPath); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return config{}, fmt.Errorf("invalid config json: %w", err)
		}
		cfg.ProjectsDir = strings.TrimSpace(cfg.ProjectsDir)
		cfg.ProjectsDirs = normalizeProjectDirs(cfg.ProjectsDirs)
		if cfg.Launchpads == nil {
			cfg.Launchpads = make(map[string][]string)
		}
		return cfg, nil
	}

	if data, err := os.ReadFile(legacyPath); err == nil {
		line := strings.TrimSpace(string(data))
		const key = "PROJECTS_DIR="
		if !strings.HasPrefix(line, key) {
			return config{}, errors.New("legacy config missing PROJECTS_DIR")
		}
		v := strings.TrimSpace(strings.TrimPrefix(line, key))
		v = strings.Trim(v, "\"'")
		if v == "" {
			return config{}, errors.New("legacy config has empty PROJECTS_DIR")
		}
		cfg.ProjectsDir = v
		cfg.ProjectsDirs = []string{v}
		cfg.Launchpads = make(map[string][]string)
		return cfg, nil
	}

	return config{}, errors.New("config not found")
}

func saveConfig(path string, cfg config) error {
	dirs := effectiveProjectDirs(cfg)
	if len(dirs) == 0 {
		return errors.New("projects dirs cannot be empty")
	}
	cfg.ProjectsDirs = dirs
	cfg.ProjectsDir = dirs[0]
	if cfg.Launchpads == nil {
		cfg.Launchpads = make(map[string][]string)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func promptProjectsDir(current config, in io.Reader, out io.Writer) (config, error) {
	inFile, inOk := in.(*os.File)
	outFile, outOk := out.(*os.File)
	if inOk && outOk && term.IsTerminal(int(inFile.Fd())) && term.IsTerminal(int(outFile.Fd())) {
		return promptProjectsDirWithBubbleTea(current, inFile, outFile)
	}

	return promptProjectsDirLine(current, in, out)
}

func promptProjectsDirWithBubbleTea(current config, inFile, outFile *os.File) (config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return config{}, fmt.Errorf("❌ could not resolve home directory: %w", err)
	}
	initialValue := strings.Join(effectiveProjectDirs(current), ", ")

	model := newProjectsDirPromptModel(home, initialValue)
	p := tea.NewProgram(
		model,
		tea.WithInput(inFile),
		tea.WithOutput(outFile),
	)

	result, err := p.Run()
	if err != nil {
		return config{}, fmt.Errorf("❌ failed reading input: %w", err)
	}

	finalModel, ok := result.(*projectsDirPromptModel)
	if !ok || finalModel == nil || finalModel.canceled {
		return config{}, errors.New("canceled")
	}
	if len(finalModel.selecteds) == 0 {
		return config{}, errors.New("❌ invalid directory")
	}

	return config{ProjectsDir: finalModel.selecteds[0], ProjectsDirs: finalModel.selecteds, Launchpads: current.Launchpads}, nil
}

func promptProjectsDirLine(current config, in io.Reader, out io.Writer) (config, error) {
	reader := bufio.NewReader(in)
	fmt.Fprintln(out, "📁 Enter your projects directories (comma-separated, relative to ~):")
	currentDirs := strings.Join(effectiveProjectDirs(current), ", ")
	if currentDirs != "" {
		fmt.Fprintf(out, "Current: %s\n", currentDirs)
	}
	fmt.Fprint(out, "New value: ")

	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return config{}, fmt.Errorf("❌ failed reading input: %w", err)
	}
	inputPath := strings.TrimSpace(line)
	if inputPath == "" && currentDirs != "" {
		inputPath = currentDirs
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return config{}, fmt.Errorf("❌ could not resolve home directory: %w", err)
	}
	projectsPaths, err := resolveProjectsPaths(home, inputPath)
	if err != nil {
		return config{}, err
	}

	return config{ProjectsDir: projectsPaths[0], ProjectsDirs: projectsPaths, Launchpads: current.Launchpads}, nil
}

func resolveProjectsPath(home, inputPath string) (string, error) {
	paths, err := resolveProjectsPaths(home, inputPath)
	if err != nil {
		return "", err
	}
	return paths[0], nil
}

func resolveProjectsPaths(home, inputPath string) ([]string, error) {
	rawParts := strings.Split(strings.TrimSpace(inputPath), ",")
	if len(rawParts) == 0 {
		rawParts = []string{""}
	}

	paths := make([]string, 0, len(rawParts))
	seen := make(map[string]bool, len(rawParts))
	for _, raw := range rawParts {
		part := strings.TrimSpace(raw)
		var projectsPath string
		switch {
		case part == "":
			projectsPath = home
		case strings.HasPrefix(part, "~/"):
			projectsPath = filepath.Join(home, strings.TrimPrefix(part, "~/"))
		case filepath.IsAbs(part):
			projectsPath = part
		default:
			projectsPath = filepath.Join(home, part)
		}

		projectsPath = filepath.Clean(projectsPath)
		st, err := os.Stat(projectsPath)
		if err != nil || !st.IsDir() {
			return nil, fmt.Errorf("❌ invalid directory: %s", projectsPath)
		}

		if !seen[projectsPath] {
			seen[projectsPath] = true
			paths = append(paths, projectsPath)
		}
	}

	if len(paths) == 0 {
		return nil, errors.New("❌ at least one valid directory is required")
	}

	return paths, nil
}

func normalizeProjectDirs(dirs []string) []string {
	clean := make([]string, 0, len(dirs))
	seen := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		trimmed := strings.TrimSpace(dir)
		if trimmed == "" {
			continue
		}
		trimmed = filepath.Clean(trimmed)
		if seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		clean = append(clean, trimmed)
	}
	return clean
}

func effectiveProjectDirs(cfg config) []string {
	dirs := normalizeProjectDirs(cfg.ProjectsDirs)
	if len(dirs) > 0 {
		return dirs
	}
	if strings.TrimSpace(cfg.ProjectsDir) == "" {
		return nil
	}
	return normalizeProjectDirs([]string{cfg.ProjectsDir})
}

func allDirsExist(dirs []string) bool {
	for _, dir := range dirs {
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			return false
		}
	}
	return true
}

func listProjects(roots []string) ([]projectEntry, error) {
	projects := make([]projectEntry, 0)
	nameCounts := make(map[string]int)

	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			return nil, fmt.Errorf("❌ failed listing projects in %s: %w", root, err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			projectPath := filepath.Join(root, entry.Name())
			if !isRunnableProjectDir(projectPath) {
				continue
			}
			p := projectEntry{
				Name:    entry.Name(),
				Path:    projectPath,
				RootDir: root,
			}
			projects = append(projects, p)
			nameCounts[p.Name]++
		}
	}

	for i := range projects {
		p := projects[i]
		display := p.Name
		if nameCounts[p.Name] > 1 {
			display = fmt.Sprintf("%s (%s)", p.Name, p.RootDir)
		}
		projects[i].Display = display
	}

	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Name == projects[j].Name {
			return projects[i].RootDir < projects[j].RootDir
		}
		return projects[i].Name < projects[j].Name
	})

	return projects, nil
}

func displayNames(entries []projectEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Display)
	}
	return names
}

func findProjectByDisplay(entries []projectEntry, display string) (projectEntry, bool) {
	for _, entry := range entries {
		if entry.Display == display {
			return entry, true
		}
	}
	return projectEntry{}, false
}

func chooseProject(entries []projectEntry, query string, in io.Reader, out io.Writer) (projectEntry, error) {
	if query != "" {
		for _, entry := range entries {
			if entry.Display == query {
				return entry, nil
			}
		}
		matchingByName := make([]projectEntry, 0)
		for _, entry := range entries {
			if entry.Name == query {
				matchingByName = append(matchingByName, entry)
			}
		}
		if len(matchingByName) == 1 {
			return matchingByName[0], nil
		}
	}

	projects := displayNames(entries)
	stackMap := buildProjectStackMap(entries)
	selected, err := selectProject(projects, stackMap, query, in, out)
	if err != nil {
		if query == "" {
			return projectEntry{}, errors.New("⚠️ no project selected")
		}
		return projectEntry{}, fmt.Errorf("❌ project not found: %s", query)
	}

	entry, ok := findProjectByDisplay(entries, selected)
	if !ok {
		return projectEntry{}, fmt.Errorf("❌ selected project not found: %s", selected)
	}

	return entry, nil
}

func selectProject(projects []string, stackMap map[string]string, query string, in io.Reader, out io.Writer) (string, error) {
	return selectWithBubbleTea(projects, stackMap, query, in, out)
}

func selectWithBubbleTea(projects []string, stackMap map[string]string, initialQuery string, in io.Reader, out io.Writer) (string, error) {
	inFile, inOk := in.(*os.File)
	outFile, outOk := out.(*os.File)
	if !inOk || !outOk || !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		return selectWithLineMenu(projects, initialQuery, in, out)
	}

	model := newProjectPickerModel(projects, stackMap, initialQuery)
	p := tea.NewProgram(
		model,
		tea.WithInput(inFile),
		tea.WithOutput(outFile),
		tea.WithAltScreen(),
	)

	result, err := p.Run()
	if err != nil {
		return "", err
	}

	finalModel, ok := result.(*projectPickerModel)
	if !ok || finalModel == nil {
		return "", errors.New("canceled")
	}
	if finalModel.canceled || finalModel.selected == "" {
		return "", errors.New("canceled")
	}

	return finalModel.selected, nil
}

func selectWithLineMenu(projects []string, initialQuery string, in io.Reader, out io.Writer) (string, error) {
	reader := bufio.NewReader(in)
	filter := strings.TrimSpace(initialQuery)

	for {
		filtered := filterProjects(projects, filter)
		fmt.Fprintln(out, "\n🚀 Liftoff")
		fmt.Fprintln(out, "\n📚 Select a project")
		if filter == "" {
			fmt.Fprintln(out, "🔎 Filter: (none)")
		} else {
			fmt.Fprintf(out, "🔎 Filter: %s\n", filter)
		}

		if len(filtered) == 0 {
			fmt.Fprintln(out, "⚠️ No projects match this filter.")
		} else {
			maxItems := len(filtered)
			if maxItems > 30 {
				maxItems = 30
			}

			for i := 0; i < maxItems; i++ {
				fmt.Fprintf(out, "  %2d) %s\n", i+1, filtered[i])
			}

			if len(filtered) > maxItems {
				fmt.Fprintf(out, "  ... and %d more\n", len(filtered)-maxItems)
			}
		}

		fmt.Fprint(out, "\n󰍉 Type filter, number to select, or 'q' to cancel: ")
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}

		input := strings.TrimSpace(line)
		if input == "" && errors.Is(err, io.EOF) {
			return "", errors.New("canceled")
		}

		if strings.EqualFold(input, "q") || strings.EqualFold(input, "quit") {
			return "", errors.New("canceled")
		}

		if n, convErr := strconv.Atoi(input); convErr == nil {
			if len(filtered) == 0 {
				fmt.Fprintln(out, "⚠️ No project to select. Change the filter.")
				continue
			}
			if n < 1 || n > len(filtered) {
				fmt.Fprintln(out, "⚠️ Invalid number. Choose one from the list.")
				continue
			}
			return filtered[n-1], nil
		}

		filter = input
	}
}

func runLaunchpadFlow(cfgPath string, cfg *config, entries []projectEntry, name string, in io.Reader, out io.Writer, errOut io.Writer) error {
	if cfg.Launchpads == nil {
		cfg.Launchpads = make(map[string][]string)
	}
	projects := displayNames(entries)

	selectedProjects, exists := cfg.Launchpads[name]
	if !exists {
		fmt.Fprintf(out, "⚠️ Launchpad '%s' not found. Let's create it.\n", name)
		selectedProjects, err := selectProjectsForLaunchpad(projects, nil, fmt.Sprintf("🧩 Create launchpad: %s", name), in, out)
		if err != nil {
			return err
		}
		if len(selectedProjects) == 0 {
			return errors.New("⚠️ launchpad must contain at least one project")
		}
		cfg.Launchpads[name] = selectedProjects
		if err := saveConfig(cfgPath, *cfg); err != nil {
			return fmt.Errorf("❌ could not save launchpad: %w", err)
		}
		fmt.Fprintf(out, "✅ Launchpad '%s' saved with %d projects\n", name, len(selectedProjects))
		fmt.Fprintf(out, "💡 Run 'lo --pad %s' again to start these projects\n", name)
		return nil
	}

	resolvedProjects := resolveLaunchpadProjects(selectedProjects, entries)
	if len(resolvedProjects) == 0 {
		return fmt.Errorf("⚠️ launchpad '%s' has no resolvable projects", name)
	}

	return launchProjectsParallel(name, resolvedProjects, out, errOut)
}

func listProjectsFlow(projects []projectEntry, out io.Writer) {
	fmt.Fprintf(out, "📚 Projects (%d):\n", len(projects))
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tSTACK\tROOT")
	for _, project := range projects {
		stack := previewProjectStack(project.Path)
		if strings.TrimSpace(stack) == "" {
			stack = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", project.Display, stack, project.RootDir)
	}
	tw.Flush()
}

func listLaunchpadsFlow(cfg config, launchpadName string, out io.Writer) error {
	if len(cfg.Launchpads) == 0 {
		fmt.Fprintln(out, "📦 No launchpads found")
		return nil
	}

	if strings.TrimSpace(launchpadName) != "" {
		projects, ok := cfg.Launchpads[launchpadName]
		if !ok {
			return fmt.Errorf("❌ launchpad not found: %s", launchpadName)
		}
		fmt.Fprintf(out, "🧩 Launchpad: %s (%d projects)\n", launchpadName, len(projects))
		for _, project := range projects {
			fmt.Fprintf(out, "- %s\n", project)
		}
		return nil
	}

	names := sortedMapKeys(cfg.Launchpads)
	fmt.Fprintf(out, "🧩 Launchpads (%d):\n", len(names))
	for _, name := range names {
		projects := cfg.Launchpads[name]
		fmt.Fprintf(out, "- %s (%d)\n", name, len(projects))
		for _, project := range projects {
			fmt.Fprintf(out, "  - %s\n", project)
		}
	}
	return nil
}

func editLaunchpadFlow(cfgPath string, cfg *config, entries []projectEntry, name string, in io.Reader, out io.Writer) error {
	if cfg.Launchpads == nil {
		cfg.Launchpads = make(map[string][]string)
	}
	if len(cfg.Launchpads) == 0 {
		return errors.New("⚠️ no launchpads found to edit")
	}

	if strings.TrimSpace(name) == "" {
		names := sortedMapKeys(cfg.Launchpads)
		picked, pickErr := selectProject(names, nil, "", in, out)
		if pickErr != nil {
			return pickErr
		}
		name = picked
	}

	current, ok := cfg.Launchpads[name]
	if !ok {
		return fmt.Errorf("❌ launchpad not found: %s", name)
	}

	projects := displayNames(entries)
	updated, err := selectProjectsForLaunchpad(projects, current, fmt.Sprintf("🛠 Edit launchpad: %s", name), in, out)
	if err != nil {
		return err
	}

	if len(updated) == 0 {
		delete(cfg.Launchpads, name)
		if err := saveConfig(cfgPath, *cfg); err != nil {
			return fmt.Errorf("❌ could not save launchpads: %w", err)
		}
		fmt.Fprintf(out, "✅ Launchpad '%s' removed (empty selection)\n", name)
		return nil
	}

	cfg.Launchpads[name] = updated
	if err := saveConfig(cfgPath, *cfg); err != nil {
		return fmt.Errorf("❌ could not save launchpads: %w", err)
	}
	fmt.Fprintf(out, "✅ Launchpad '%s' updated with %d projects\n", name, len(updated))
	return nil
}

func selectProjectsForLaunchpad(projects []string, selected []string, title string, in io.Reader, out io.Writer) ([]string, error) {
	selectedSet := make(map[string]bool, len(selected))
	for _, name := range selected {
		selectedSet[name] = true
	}

	inFile, inOk := in.(*os.File)
	outFile, outOk := out.(*os.File)
	if !inOk || !outOk || !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		return selectProjectsForLaunchpadLine(projects, selectedSet, in, out)
	}

	model := newProjectChecklistModel(title, projects, selectedSet)
	p := tea.NewProgram(
		model,
		tea.WithInput(inFile),
		tea.WithOutput(outFile),
		tea.WithAltScreen(),
	)

	result, err := p.Run()
	if err != nil {
		return nil, err
	}

	finalModel, ok := result.(*projectChecklistModel)
	if !ok || finalModel == nil || finalModel.canceled {
		return nil, errors.New("canceled")
	}
	if !finalModel.confirm {
		return nil, errors.New("canceled")
	}

	return finalModel.selectedProjects(), nil
}

func selectProjectsForLaunchpadLine(projects []string, selectedSet map[string]bool, in io.Reader, out io.Writer) ([]string, error) {
	reader := bufio.NewReader(in)
	fmt.Fprintln(out, "📚 Select projects (comma-separated names), empty to keep current:")
	for i, p := range projects {
		marker := "[ ]"
		if selectedSet[p] {
			marker = "[x]"
		}
		fmt.Fprintf(out, "  %2d) %s %s\n", i+1, marker, p)
	}
	fmt.Fprint(out, "\n󰍉 Projects: ")
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		outSelected := make([]string, 0)
		for _, p := range projects {
			if selectedSet[p] {
				outSelected = append(outSelected, p)
			}
		}
		return outSelected, nil
	}

	requested := make(map[string]bool)
	for _, chunk := range strings.Split(line, ",") {
		name := strings.TrimSpace(chunk)
		if name != "" {
			requested[name] = true
		}
	}

	outSelected := make([]string, 0)
	for _, p := range projects {
		if requested[p] {
			outSelected = append(outSelected, p)
		}
	}
	return outSelected, nil
}

func sortedMapKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func resolveLaunchpadProjects(saved []string, entries []projectEntry) []projectEntry {
	resolved := make([]projectEntry, 0, len(saved))
	used := make(map[string]bool)

	for _, item := range saved {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		if exact, ok := findProjectByDisplay(entries, item); ok {
			if !used[exact.Path] {
				resolved = append(resolved, exact)
				used[exact.Path] = true
			}
			continue
		}

		for _, entry := range entries {
			if entry.Name == item && !used[entry.Path] {
				resolved = append(resolved, entry)
				used[entry.Path] = true
				break
			}
		}
	}

	return resolved
}

func launchProjectsParallel(launchpadName string, projects []projectEntry, out io.Writer, errOut io.Writer) error {
	if len(projects) == 0 {
		return errors.New("⚠️ launchpad has no projects")
	}

	fmt.Fprintf(out, "🚀 Launchpad '%s' starting %d projects in parallel\n", launchpadName, len(projects))

	var wg sync.WaitGroup
	var mu sync.Mutex
	errs := make([]error, 0)

	for _, project := range projects {
		project := project
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !fileExists(project.Path) {
				mu.Lock()
				errs = append(errs, fmt.Errorf("project not found: %s", project.Display))
				mu.Unlock()
				return
			}
			if err := launchProject(project.Path, project.Name, out, errOut); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", project.Display, err))
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	if len(errs) == 0 {
		return nil
	}
	return fmt.Errorf("❌ launchpad completed with %d errors (first: %v)", len(errs), errs[0])
}

func launchProject(projectPath, projectName string, out io.Writer, errOut io.Writer) error {
	target, installCmd, runCmd, err := detectProjectRunner(projectPath)
	if err != nil {
		return err
	}

	if len(installCmd) > 0 {
		fmt.Fprintf(out, "📦 Installing dependencies for %s...\n", projectName)
		if err := runCommandInDir(projectPath, installCmd, out, errOut); err != nil {
			return fmt.Errorf("dependency installation failed (%s): %w", strings.Join(installCmd, " "), err)
		}
	}

	fmt.Fprintf(out, "🚀 Launching %s with %s\n", projectName, target)
	if err := launchCrossPlatform(projectPath, runCmd, out, errOut); err != nil {
		return err
	}
	return nil
}

func filterProjects(projects []string, filter string) []string {
	needle := strings.ToLower(strings.TrimSpace(filter))
	if needle == "" {
		copyProjects := make([]string, len(projects))
		copy(copyProjects, projects)
		return copyProjects
	}

	filtered := make([]string, 0, len(projects))
	for _, p := range projects {
		if strings.Contains(strings.ToLower(p), needle) {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

func isRunnableProjectDir(projectPath string) bool {
	entries, err := os.ReadDir(projectPath)
	if err != nil || len(entries) == 0 {
		return false
	}

	packageJSONPath := filepath.Join(projectPath, "package.json")
	if fileExists(packageJSONPath) {
		pkg, err := loadPackageJSON(packageJSONPath)
		if err != nil {
			return false
		}
		return detectScript(pkg) != ""
	}

	if fileExists(filepath.Join(projectPath, "Cargo.toml")) {
		return true
	}

	if isGoProject(projectPath) || isJavaProject(projectPath) {
		return true
	}

	if isPythonProject(projectPath) {
		_, err := detectPythonEntrypoint(projectPath)
		return err == nil
	}

	return false
}

func buildProjectStackMap(projects []projectEntry) map[string]string {
	stackMap := make(map[string]string, len(projects))
	for _, project := range projects {
		stackMap[project.Display] = previewProjectStack(project.Path)
	}
	return stackMap
}

func previewProjectStack(projectPath string) string {
	if fileExists(filepath.Join(projectPath, "package.json")) {
		return previewNodeStack(projectPath)
	}

	if fileExists(filepath.Join(projectPath, "Cargo.toml")) {
		framework := detectRustFramework(projectPath)
		return fmt.Sprintf("🦀 rust / %s", framework)
	}

	if isGoProject(projectPath) {
		framework := detectGoFramework(projectPath)
		entry := detectGoEntryHint(projectPath)
		return fmt.Sprintf("🐹 go / %s / %s", framework, entry)
	}

	if isJavaProject(projectPath) {
		framework := detectJavaFramework(projectPath)
		runner := detectJavaRunnerHint(projectPath)
		return fmt.Sprintf("☕ java / %s / %s", framework, runner)
	}

	if isPythonProject(projectPath) {
		framework := detectPythonFramework(projectPath)
		runner := detectPythonRunnerHint(projectPath)
		return fmt.Sprintf("🐍 python / %s / %s", framework, runner)
	}

	return ""
}

func previewNodeStack(projectPath string) string {
	pkgPath := filepath.Join(projectPath, "package.json")
	pkg, err := loadPackageJSON(pkgPath)
	if err != nil {
		return " node / invalid package.json"
	}

	script := detectScript(pkg)
	if script == "" {
		script = "(no dev/start)"
	}

	pm := detectPackageManagerFromLockfile(projectPath)
	framework := detectNodeFramework(pkg)
	return fmt.Sprintf(" %s / %s / %s", pm, framework, script)
}

func detectNodeFramework(pkg packageJSON) string {
	deps := make(map[string]struct{})
	for k := range pkg.Dependencies {
		deps[strings.ToLower(k)] = struct{}{}
	}
	for k := range pkg.DevDepends {
		deps[strings.ToLower(k)] = struct{}{}
	}
	for k := range pkg.PeerDepends {
		deps[strings.ToLower(k)] = struct{}{}
	}
	for k := range pkg.OptionalDeps {
		deps[strings.ToLower(k)] = struct{}{}
	}

	has := func(name string) bool {
		_, ok := deps[name]
		return ok
	}

	switch {
	case has("next"):
		return "next"
	case has("nuxt") || has("nuxt3"):
		return "nuxt"
	case has("@sveltejs/kit"):
		return "sveltekit"
	case has("astro"):
		return "astro"
	case has("@nestjs/core"):
		return "nestjs"
	case has("remix") || has("@remix-run/react"):
		return "remix"
	case has("vite") && has("react"):
		return "vite+react"
	case has("vite") && has("vue"):
		return "vite+vue"
	case has("vite"):
		return "vite"
	case has("react"):
		return "react"
	case has("vue"):
		return "vue"
	case has("@angular/core") || has("angular"):
		return "angular"
	case has("express"):
		return "express"
	case has("fastify"):
		return "fastify"
	case has("hono"):
		return "hono"
	default:
		return "node"
	}
}

func detectRustFramework(projectPath string) string {
	content := strings.ToLower(readFileOrEmpty(filepath.Join(projectPath, "Cargo.toml")))
	switch {
	case strings.Contains(content, "axum"):
		return "axum"
	case strings.Contains(content, "actix-web"):
		return "actix"
	case strings.Contains(content, "rocket"):
		return "rocket"
	case strings.Contains(content, "tauri"):
		return "tauri"
	case strings.Contains(content, "bevy"):
		return "bevy"
	default:
		return "cargo"
	}
}

func detectPythonFramework(projectPath string) string {
	if fileExists(filepath.Join(projectPath, "manage.py")) {
		return "django"
	}

	deps := make(map[string]struct{})
	for _, dep := range parsePythonDependencies(projectPath) {
		deps[strings.ToLower(dep)] = struct{}{}
	}

	has := func(name string) bool {
		_, ok := deps[name]
		return ok
	}

	switch {
	case has("fastapi"):
		return "fastapi"
	case has("flask"):
		return "flask"
	case has("django"):
		return "django"
	case has("streamlit"):
		return "streamlit"
	case has("gradio"):
		return "gradio"
	default:
		return "python"
	}
}

func parsePythonDependencies(projectPath string) []string {
	deps := make(map[string]struct{})

	pyprojectPath := filepath.Join(projectPath, "pyproject.toml")
	if fileExists(pyprojectPath) {
		if parsed, err := loadPyproject(pyprojectPath); err == nil {
			for _, dep := range parsed {
				if dep != "" {
					deps[strings.ToLower(dep)] = struct{}{}
				}
			}
		}
	}

	reqPath := filepath.Join(projectPath, "requirements.txt")
	for _, dep := range parseRequirements(reqPath) {
		deps[strings.ToLower(dep)] = struct{}{}
	}

	out := make([]string, 0, len(deps))
	for dep := range deps {
		out = append(out, dep)
	}
	sort.Strings(out)
	return out
}

func loadPyproject(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg pyprojectToml
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	deps := make(map[string]struct{})
	for _, raw := range cfg.Project.Dependencies {
		name := parsePythonDependencyName(raw)
		if name != "" {
			deps[name] = struct{}{}
		}
	}
	for _, groupDeps := range cfg.Project.OptionalDependencies {
		for _, raw := range groupDeps {
			name := parsePythonDependencyName(raw)
			if name != "" {
				deps[name] = struct{}{}
			}
		}
	}
	for name := range cfg.Tool.Poetry.Dependencies {
		if strings.EqualFold(name, "python") {
			continue
		}
		deps[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
	}
	for _, group := range cfg.Tool.Poetry.Group {
		for name := range group.Dependencies {
			if strings.EqualFold(name, "python") {
				continue
			}
			deps[strings.ToLower(strings.TrimSpace(name))] = struct{}{}
		}
	}

	out := make([]string, 0, len(deps))
	for name := range deps {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

func parseRequirements(path string) []string {
	content := readFileOrEmpty(path)
	if content == "" {
		return nil
	}

	deps := make([]string, 0)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := parsePythonDependencyName(line)
		if name != "" {
			deps = append(deps, name)
		}
	}
	return deps
}

func parsePythonDependencyName(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if idx := strings.Index(raw, ";"); idx >= 0 {
		raw = strings.TrimSpace(raw[:idx])
	}

	for _, sep := range []string{"==", ">=", "<=", "~=", "!=", ">", "<", "[", " "} {
		if idx := strings.Index(raw, sep); idx >= 0 {
			raw = strings.TrimSpace(raw[:idx])
		}
	}

	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return strings.ToLower(raw)
}

func detectPythonRunnerHint(projectPath string) string {
	if fileExists(filepath.Join(projectPath, "manage.py")) {
		return "manage.py runserver"
	}
	if fileExists(filepath.Join(projectPath, "main.py")) {
		return "main.py"
	}
	if fileExists(filepath.Join(projectPath, "app.py")) {
		return "app.py"
	}
	if fileExists(filepath.Join(projectPath, "uv.lock")) {
		return "uv run"
	}
	if fileExists(filepath.Join(projectPath, "poetry.lock")) {
		return "poetry run"
	}
	return "python"
}

func detectJavaFramework(projectPath string) string {
	content := strings.ToLower(
		readFileOrEmpty(filepath.Join(projectPath, "pom.xml")) + "\n" +
			readFileOrEmpty(filepath.Join(projectPath, "build.gradle")) + "\n" +
			readFileOrEmpty(filepath.Join(projectPath, "build.gradle.kts")),
	)

	switch {
	case strings.Contains(content, "spring-boot") || strings.Contains(content, "org.springframework.boot"):
		return "spring"
	case strings.Contains(content, "quarkus"):
		return "quarkus"
	case strings.Contains(content, "micronaut"):
		return "micronaut"
	default:
		return "java"
	}
}

func detectJavaRunnerHint(projectPath string) string {
	if fileExists(filepath.Join(projectPath, "mvnw")) || fileExists(filepath.Join(projectPath, "mvnw.cmd")) {
		if detectJavaFramework(projectPath) == "spring" {
			return "mvn spring-boot:run"
		}
		return "mvn exec"
	}
	if fileExists(filepath.Join(projectPath, "gradlew")) || fileExists(filepath.Join(projectPath, "gradlew.bat")) {
		if detectJavaFramework(projectPath) == "spring" {
			return "gradle bootRun"
		}
		return "gradle run"
	}
	if fileExists(filepath.Join(projectPath, "pom.xml")) {
		return "mvn"
	}
	if fileExists(filepath.Join(projectPath, "build.gradle")) || fileExists(filepath.Join(projectPath, "build.gradle.kts")) {
		return "gradle"
	}
	return "java"
}

func isGoProject(projectPath string) bool {
	return fileExists(filepath.Join(projectPath, "go.mod"))
}

func detectGoFramework(projectPath string) string {
	content := strings.ToLower(readFileOrEmpty(filepath.Join(projectPath, "go.mod")))
	switch {
	case strings.Contains(content, "github.com/gin-gonic/gin"):
		return "gin"
	case strings.Contains(content, "github.com/gofiber/fiber"):
		return "fiber"
	case strings.Contains(content, "github.com/labstack/echo"):
		return "echo"
	case strings.Contains(content, "github.com/go-chi/chi"):
		return "chi"
	case strings.Contains(content, "go.temporal.io"):
		return "temporal"
	default:
		return "go"
	}
}

func detectGoEntryHint(projectPath string) string {
	if hasMainPackageInDir(projectPath) {
		return "go run ."
	}

	cmdDir := filepath.Join(projectPath, "cmd")
	entries, err := os.ReadDir(cmdDir)
	if err != nil {
		return "go run ."
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sub := filepath.Join(cmdDir, entry.Name())
		if hasMainPackageInDir(sub) {
			return fmt.Sprintf("go run ./cmd/%s", entry.Name())
		}
	}

	return "go run ."
}

func hasMainPackageInDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		content := strings.ToLower(readFileOrEmpty(filepath.Join(dir, entry.Name())))
		if strings.Contains(content, "package main") {
			return true
		}
	}
	return false
}

func detectPackageManagerFromLockfile(projectPath string) string {
	switch {
	case fileExists(filepath.Join(projectPath, "pnpm-lock.yaml")):
		return "pnpm"
	case fileExists(filepath.Join(projectPath, "bun.lock")) || fileExists(filepath.Join(projectPath, "bun.lockb")):
		return "bun"
	case fileExists(filepath.Join(projectPath, "package-lock.json")):
		return "npm"
	case fileExists(filepath.Join(projectPath, "yarn.lock")):
		return "yarn"
	default:
		return "unknown"
	}
}

func readFileOrEmpty(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func loadPackageJSON(path string) (packageJSON, error) {
	var pkg packageJSON
	data, err := os.ReadFile(path)
	if err != nil {
		return pkg, fmt.Errorf("❌ package.json not found in %s", filepath.Dir(path))
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return pkg, fmt.Errorf("❌ invalid package.json: %w", err)
	}
	return pkg, nil
}

func detectScript(pkg packageJSON) string {
	if pkg.Scripts == nil {
		return ""
	}
	if _, ok := pkg.Scripts["dev"]; ok {
		return "dev"
	}
	if _, ok := pkg.Scripts["start"]; ok {
		return "start"
	}
	return ""
}

func detectProjectRunner(projectPath string) (target string, installCmd []string, runCmd []string, err error) {
	packageJSONPath := filepath.Join(projectPath, "package.json")
	if _, statErr := os.Stat(packageJSONPath); statErr == nil {
		pkg, readErr := loadPackageJSON(packageJSONPath)
		if readErr != nil {
			return "", nil, nil, readErr
		}

		script := detectScript(pkg)
		if script == "" {
			return "", nil, nil, errors.New("❌ no dev/start script found in package.json")
		}

		pm, install, run, pmErr := detectPackageManager(projectPath, script)
		if pmErr != nil {
			return "", nil, nil, pmErr
		}

		nodeModulesPath := filepath.Join(projectPath, "node_modules")
		if _, statErr := os.Stat(nodeModulesPath); errors.Is(statErr, os.ErrNotExist) {
			return fmt.Sprintf("%s (%s)", pm, script), install, run, nil
		}

		return fmt.Sprintf("%s (%s)", pm, script), nil, run, nil
	}

	cargoPath := filepath.Join(projectPath, "Cargo.toml")
	if _, statErr := os.Stat(cargoPath); statErr == nil {
		if !hasCommand("cargo") {
			return "", nil, nil, errors.New("❌ missing dependency: cargo. Install Rust toolchain and run again")
		}
		return "cargo (run)", nil, []string{"cargo", "run"}, nil
	}

	if isGoProject(projectPath) {
		return detectGoRunner(projectPath)
	}

	if isJavaProject(projectPath) {
		return detectJavaRunner(projectPath)
	}

	if isPythonProject(projectPath) {
		return detectPythonRunner(projectPath)
	}

	return "", nil, nil, errors.New("❌ unsupported project type. Expected package.json, Cargo.toml, go.mod, pom.xml/build.gradle, pyproject.toml or .py entrypoint")
}

func detectGoRunner(projectPath string) (target string, installCmd []string, runCmd []string, err error) {
	if !hasCommand("go") {
		return "", nil, nil, errors.New("❌ missing dependency: go. Install Go and run again")
	}

	entry := detectGoEntryHint(projectPath)
	framework := detectGoFramework(projectPath)

	if strings.HasPrefix(entry, "go run ./cmd/") {
		cmdPath := strings.TrimPrefix(entry, "go run ")
		return fmt.Sprintf("go (%s)", framework), nil, []string{"go", "run", cmdPath}, nil
	}

	return fmt.Sprintf("go (%s)", framework), nil, []string{"go", "run", "."}, nil
}

func detectJavaRunner(projectPath string) (target string, installCmd []string, runCmd []string, err error) {
	framework := detectJavaFramework(projectPath)

	if fileExists(filepath.Join(projectPath, "pom.xml")) {
		mvnCmd, err := detectMavenCommand(projectPath)
		if err != nil {
			return "", nil, nil, err
		}

		if framework == "spring" {
			return "java (spring/maven)", nil, []string{mvnCmd, "spring-boot:run"}, nil
		}

		return "java (maven)", nil, []string{mvnCmd, "exec:java"}, nil
	}

	if fileExists(filepath.Join(projectPath, "build.gradle")) || fileExists(filepath.Join(projectPath, "build.gradle.kts")) {
		gradleCmd, err := detectGradleCommand(projectPath)
		if err != nil {
			return "", nil, nil, err
		}

		if framework == "spring" {
			return "java (spring/gradle)", nil, []string{gradleCmd, "bootRun"}, nil
		}

		if hasGradleApplicationPlugin(projectPath) {
			return "java (gradle)", nil, []string{gradleCmd, "run"}, nil
		}

		return "java (gradle)", nil, []string{gradleCmd, "build"}, nil
	}

	return "", nil, nil, errors.New("❌ java project detected but no pom.xml/build.gradle found")
}

func detectMavenCommand(projectPath string) (string, error) {
	if runtime.GOOS == "windows" && fileExists(filepath.Join(projectPath, "mvnw.cmd")) {
		return "mvnw.cmd", nil
	}
	if fileExists(filepath.Join(projectPath, "mvnw")) {
		return "./mvnw", nil
	}
	if hasCommand("mvn") {
		return "mvn", nil
	}
	return "", errors.New("❌ missing dependency: maven. Install mvn or add mvnw wrapper")
}

func detectGradleCommand(projectPath string) (string, error) {
	if runtime.GOOS == "windows" && fileExists(filepath.Join(projectPath, "gradlew.bat")) {
		return "gradlew.bat", nil
	}
	if fileExists(filepath.Join(projectPath, "gradlew")) {
		return "./gradlew", nil
	}
	if hasCommand("gradle") {
		return "gradle", nil
	}
	return "", errors.New("❌ missing dependency: gradle. Install gradle or add gradlew wrapper")
}

func hasGradleApplicationPlugin(projectPath string) bool {
	content := strings.ToLower(
		readFileOrEmpty(filepath.Join(projectPath, "build.gradle")) + "\n" +
			readFileOrEmpty(filepath.Join(projectPath, "build.gradle.kts")),
	)
	return strings.Contains(content, "application")
}

func isJavaProject(projectPath string) bool {
	if fileExists(filepath.Join(projectPath, "pom.xml")) || fileExists(filepath.Join(projectPath, "build.gradle")) || fileExists(filepath.Join(projectPath, "build.gradle.kts")) {
		return true
	}

	return fileExists(filepath.Join(projectPath, "src", "main", "java"))
}

func detectPythonRunner(projectPath string) (target string, installCmd []string, runCmd []string, err error) {
	pythonCmd, err := detectPythonCommand()
	if err != nil {
		return "", nil, nil, err
	}

	entry, err := detectPythonEntrypoint(projectPath)
	if err != nil {
		return "", nil, nil, err
	}

	framework := detectPythonFramework(projectPath)

	if fileExists(filepath.Join(projectPath, "uv.lock")) && hasCommand("uv") {
		run := append([]string{"uv", "run", "python"}, entry...)
		return fmt.Sprintf("python (%s, uv)", framework), []string{"uv", "sync"}, run, nil
	}

	if fileExists(filepath.Join(projectPath, "poetry.lock")) && hasCommand("poetry") {
		run := append([]string{"poetry", "run", "python"}, entry...)
		return fmt.Sprintf("python (%s, poetry)", framework), []string{"poetry", "install"}, run, nil
	}

	run := append([]string{pythonCmd}, entry...)
	if fileExists(filepath.Join(projectPath, "requirements.txt")) {
		install := []string{pythonCmd, "-m", "pip", "install", "-r", "requirements.txt"}
		return fmt.Sprintf("python (%s)", framework), install, run, nil
	}

	return fmt.Sprintf("python (%s)", framework), nil, run, nil
}

func detectPythonEntrypoint(projectPath string) ([]string, error) {
	if fileExists(filepath.Join(projectPath, "manage.py")) {
		return []string{"manage.py", "runserver"}, nil
	}
	if fileExists(filepath.Join(projectPath, "main.py")) {
		return []string{"main.py"}, nil
	}
	if fileExists(filepath.Join(projectPath, "app.py")) {
		return []string{"app.py"}, nil
	}
	if fileExists(filepath.Join(projectPath, "wsgi.py")) {
		return []string{"wsgi.py"}, nil
	}

	moduleName := sanitizeModuleName(filepath.Base(projectPath))
	if moduleName != "" {
		if fileExists(filepath.Join(projectPath, moduleName, "__main__.py")) || fileExists(filepath.Join(projectPath, "src", moduleName, "__main__.py")) {
			return []string{"-m", moduleName}, nil
		}
	}

	return nil, errors.New("❌ python project detected but no entrypoint found (main.py, app.py, manage.py, or package __main__.py)")
}

func sanitizeModuleName(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")
	return name
}

func detectPythonCommand() (string, error) {
	if hasCommand("python3") {
		return "python3", nil
	}
	if hasCommand("python") {
		return "python", nil
	}
	if runtime.GOOS == "windows" && hasCommand("py") {
		return "py", nil
	}
	return "", errors.New("❌ missing dependency: python. Install Python and run again")
}

func isPythonProject(projectPath string) bool {
	if fileExists(filepath.Join(projectPath, "pyproject.toml")) || fileExists(filepath.Join(projectPath, "requirements.txt")) || fileExists(filepath.Join(projectPath, "setup.py")) {
		return true
	}

	pythonMarkers := []string{"main.py", "app.py", "manage.py", "wsgi.py"}
	for _, marker := range pythonMarkers {
		if fileExists(filepath.Join(projectPath, marker)) {
			return true
		}
	}

	return false
}

func detectPackageManager(projectPath, script string) (pm string, installCmd []string, runCmd []string, err error) {
	type pmDef struct {
		name    string
		lock    string
		run     []string
		install []string
	}

	defs := []pmDef{
		{name: "pnpm", lock: "pnpm-lock.yaml", run: []string{"pnpm", script}, install: []string{"pnpm", "install"}},
		{name: "bun", lock: "bun.lock", run: []string{"bun", script}, install: []string{"bun", "install"}},
		{name: "bun", lock: "bun.lockb", run: []string{"bun", script}, install: []string{"bun", "install"}},
		{name: "npm", lock: "package-lock.json", run: []string{"npm", "run", script}, install: []string{"npm", "install"}},
		{name: "yarn", lock: "yarn.lock", run: []string{"yarn", script}, install: []string{"yarn", "install"}},
	}

	for _, def := range defs {
		if _, statErr := os.Stat(filepath.Join(projectPath, def.lock)); statErr == nil {
			if !hasCommand(def.name) {
				return "", nil, nil, fmt.Errorf("❌ missing dependency: %s. Install it and run again", def.name)
			}
			return def.name, def.install, def.run, nil
		}
	}

	return "", nil, nil, errors.New("❌ no lockfile found (pnpm-lock.yaml, bun.lock*, package-lock.json, yarn.lock)")
}

func runCommandInDir(dir string, args []string, out io.Writer, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("empty command")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = errOut
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func launchCrossPlatform(projectPath string, runCmd []string, out io.Writer, errOut io.Writer) error {
	shellLine := fmt.Sprintf("cd %s && %s", shellQuote(projectPath), shellJoin(runCmd))

	switch runtime.GOOS {
	case "linux":
		terminals := []struct {
			name string
			args []string
		}{
			{name: "ghostty", args: []string{"-e", "bash", "-lc", shellLine}},
			{name: "kitty", args: []string{"bash", "-lc", shellLine}},
			{name: "alacritty", args: []string{"-e", "bash", "-lc", shellLine}},
			{name: "gnome-terminal", args: []string{"--", "bash", "-lc", shellLine}},
		}

		for _, term := range terminals {
			if !hasCommand(term.name) {
				continue
			}
			cmd := exec.Command(term.name, term.args...)
			if err := cmd.Start(); err == nil {
				return nil
			}
		}

		fmt.Fprintln(out, "⚠️ No supported terminal found, running in current shell...")
		return runCommandInDir(projectPath, runCmd, out, errOut)
	case "darwin":
		if hasCommand("osascript") {
			script := fmt.Sprintf(`tell application "Terminal" to do script "%s"`, appleScriptEscape(shellLine))
			if err := exec.Command("osascript", "-e", script).Start(); err == nil {
				return nil
			}
		}
		fmt.Fprintln(out, "⚠️ Could not open macOS Terminal, running in current shell...")
		return runCommandInDir(projectPath, runCmd, out, errOut)
	case "windows":
		line := fmt.Sprintf("cd /d %s && %s", windowsQuote(projectPath), windowsJoin(runCmd))
		cmd := exec.Command("cmd", "/C", "start", "", "cmd", "/K", line)
		if err := cmd.Start(); err == nil {
			return nil
		}
		fmt.Fprintln(out, "⚠️ Could not open terminal window, running in current shell...")
		return runCommandInDir(projectPath, runCmd, out, errOut)
	default:
		fmt.Fprintf(out, "⚠️ Unsupported OS (%s), running in current shell...\n", runtime.GOOS)
		return runCommandInDir(projectPath, runCmd, out, errOut)
	}
}

func hasCommand(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

func shellJoin(args []string) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `"'"'`) + "'"
}

func appleScriptEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, `"`, `\\"`)
	return s
}

func windowsJoin(args []string) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, windowsQuote(a))
	}
	return strings.Join(parts, " ")
}

func windowsQuote(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\"&|<>()^") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}
