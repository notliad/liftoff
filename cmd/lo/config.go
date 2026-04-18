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
)

// config holds the persisted user configuration (projects dirs + launchpads).
type config struct {
	ProjectsDir  string              `json:"projectsDir,omitempty"`
	ProjectsDirs []string            `json:"projectsDirs,omitempty"`
	Launchpads   map[string][]string `json:"launchpads,omitempty"`
}

// legacyLaunchpadsFile represents the old separate launchpads.json format.
type legacyLaunchpadsFile struct {
	Pads map[string][]string `json:"pads"`
}

// --- Paths ---

// configPaths returns (configJSON, legacyPlaintext) paths inside ~/.config/lo/.
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

// loadConfig reads config from the JSON path or falls back to the legacy plaintext format.
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

	return config{ProjectsDir: projectsPaths[0], ProjectsDirs: projectsPaths, Launchpads: current.Launchpads}, nil
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
