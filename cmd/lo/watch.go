package main

// Watch mode: real-time process monitoring dashboard using BubbleTea.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// --- Types ---

type processTreeStats struct {
	ProcessCount  int
	CPUPercent    float64
	CPUPercentRaw float64
	CPUCoresUsed  float64
	RSSKB         int
}

type processSample struct {
	PID        int
	ParentPID  int
	CPUPercent float64
	RSSKB      int
}

type watchTickMsg struct{}

type watchTarget struct {
	ProjectName string
	Terminal    string
	RootPID     int
	StartedAt   time.Time

	Stats       processTreeStats
	LastUpdated time.Time
	LastErr     string
	Done        bool
	StatusText  string
}

// --- BubbleTea dashboard model ---

type watchDashboardModel struct {
	title     string
	startedAt time.Time
	targets   []watchTarget
	allDone   bool
}

func newWatchDashboardModel(title string, targets []watchTarget) *watchDashboardModel {
	return &watchDashboardModel{
		title:     title,
		startedAt: time.Now(),
		targets:   targets,
	}
}

func watchTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg {
		return watchTickMsg{}
	})
}

func (m *watchDashboardModel) Init() tea.Cmd {
	return watchTick()
}

func (m *watchDashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.allDone = true
			return m, tea.Quit
		}
	case watchTickMsg:
		allDone := true
		now := time.Now()
		for i := range m.targets {
			t := &m.targets[i]
			if t.Done {
				continue
			}

			stats, err := collectProcessTreeStats(t.RootPID)
			if err != nil {
				t.LastErr = err.Error()
				t.LastUpdated = now
				t.StatusText = "stats error"
				allDone = false
				continue
			}

			if stats.ProcessCount == 0 {
				t.Done = true
				t.LastUpdated = now
				t.StatusText = "finished"
				continue
			}

			t.Stats = stats
			t.LastErr = ""
			t.LastUpdated = now
			t.StatusText = "running"
			allDone = false
		}

		if allDone {
			m.allDone = true
			return m, tea.Quit
		}
		return m, watchTick()
	}

	return m, nil
}

func (m *watchDashboardModel) View() string {
	uptime := time.Since(m.startedAt).Round(time.Second)

	var b strings.Builder

	b.WriteString("\n")
	b.WriteString(" " + titleStyle.Render("🚀 Liftoff"))
	b.WriteString("\n\n")
	b.WriteString(" " + promptStyle.Render("👀  "+m.title))
	b.WriteString("  " + mutedStyle.Render("uptime: "+uptime.String()))
	b.WriteString("\n\n")

	const (
		colProject = 24
		colPID     = 7
		colProcs   = 6
		colCPU     = 8
		colCores   = 8
		colMem     = 9
		colStatus  = 14
	)
	header := fmt.Sprintf(" %-*s  %-*s %-*s %-*s %-*s %-*s %-*s %s",
		colProject, "Project",
		colPID, "PID",
		colProcs, "Procs",
		colCPU, "CPU%",
		colCores, "Cores",
		colMem, "Mem(MB)",
		colStatus, "Status",
		"Terminal",
	)
	sepLen := len(header) + 2
	if sepLen < 80 {
		sepLen = 80
	}
	b.WriteString(mutedStyle.Render(header))
	b.WriteString("\n")
	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n")

	for _, t := range m.targets {
		memMB := float64(t.Stats.RSSKB) / 1024.0
		cpu := t.Stats.CPUPercent
		cores := t.Stats.CPUCoresUsed
		status := t.StatusText
		if status == "" {
			status = "initializing"
		}

		statusStr := mutedStyle.Render(status)
		switch status {
		case "running":
			statusStr = successStyle.Render(status)
		case "finished":
			statusStr = previewStyle.Render(status)
		case "stats error":
			statusStr = warnStyle.Render(status)
		}

		b.WriteString(fmt.Sprintf(" %-*s  %-*d %-*d %-*.1f %-*.2f %-*.1f ",
			colProject, truncateRunes(t.ProjectName, colProject),
			colPID, t.RootPID,
			colProcs, t.Stats.ProcessCount,
			colCPU, cpu,
			colCores, cores,
			colMem, memMB,
		))
		b.WriteString(fmt.Sprintf("%-*s ", colStatus, statusStr))
		b.WriteString(mutedStyle.Render(t.Terminal))
		if t.LastErr != "" {
			b.WriteString("  " + warnStyle.Render("⚠"))
		}
		b.WriteString("\n")
	}

	b.WriteString(mutedStyle.Render(strings.Repeat("─", sepLen)))
	b.WriteString("\n\n")

	if m.allDone {
		b.WriteString(" " + successStyle.Render("✓  all processes finished"))
	} else {
		b.WriteString(" " + successStyle.Render("●  monitoring"))
	}
	b.WriteString("\n")

	for _, t := range m.targets {
		if t.LastErr != "" {
			b.WriteString(" " + warnStyle.Render(fmt.Sprintf("⚠  [%s] %s", t.ProjectName, t.LastErr)))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(" " + mutedStyle.Render("q / esc  stop monitoring (projects keep running)"))
	b.WriteString("\n")
	return b.String()
}

// --- Watch mode launchers ---

// launchWithWatch starts a single project in a detached terminal and opens the
// monitoring dashboard.
func launchWithWatch(projectPath, projectName string, runCmd []string, in io.Reader, out io.Writer, errOut io.Writer) error {
	if len(runCmd) == 0 {
		return errors.New("empty command")
	}
	if !hasCommand("ps") {
		return errors.New("❌ watch mode requires 'ps' command")
	}

	pid, terminalName, err := startWatchTerminal(projectPath, runCmd)
	if err != nil {
		return err
	}

	inFile, inOk := in.(*os.File)
	outFile, outOk := out.(*os.File)
	if !inOk || !outOk || !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		return errors.New("❌ watch mode requires an interactive terminal")
	}

	target := watchTarget{
		ProjectName: projectName,
		Terminal:    terminalName,
		RootPID:     pid,
		StartedAt:   time.Now(),
		StatusText:  "initializing",
	}

	model := newWatchDashboardModel(
		fmt.Sprintf("Project: %s", projectName),
		[]watchTarget{target},
	)
	p := tea.NewProgram(
		model,
		tea.WithInput(inFile),
		tea.WithOutput(outFile),
		tea.WithAltScreen(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(errOut, "⚠️ watch UI exited with error: %v\n", err)
		return err
	}

	return nil
}

// launchProjectsWatchMode starts all launchpad projects in detached terminals
// and shows a combined monitoring dashboard.
func launchProjectsWatchMode(launchpadName string, projects []projectEntry, in io.Reader, out io.Writer, errOut io.Writer) error {
	if len(projects) == 0 {
		return errors.New("⚠️ launchpad has no projects")
	}

	fmt.Fprintf(out, "👀 Watch mode for launchpad '%s' (%d projects)\n", launchpadName, len(projects))

	targets := make([]watchTarget, 0, len(projects))
	startupErrors := make([]string, 0)

	for _, project := range projects {
		target, installCmd, runCmd, err := detectProjectRunner(project.Path)
		if err != nil {
			startupErrors = append(startupErrors, fmt.Sprintf("%s: %v", project.Name, err))
			continue
		}

		if len(installCmd) > 0 {
			fmt.Fprintf(out, "📦 Installing dependencies for %s...\n", project.Name)
			if err := runCommandInDir(project.Path, installCmd, out, errOut); err != nil {
				startupErrors = append(startupErrors, fmt.Sprintf("%s: install failed (%v)", project.Name, err))
				continue
			}
		}

		fmt.Fprintf(out, "🚀 Launching %s with %s\n", project.Name, target)
		pid, terminalName, err := startWatchTerminal(project.Path, runCmd)
		if err != nil {
			startupErrors = append(startupErrors, fmt.Sprintf("%s: %v", project.Name, err))
			continue
		}

		targets = append(targets, watchTarget{
			ProjectName: project.Name,
			Terminal:    terminalName,
			RootPID:     pid,
			StartedAt:   time.Now(),
			StatusText:  "initializing",
		})
	}

	if len(targets) == 0 {
		if len(startupErrors) == 0 {
			return errors.New("❌ no project could be launched in watch mode")
		}
		return fmt.Errorf("❌ failed to start watch mode (first error: %s)", startupErrors[0])
	}

	for _, msg := range startupErrors {
		fmt.Fprintf(errOut, "⚠️ %s\n", msg)
	}

	inFile, inOk := in.(*os.File)
	outFile, outOk := out.(*os.File)
	if !inOk || !outOk || !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		return errors.New("❌ watch mode requires an interactive terminal")
	}

	model := newWatchDashboardModel(
		fmt.Sprintf("Launchpad: %s", launchpadName),
		targets,
	)
	p := tea.NewProgram(
		model,
		tea.WithInput(inFile),
		tea.WithOutput(outFile),
		tea.WithAltScreen(),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(errOut, "⚠️ watch UI exited with error: %v\n", err)
		return err
	}

	return nil
}

// --- Terminal spawning ---

// startWatchTerminal opens a detached terminal and returns the PID of the
// process running the command. Currently Linux-only.
func startWatchTerminal(projectPath string, runCmd []string) (int, string, error) {
	if runtime.GOOS != "linux" {
		return 0, "", fmt.Errorf("❌ --watch with external terminal is currently supported on Linux only")
	}

	shellLine := fmt.Sprintf("cd %s && %s", shellQuote(projectPath), shellJoin(runCmd))
	terminals := []struct {
		name string
		args []string
	}{
		{name: "ghostty", args: []string{"-e", "bash", "-lc", shellLine}},
		{name: "kitty", args: []string{"bash", "-lc", shellLine}},
		{name: "alacritty", args: []string{"-e", "bash", "-lc", shellLine}},
		{name: "gnome-terminal", args: []string{"--", "bash", "-lc", shellLine}},
	}

	for _, termDef := range terminals {
		if !hasCommand(termDef.name) {
			continue
		}
		cmd := exec.Command(termDef.name, termDef.args...)
		if err := cmd.Start(); err != nil {
			continue
		}
		if cmd.Process == nil {
			continue
		}
		return cmd.Process.Pid, termDef.name, nil
	}

	return 0, "", errors.New("❌ watch mode could not open a terminal (tried: ghostty, kitty, alacritty, gnome-terminal)")
}

// --- Process tree stats ---

// collectProcessTreeStats walks the process tree rooted at rootPID and
// aggregates CPU and memory usage.
func collectProcessTreeStats(rootPID int) (processTreeStats, error) {
	samples, err := readProcessSamples()
	if err != nil {
		return processTreeStats{}, err
	}

	childrenByParent := make(map[int][]int, len(samples))
	sampleByPID := make(map[int]processSample, len(samples))
	for _, sample := range samples {
		sampleByPID[sample.PID] = sample
		childrenByParent[sample.ParentPID] = append(childrenByParent[sample.ParentPID], sample.PID)
	}

	if _, ok := sampleByPID[rootPID]; !ok {
		return processTreeStats{}, nil
	}

	queue := []int{rootPID}
	visited := make(map[int]bool)
	var totalCPU float64
	var totalRSS int
	count := 0

	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if visited[pid] {
			continue
		}
		visited[pid] = true

		sample, ok := sampleByPID[pid]
		if !ok {
			continue
		}
		count++
		totalCPU += sample.CPUPercent
		totalRSS += sample.RSSKB

		queue = append(queue, childrenByParent[pid]...)
	}

	return processTreeStats{
		ProcessCount:  count,
		CPUPercent:    normalizeCPUPercent(totalCPU),
		CPUPercentRaw: totalCPU,
		CPUCoresUsed:  totalCPU / 100.0,
		RSSKB:         totalRSS,
	}, nil
}

func normalizeCPUPercent(rawCPU float64) float64 {
	cores := float64(runtime.NumCPU())
	if cores <= 0 {
		cores = 1
	}
	normalized := rawCPU / cores
	if normalized < 0 {
		return 0
	}
	if normalized > 100 {
		return 100
	}
	return normalized
}

// readProcessSamples parses the output of `ps -eo pid=,ppid=,%cpu=,rss=`.
func readProcessSamples() ([]processSample, error) {
	out, err := exec.Command("ps", "-eo", "pid=,ppid=,%cpu=,rss=").Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	samples := make([]processSample, 0, len(lines))
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}

		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			continue
		}
		cpu, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			continue
		}
		rssKB, err := strconv.Atoi(fields[3])
		if err != nil {
			continue
		}

		samples = append(samples, processSample{
			PID:        pid,
			ParentPID:  ppid,
			CPUPercent: cpu,
			RSSKB:      rssKB,
		})
	}

	return samples, nil
}
