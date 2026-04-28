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
	Name           string
	Path           string
	RootDir        string
	Variant        string
	Display        string // includes root hint when names collide across roots
	ScriptOverride string // when set, overrides the detected npm script (e.g. "storybook")
}

func (p projectEntry) identityKey() string {
	return strings.Join([]string{p.Path, p.Variant, p.ScriptOverride}, "\x00")
}

// --- Discovery ---

// listProjects scans the given root directories and returns runnable projects.
func listProjects(roots []string) ([]projectEntry, error) {
	type discoveredProject struct {
		name       string
		path       string
		root       string
		hasRuntime bool
		hasCompose bool
	}

	projects := make([]projectEntry, 0)
	discovered := make([]discoveredProject, 0)
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
			hasRuntime := isRunnableProjectDir(projectPath)
			hasCompose := hasComposeFile(projectPath)
			if !hasRuntime && !hasCompose {
				continue
			}
			discovered = append(discovered, discoveredProject{
				name:       entry.Name(),
				path:       projectPath,
				root:       root,
				hasRuntime: hasRuntime,
				hasCompose: hasCompose,
			})
			nameCounts[entry.Name()]++
		}
	}

	for _, item := range discovered {
		baseDisplay := item.name
		if nameCounts[item.name] > 1 {
			baseDisplay = fmt.Sprintf("%s (%s)", item.name, item.root)
		}

		base := projectEntry{
			Name:    item.name,
			Path:    item.path,
			RootDir: item.root,
			Display: baseDisplay,
		}

		if item.hasRuntime {
			projects = append(projects, base)
			projects = append(projects, addStorybookVariants(base)...)
		}

		if item.hasCompose {
			compose := base
			compose.Variant = projectVariantCompose
			compose.Display = baseDisplay + " (compose)"
			projects = append(projects, compose)
		}
	}

	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Display == projects[j].Display {
			return projects[i].RootDir < projects[j].RootDir
		}
		return projects[i].Display < projects[j].Display
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
		for _, entry := range entries {
			if entry.Name == query {
				return entry, nil
			}
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

// addStorybookVariants appends a separate storybook launch entry for a
// node project that declares a "storybook" script alongside its primary script.
func addStorybookVariants(project projectEntry) []projectEntry {
	if project.ScriptOverride != "" || project.Variant != "" {
		return nil
	}
	pkgPath := filepath.Join(project.Path, "package.json")
	if !fileExists(pkgPath) {
		return nil
	}
	pkg, err := loadPackageJSON(pkgPath)
	if err != nil || pkg.Scripts == nil {
		return nil
	}
	if _, hasStorybook := pkg.Scripts["storybook"]; !hasStorybook {
		return nil
	}
	// Only add a storybook variant when there is also a primary script.
	// (A storybook-only project is already listed as-is.)
	primary := detectScript(pkg)
	if primary == "storybook" {
		return nil
	}
	variant := project
	variant.Variant = projectVariantStorybook
	variant.ScriptOverride = "storybook"
	variant.Display = project.Display + " (storybook)"
	return []projectEntry{variant}
}

// --- Stack preview ---

func buildProjectStackMap(projects []projectEntry) map[string]string {
	stackMap := make(map[string]string, len(projects))
	for _, project := range projects {
		if project.Variant == projectVariantStorybook {
			stackMap[project.Display] = "📖 storybook"
		} else if project.Variant == projectVariantCompose {
			stackMap[project.Display] = "🐳 docker compose"
		} else {
			stackMap[project.Display] = previewProjectStack(project.Path)
		}
	}
	return stackMap
}

// --- List flow ---

func listProjectsFlow(projects []projectEntry, out io.Writer) {
	fmt.Fprintf(out, "📚 Projects (%d):\n", len(projects))
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROJECT\tSTACK\tROOT")
	for _, project := range projects {
		var stack string
		if project.Variant == projectVariantStorybook {
			stack = "📖 storybook"
		} else if project.Variant == projectVariantCompose {
			stack = "🐳 docker compose"
		} else {
			stack = previewProjectStack(project.Path)
			if strings.TrimSpace(stack) == "" {
				stack = "-"
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", project.Display, stack, project.RootDir)
	}
	tw.Flush()
}
