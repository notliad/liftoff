package main

// Configuration loading, saving, migration, and interactive setup.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// launchpadEntry represents a project in a launchpad with its launch order.
// Order 1 is the first batch; equal orders launch simultaneously.
type launchpadEntry struct {
	Name  string `yaml:"name"`
	Order int    `yaml:"order,omitempty"`
}

// UnmarshalYAML lets launchpadEntry load both plain strings (old format) and structs.
func (e *launchpadEntry) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		e.Name = value.Value
		e.Order = 1
		return nil
	}
	type plain launchpadEntry
	return value.Decode((*plain)(e))
}

// config holds the persisted user configuration (projects dirs + launchpads).
type config struct {
	ProjectsDirs []string                    `yaml:"projectsDirs,omitempty"`
	Launchpads   map[string][]launchpadEntry `yaml:"launchpads,omitempty"`
}

// legacyJSONConfig is used to migrate the old config.json format.
type legacyJSONConfig struct {
	ProjectsDir  string              `json:"projectsDir,omitempty"`
	ProjectsDirs []string            `json:"projectsDirs,omitempty"`
	Launchpads   map[string][]string `json:"launchpads,omitempty"`
}

// legacyLaunchpadsFile represents the old separate launchpads.json format.
type legacyLaunchpadsFile struct {
	Pads map[string][]string `json:"pads"`
}

// convertLegacyLaunchpads converts old []string launchpad format to []launchpadEntry.
func convertLegacyLaunchpads(pads map[string][]string) map[string][]launchpadEntry {
	result := make(map[string][]launchpadEntry, len(pads))
	for name, projects := range pads {
		entries := make([]launchpadEntry, len(projects))
		for i, p := range projects {
			entries[i] = launchpadEntry{Name: p, Order: 1}
		}
		result[name] = entries
	}
	return result
}

// --- Paths ---

// configPaths returns (configYAML, legacyPlaintext) paths inside ~/.config/lo/.
func configPaths() (string, string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", "", err
	}
	dir := filepath.Join(base, "lo")
	return filepath.Join(dir, "config.yaml"), filepath.Join(dir, "config"), nil
}

// legacyConfigJSONPath returns the old config.json path for migration.
func legacyConfigJSONPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "lo", "config.json"), nil
}

func legacyLaunchpadsPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "lo", "launchpads.json"), nil
}

// --- Load / Save ---

// loadOrInitConfig loads existing config or runs the interactive setup on first use.
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

// loadConfig reads config from the YAML path, falling back to old JSON then legacy plaintext.
func loadConfig(cfgPath, legacyPath string) (config, error) {
	// Primary: YAML format
	if data, err := os.ReadFile(cfgPath); err == nil {
		var cfg config
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return config{}, fmt.Errorf("invalid config yaml: %w", err)
		}
		cfg.ProjectsDirs = normalizeProjectDirs(cfg.ProjectsDirs)
		if cfg.Launchpads == nil {
			cfg.Launchpads = make(map[string][]launchpadEntry)
		}
		return cfg, nil
	}

	// Migration: old JSON format (config.json)
	if jsonPath, err := legacyConfigJSONPath(); err == nil {
		if data, err := os.ReadFile(jsonPath); err == nil {
			var legacy legacyJSONConfig
			if err := json.Unmarshal(data, &legacy); err == nil {
				dirs := normalizeProjectDirs(legacy.ProjectsDirs)
				if len(dirs) == 0 && legacy.ProjectsDir != "" {
					dirs = normalizeProjectDirs([]string{legacy.ProjectsDir})
				}
				cfg := config{
					ProjectsDirs: dirs,
					Launchpads:   convertLegacyLaunchpads(legacy.Launchpads),
				}
				if cfg.Launchpads == nil {
					cfg.Launchpads = make(map[string][]launchpadEntry)
				}
				return cfg, nil
			}
		}
	}

	// Migration: legacy plaintext format
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
		return config{
			ProjectsDirs: []string{v},
			Launchpads:   make(map[string][]launchpadEntry),
		}, nil
	}

	return config{}, errors.New("config not found")
}

func saveConfig(path string, cfg config) error {
	dirs := effectiveProjectDirs(cfg)
	if len(dirs) == 0 {
		return errors.New("projects dirs cannot be empty")
	}
	cfg.ProjectsDirs = dirs
	if cfg.Launchpads == nil {
		cfg.Launchpads = make(map[string][]launchpadEntry)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// --- Migration ---

// migrateLegacyLaunchpads merges old launchpads.json into the main config.
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
		cfg.Launchpads = make(map[string][]launchpadEntry)
	}
	changed := false
	for name, projects := range legacy.Pads {
		if _, exists := cfg.Launchpads[name]; exists {
			continue
		}
		entries := make([]launchpadEntry, len(projects))
		for i, p := range projects {
			entries[i] = launchpadEntry{Name: p, Order: 1}
		}
		cfg.Launchpads[name] = entries
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

// --- Interactive setup ---

// promptProjectsDir picks between the BubbleTea prompt (TTY) and a plain line prompt.
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
		tea.WithAltScreen(),
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

	return config{ProjectsDirs: finalModel.selecteds, Launchpads: current.Launchpads}, nil
}

func promptProjectsDirLine(current config, in io.Reader, out io.Writer) (config, error) {
	reader := bufio.NewReader(in)
	if runtime.GOOS == "windows" {
		fmt.Fprintln(out, "📁 Enter your projects directories (comma-separated):")
	} else {
		fmt.Fprintln(out, "📁 Enter your projects directories (comma-separated, relative to ~):")
	}
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

	return config{ProjectsDirs: projectsPaths, Launchpads: current.Launchpads}, nil
}

// --- Path resolution helpers ---

func resolveProjectsPath(home, inputPath string) (string, error) {
	paths, err := resolveProjectsPaths(home, inputPath)
	if err != nil {
		return "", err
	}
	return paths[0], nil
}

// resolveProjectsPaths expands and validates comma-separated directory paths.
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
		case part == "" || part == "~":
			projectsPath = home
		case strings.HasPrefix(part, "~/") || strings.HasPrefix(part, `~\`):
			// Both ~/ (Unix) and ~\ (Windows-style) are accepted.
			projectsPath = filepath.Join(home, part[2:])
		case runtime.GOOS == "windows" && isUserProfilePrefix(part):
			// %USERPROFILE% or %USERPROFILE%\sub
			projectsPath = filepath.Join(home, stripUserProfilePrefix(part))
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

// isUserProfilePrefix returns true when s starts with %USERPROFILE% (case-insensitive).
func isUserProfilePrefix(s string) bool {
	upper := strings.ToUpper(s)
	return upper == "%USERPROFILE%" || strings.HasPrefix(upper, `%USERPROFILE%\`) || strings.HasPrefix(upper, "%USERPROFILE%/")
}

// stripUserProfilePrefix removes the %USERPROFILE% prefix and any following separator.
func stripUserProfilePrefix(s string) string {
	rest := s[len("%USERPROFILE%"):]
	if len(rest) > 0 && (rest[0] == '\\' || rest[0] == '/') {
		return rest[1:]
	}
	return rest
}

// normalizeProjectDirs deduplicates and cleans a list of directory paths.
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

// effectiveProjectDirs returns the resolved list of project root directories.
func effectiveProjectDirs(cfg config) []string {
	return normalizeProjectDirs(cfg.ProjectsDirs)
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

// runConfigMenu opens the visual configuration menu where the user can edit
// project directories or existing launchpads.
func runConfigMenu(cfgPath string, cfg *config, in io.Reader, out io.Writer) error {
	const optDirs = "📁  Edit directories"
	const optPads = "🧩  Edit launchpads"

	choice, err := selectMenuItem([]string{optDirs, optPads}, "⚙  Config", in, out)
	if err != nil {
		return nil // canceled
	}

	switch choice {
	case optDirs:
		newCfg, err := promptProjectsDir(*cfg, in, out)
		if err != nil {
			if err.Error() == "canceled" {
				return nil
			}
			return err
		}
		newCfg.Launchpads = cfg.Launchpads
		if err := saveConfig(cfgPath, newCfg); err != nil {
			return fmt.Errorf("❌ failed saving config: %w", err)
		}
		fmt.Fprintf(out, "✅ Saved config to %s\n", cfgPath)
		*cfg = newCfg

	case optPads:
		if len(cfg.Launchpads) == 0 {
			fmt.Fprintln(out, "⚠️  No launchpads found. Create one with 'lo --pad <name>'")
			return nil
		}
		dirs := effectiveProjectDirs(*cfg)
		entries, err := listProjects(dirs)
		if err != nil {
			return err
		}
		return editLaunchpadFlow(cfgPath, cfg, entries, "", in, out)
	}

	return nil
}
