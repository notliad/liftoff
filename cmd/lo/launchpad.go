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
		cfg.Launchpads = make(map[string][]launchpadEntry)
	}
	projects := displayNames(entries)

	savedEntries, exists := cfg.Launchpads[name]
	if !exists {
		fmt.Fprintf(out, "⚠️ Launchpad '%s' not found. Let's create it.\n", name)
		selectedEntries, err := selectProjectsForLaunchpad(projects, nil, fmt.Sprintf("🧩 Create launchpad: %s", name), in, out)
		if err != nil {
			return err
		}
		if len(selectedEntries) == 0 {
			return errors.New("⚠️ launchpad must contain at least one project")
		}
		cfg.Launchpads[name] = selectedEntries
		if err := saveConfig(cfgPath, *cfg); err != nil {
			return fmt.Errorf("❌ could not save launchpad: %w", err)
		}
		fmt.Fprintf(out, "✅ Launchpad '%s' saved with %d projects\n", name, len(selectedEntries))
		fmt.Fprintf(out, "💡 Run 'lo --pad %s' again to start these projects\n", name)
		return nil
	}

	resolvedProjects := resolveLaunchpadProjects(savedEntries, entries)
	if len(resolvedProjects) == 0 {
		return fmt.Errorf("⚠️ launchpad '%s' has no resolvable projects", name)
	}

	if watchMode {
		return launchProjectsWatchMode(name, resolvedProjects, in, out, errOut)
	}

	groups := groupByLaunchOrder(resolvedProjects)
	if len(groups) <= 1 {
		return launchProjectsParallel(name, resolvedProjects, out, errOut)
	}
	return launchProjectsSequential(name, groups, out, errOut)
}

func listLaunchpadsFlow(cfg config, launchpadName string, out io.Writer) error {
	if len(cfg.Launchpads) == 0 {
		fmt.Fprintln(out, "📦 No launchpads found")
		return nil
	}

	if strings.TrimSpace(launchpadName) != "" {
		entries, ok := cfg.Launchpads[launchpadName]
		if !ok {
			return fmt.Errorf("❌ launchpad not found: %s", launchpadName)
		}
		fmt.Fprintf(out, "🧩 Launchpad: %s (%d projects)\n", launchpadName, len(entries))
		for _, e := range entries {
			o := e.Order
			if o < 1 {
				o = 1
			}
			fmt.Fprintf(out, "- %s (batch %d)\n", e.Name, o)
		}
		return nil
	}

	names := sortedMapKeys(cfg.Launchpads)
	fmt.Fprintf(out, "🧩 Launchpads (%d):\n", len(names))
	for _, name := range names {
		entries := cfg.Launchpads[name]
		fmt.Fprintf(out, "- %s (%d)\n", name, len(entries))
		for _, e := range entries {
			o := e.Order
			if o < 1 {
				o = 1
			}
			fmt.Fprintf(out, "  - %s (batch %d)\n", e.Name, o)
		}
	}
	return nil
}

func editLaunchpadFlow(cfgPath string, cfg *config, entries []projectEntry, name string, in io.Reader, out io.Writer) error {
	if cfg.Launchpads == nil {
		cfg.Launchpads = make(map[string][]launchpadEntry)
	}
	if len(cfg.Launchpads) == 0 {
		return errors.New("⚠️ no launchpads found to edit")
	}

	if strings.TrimSpace(name) == "" {
		names := sortedMapKeys(cfg.Launchpads)
		picked, pickErr := selectProject(names, nil, "", "Select a launchpad", in, out)
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
	updated, err := selectProjectsForLaunchpad(projects, current, fmt.Sprintf("🛠  Edit launchpad: %s", name), in, out)
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

// selectProjectsForLaunchpad opens the ordered checklist model (TTY) or a line-based
// fallback for choosing which projects belong to a launchpad and their launch order.
func selectProjectsForLaunchpad(projects []string, selected []launchpadEntry, title string, in io.Reader, out io.Writer) ([]launchpadEntry, error) {
	inFile, inOk := in.(*os.File)
	outFile, outOk := out.(*os.File)
	if !inOk || !outOk || !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		return selectProjectsForLaunchpadLine(projects, selected, in, out)
	}

	model := newProjectChecklistModel(title, projects, selected)
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

	return finalModel.selectedEntries(), nil
}

func selectProjectsForLaunchpadLine(projects []string, selected []launchpadEntry, in io.Reader, out io.Writer) ([]launchpadEntry, error) {
	selectedSet := make(map[string]int, len(selected))
	for _, e := range selected {
		o := e.Order
		if o < 1 {
			o = 1
		}
		selectedSet[e.Name] = o
	}

	reader := bufio.NewReader(in)
	fmt.Fprintln(out, "📚 Select projects (comma-separated names), empty to keep current:")
	for i, p := range projects {
		if o, ok := selectedSet[p]; ok {
			fmt.Fprintf(out, "  %2d) [%d] %s\n", i+1, o, p)
		} else {
			fmt.Fprintf(out, "  %2d) [ ] %s\n", i+1, p)
		}
	}
	fmt.Fprint(out, "\n Projects: ")
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		out := make([]launchpadEntry, 0, len(selectedSet))
		for _, p := range projects {
			if o, ok := selectedSet[p]; ok {
				out = append(out, launchpadEntry{Name: p, Order: o})
			}
		}
		return out, nil
	}

	result := make([]launchpadEntry, 0)
	for _, chunk := range strings.Split(line, ",") {
		name := strings.TrimSpace(chunk)
		if name == "" {
			continue
		}
		for _, p := range projects {
			if p == name {
				o := 1
				if prev, ok := selectedSet[p]; ok {
					o = prev
				}
				result = append(result, launchpadEntry{Name: p, Order: o})
				break
			}
		}
	}
	return result, nil
}

// --- Resolution ---

// resolveLaunchpadProjects maps saved launchpadEntries back to discovered project entries,
// preserving each entry's LaunchOrder for sequential batch launching.
func resolveLaunchpadProjects(saved []launchpadEntry, entries []projectEntry) []projectEntry {
	resolved := make([]projectEntry, 0, len(saved))
	used := make(map[string]bool)

	for _, item := range saved {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		o := item.Order
		if o < 1 {
			o = 1
		}

		if exact, ok := findProjectByDisplay(entries, name); ok {
			if !used[exact.Path] {
				exact.LaunchOrder = o
				resolved = append(resolved, exact)
				used[exact.Path] = true
			}
			continue
		}

		for _, entry := range entries {
			if entry.Name == name && !used[entry.Path] {
				entry.LaunchOrder = o
				resolved = append(resolved, entry)
				used[entry.Path] = true
				break
			}
		}
	}

	return resolved
}
