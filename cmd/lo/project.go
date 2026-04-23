package main

// Project discovery, listing, filtering, and interactive selection.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// projectEntry represents a discovered runnable project on disk.
type projectEntry struct {
	Name        string
	Path        string
	RootDir     string
	Display     string // includes root hint when names collide across roots
	LaunchOrder int    // batch position within a launchpad (0 treated as 1)
}

// --- Discovery ---

// listProjects scans the given root directories and returns runnable projects.
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

	// Disambiguate projects with identical names in different roots.
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

// --- Selection ---

// chooseProject matches a direct query or opens the interactive picker.
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
	selected, err := selectProject(projects, stackMap, query, "Select a project", in, out)
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

func selectProject(projects []string, stackMap map[string]string, query string, title string, in io.Reader, out io.Writer) (string, error) {
	return selectWithBubbleTea(projects, stackMap, query, title, in, out)
}

// selectWithBubbleTea runs the interactive BubbleTea picker, falling back to
// a line-based menu when stdin/stdout are not a terminal.
func selectWithBubbleTea(projects []string, stackMap map[string]string, initialQuery string, title string, in io.Reader, out io.Writer) (string, error) {
	inFile, inOk := in.(*os.File)
	outFile, outOk := out.(*os.File)
	if !inOk || !outOk || !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		return selectWithLineMenu(projects, initialQuery, title, in, out)
	}

	model := newProjectPickerModel(title, projects, stackMap, initialQuery)
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

// selectWithLineMenu provides a non-interactive numbered menu for piped/CI contexts.
func selectWithLineMenu(projects []string, initialQuery string, title string, in io.Reader, out io.Writer) (string, error) {
	reader := bufio.NewReader(in)
	filter := strings.TrimSpace(initialQuery)

	for {
		filtered := filterProjects(projects, filter)
		fmt.Fprintln(out, "\n🚀 Liftoff")
		fmt.Fprintf(out, "\n📚 %s\n", title)
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

// --- Filtering ---

// filterProjects returns projects whose names contain the filter (case-insensitive).
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

// --- Stack preview ---

func buildProjectStackMap(projects []projectEntry) map[string]string {
	stackMap := make(map[string]string, len(projects))
	for _, project := range projects {
		stackMap[project.Display] = previewProjectStack(project.Path)
	}
	return stackMap
}

// --- Simple menu selection ---

// selectMenuItem opens a minimal menu (no filter) for choosing among a small set of options.
// Falls back to a numbered line menu when stdin/stdout are not a terminal.
func selectMenuItem(items []string, title string, in io.Reader, out io.Writer) (string, error) {
	inFile, inOk := in.(*os.File)
	outFile, outOk := out.(*os.File)
	if !inOk || !outOk || !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		return selectWithLineMenu(items, "", title, in, out)
	}

	model := newSimpleMenuModel(title, items)
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

	finalModel, ok := result.(*simpleMenuModel)
	if !ok || finalModel == nil || finalModel.canceled || finalModel.selected == "" {
		return "", errors.New("canceled")
	}

	return finalModel.selected, nil
}

// --- List flow ---

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
