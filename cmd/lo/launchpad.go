package main

// Launchpad management: create, edit, list, and run named project groups.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// runLaunchpadFlow creates a new launchpad if it doesn't exist, or runs an
// existing one by launching all its projects.
func runLaunchpadFlow(cfgPath string, cfg *config, entries []projectEntry, name string, watchMode bool, in io.Reader, out io.Writer, errOut io.Writer) error {
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

	if watchMode {
		return launchProjectsWatchMode(name, resolvedProjects, in, out, errOut)
	}

	return launchProjectsParallel(name, resolvedProjects, out, errOut)
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

// --- Project multi-selection ---

// selectProjectsForLaunchpad opens the checklist model (TTY) or a line-based
// fallback for choosing which projects belong to a launchpad.
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

// --- Resolution ---

// resolveLaunchpadProjects maps saved display-names back to discovered project entries.
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
