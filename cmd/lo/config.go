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

// config holds the persisted user configuration.
type config struct {
	Dirs       []string            `yaml:"dirs"`
	Launchpads map[string][]string `yaml:"launchpads,omitempty"`
}

// legacyJSONConfig is used only for migrating old config.json files.
type legacyJSONConfig struct {
	ProjectsDir  string              `json:"projectsDir,omitempty"`
	ProjectsDirs []string            `json:"projectsDirs,omitempty"`
	Launchpads   map[string][]string `json:"launchpads,omitempty"`
}

// legacyLaunchpadsFile represents the old separate launchpads.json format.
type legacyLaunchpadsFile struct {
	Pads map[string][]string `json:"pads"`
}

// --- Paths ---

// configPaths returns (yamlPath, legacyJSONPath, legacyPlaintextPath) inside ~/.config/lo/.
func configPaths() (string, string, string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", "", "", err
	}
	dir := filepath.Join(base, "lo")
	return filepath.Join(dir, "config.yaml"),
		filepath.Join(dir, "config.json"),
		filepath.Join(dir, "config"),
		nil
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
func loadOrInitConfig(cfgPath, jsonMigratePath, legacyPath string, in io.Reader, out io.Writer) (config, error) {
	cfg, err := loadConfig(cfgPath, jsonMigratePath, legacyPath)
	if err == nil {
		if len(cfg.Dirs) > 0 && allDirsExist(cfg.Dirs) {
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

// loadConfig reads config from YAML, falling back to legacy JSON then plaintext.
func loadConfig(cfgPath, jsonMigratePath, legacyPath string) (config, error) {
	// 1. Try YAML (current format).
	if data, err := os.ReadFile(cfgPath); err == nil {
		var cfg config
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return config{}, fmt.Errorf("invalid config yaml: %w", err)
		}
		cfg.Dirs = normalizeProjectDirs(cfg.Dirs)
		if cfg.Launchpads == nil {
			cfg.Launchpads = make(map[string][]string)
		}
		return cfg, nil
	}

	// 2. Migrate old config.json.
	if data, err := os.ReadFile(jsonMigratePath); err == nil {
		var legacy legacyJSONConfig
		if err := json.Unmarshal(data, &legacy); err != nil {
			return config{}, fmt.Errorf("invalid legacy config json: %w", err)
		}
		dirs := normalizeProjectDirs(legacy.ProjectsDirs)
		if len(dirs) == 0 && strings.TrimSpace(legacy.ProjectsDir) != "" {
			dirs = normalizeProjectDirs([]string{legacy.ProjectsDir})
		}
		if len(dirs) == 0 {
			return config{}, errors.New("legacy config.json has no valid dirs")
		}
		if legacy.Launchpads == nil {
			legacy.Launchpads = make(map[string][]string)
		}
		return config{Dirs: dirs, Launchpads: legacy.Launchpads}, nil
	}

	// 3. Migrate legacy plaintext (PROJECTS_DIR=...).
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
		return config{Dirs: []string{v}, Launchpads: make(map[string][]string)}, nil
	}

	return config{}, errors.New("config not found")
}

func saveConfig(path string, cfg config) error {
	dirs := normalizeProjectDirs(cfg.Dirs)
	if len(dirs) == 0 {
		return errors.New("projects dirs cannot be empty")
	}
	cfg.Dirs = dirs
	if cfg.Launchpads == nil {
		cfg.Launchpads = make(map[string][]string)
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
	fmt.Fprintln(out, "✅ Migrated legacy launchpads into config.yaml")
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
	initialValue := strings.Join(current.Dirs, ", ")

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

	return config{Dirs: finalModel.selecteds, Launchpads: current.Launchpads}, nil
}

func promptProjectsDirLine(current config, in io.Reader, out io.Writer) (config, error) {
	reader := bufio.NewReader(in)
	if runtime.GOOS == "windows" {
		fmt.Fprintln(out, "📁 Enter your projects directories (comma-separated):")
	} else {
		fmt.Fprintln(out, "📁 Enter your projects directories (comma-separated, relative to ~):")
	}
	currentDirs := strings.Join(current.Dirs, ", ")
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

	return config{Dirs: projectsPaths, Launchpads: current.Launchpads}, nil
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

func allDirsExist(dirs []string) bool {
	for _, dir := range dirs {
		st, err := os.Stat(dir)
		if err != nil || !st.IsDir() {
			return false
		}
	}
	return true
}

// --- Settings flow ---

// runSettingsFlow shows the interactive settings TUI and handles all sub-flows.
func runSettingsFlow(cfgPath, jsonMigratePath, legacyPath string, cfg *config, in io.Reader, out io.Writer) error {
	inFile, inOk := in.(*os.File)
	outFile, outOk := out.(*os.File)
	if !inOk || !outOk || !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		// Non-TTY fallback: plaintext dir prompt.
		updated, err := promptProjectsDirLine(*cfg, in, out)
		if err != nil && err.Error() != "canceled" {
			return err
		}
		if err == nil {
			*cfg = updated
			return saveConfig(cfgPath, *cfg)
		}
		return nil
	}

	for {
		action, err := showSettingsMenu(*cfg, inFile, outFile)
		if err != nil || action == "" {
			return err
		}

		switch action {
		case "dirs":
			updated, err := promptProjectsDirWithBubbleTea(*cfg, inFile, outFile)
			if err != nil {
				if err.Error() == "canceled" {
					continue
				}
				return err
			}
			updated.Launchpads = cfg.Launchpads
			*cfg = updated
			if err := saveConfig(cfgPath, *cfg); err != nil {
				return fmt.Errorf("❌ failed saving config: %w", err)
			}

		case "launchpads":
			if err := runLaunchpadSettingsFlow(cfgPath, cfg, inFile, outFile); err != nil && err.Error() != "back" {
				return err
			}
		}
	}
}

// runLaunchpadSettingsFlow manages launchpads from within the settings menu.
func runLaunchpadSettingsFlow(cfgPath string, cfg *config, inFile, outFile *os.File) error {
	if cfg.Launchpads == nil {
		cfg.Launchpads = make(map[string][]string)
	}

	for {
		action, name, err := showLaunchpadSettings(*cfg, inFile, outFile)
		if err != nil || action == "back" {
			return errors.New("back")
		}

		switch action {
		case "new":
			entries, loadErr := listProjects(cfg.Dirs)
			if loadErr != nil {
				return loadErr
			}
			projects := displayNames(entries)
			selected, selErr := selectProjectsForLaunchpad(projects, nil,
				fmt.Sprintf("🧩 New launchpad: %s", name), inFile, outFile)
			if selErr != nil {
				if selErr.Error() == "canceled" {
					continue
				}
				return selErr
			}
			if len(selected) > 0 {
				cfg.Launchpads[name] = selected
				if err := saveConfig(cfgPath, *cfg); err != nil {
					return err
				}
			}

		case "edit":
			entries, loadErr := listProjects(cfg.Dirs)
			if loadErr != nil {
				return loadErr
			}
			projects := displayNames(entries)
			current := cfg.Launchpads[name]
			updated, selErr := selectProjectsForLaunchpad(projects, current,
				fmt.Sprintf("🛠  Edit: %s", name), inFile, outFile)
			if selErr != nil {
				if selErr.Error() == "canceled" {
					continue
				}
				return selErr
			}
			if len(updated) == 0 {
				delete(cfg.Launchpads, name)
			} else {
				cfg.Launchpads[name] = updated
			}
			if err := saveConfig(cfgPath, *cfg); err != nil {
				return err
			}

		case "delete":
			delete(cfg.Launchpads, name)
			if err := saveConfig(cfgPath, *cfg); err != nil {
				return err
			}
		}
	}
}
