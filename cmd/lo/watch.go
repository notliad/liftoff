package main

// Watch mode: resource-monitor footer pinned at the bottom of the project terminal.
//
// When watch mode is active the current terminal is split into two regions:
//   - Lines 1 .. height-3: a normal scrolling region where the project output appears
//   - Lines height-2 .. height: a fixed footer showing CPU, memory, and status (updated every 2 s)
//
// For launchpads, each project gets its own detached terminal window running
// "lo --_watch-inline <name> <path> <cmd...>", triggering this same inline
// behaviour in that window.

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

// watchHeaderLines is the number of lines reserved for the pinned header.
const watchHeaderLines = 3

// --- Inline watch (single project in current terminal) ---

// runInlineWatch runs a project in the current terminal while keeping a
// real-time resource strip pinned to the top watchHeaderLines lines.
// The project stdout/stderr flows normally into the scroll region below.
func runInlineWatch(projectName, projectPath string, runCmd []string) error {
	if len(runCmd) == 0 {
		return errors.New("empty command")
	}
	ttyFd := int(os.Stdout.Fd())
	if !term.IsTerminal(ttyFd) {
		// Not interactive: just run the project directly.
		return runCommandInDir(projectPath, runCmd, os.Stdout, os.Stderr)
	}
	if !hasCommand("ps") {
		return errors.New("watch mode requires ps command")
	}

	_, height, err := term.GetSize(ttyFd)
	if err != nil || height <= watchHeaderLines+2 {
		height = 24
	}

	// Clear screen, set scroll region to [1 .. height-watchHeaderLines], move
	// cursor to the top-left so project output starts there.
	fmt.Printf("\033[2J\033[1;%dr\033[1;1H", height-watchHeaderLines)

	cmd := exec.Command(runCmd[0], runCmd[1:]...)
	cmd.Dir = projectPath
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Start(); err != nil {
		resetScrollRegion(height)
		return fmt.Errorf("failed to start %s: %w", projectName, err)
	}

	pid := cmd.Process.Pid
	startedAt := time.Now()

	// Draw the initial footer before the project writes anything.
	drawInlineFooter(projectName, pid, startedAt, processTreeStats{}, "starting", height)

	stopCh := make(chan struct{})
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				stats, statsErr := collectProcessTreeStats(pid)
				status := "running"
				if statsErr != nil || stats.ProcessCount == 0 {
					status = "finishing"
				}
				drawInlineFooter(projectName, pid, startedAt, stats, status, height)
			case <-stopCh:
				return
			}
		}
	}()

	waitErr := cmd.Wait()
	close(stopCh)

	drawInlineFooter(projectName, pid, startedAt, processTreeStats{}, "finished", height)
	resetScrollRegion(height)
	return waitErr
}

// drawInlineFooter saves the cursor, jumps to the pinned footer region at the
// bottom of the terminal, redraws it, then restores the cursor.
func drawInlineFooter(projectName string, pid int, startedAt time.Time, stats processTreeStats, status string, termHeight int) {
	uptime := time.Since(startedAt).Round(time.Second)
	memMB := float64(stats.RSSKB) / 1024.0

	// Footer occupies the last watchHeaderLines rows:
	//   separatorLine : ─────────
	//   titleLine     : 🚀 name  uptime  pid
	//   statsLine     : cpu  mem  procs  status
	separatorLine := termHeight - watchHeaderLines + 1
	titleLine := separatorLine + 1
	statsLine := separatorLine + 2

	var b strings.Builder

	// Save cursor (DECSC).
	b.WriteString("\0337")

	// Separator.
	b.WriteString(fmt.Sprintf("\033[%d;1H\033[2K", separatorLine))
	b.WriteString(mutedStyle.Render(strings.Repeat("\u2500", 72)))

	// Title bar.
	b.WriteString(fmt.Sprintf("\033[%d;1H\033[2K", titleLine))
	b.WriteString(titleStyle.Render(fmt.Sprintf(" \U0001f680 %-22s", truncateRunes(projectName, 22))))
	b.WriteString(mutedStyle.Render(fmt.Sprintf("  uptime %-9s  pid %d", uptime, pid)))

	// Stats bar.
	b.WriteString(fmt.Sprintf("\033[%d;1H\033[2K", statsLine))
	var statusRendered string
	switch status {
	case "starting", "finishing":
		statusRendered = warnStyle.Render(status)
	case "finished":
		statusRendered = mutedStyle.Render(status)
	default:
		statusRendered = successStyle.Render(status)
	}
	b.WriteString(fmt.Sprintf("    %s  %s  %s  %s",
		mutedStyle.Render(fmt.Sprintf("cpu %5.1f%%", stats.CPUPercent)),
		mutedStyle.Render(fmt.Sprintf("mem %6.1f MB", memMB)),
		mutedStyle.Render(fmt.Sprintf("procs %d", stats.ProcessCount)),
		statusRendered,
	))

	// Restore cursor (DECRC).
	b.WriteString("\0338")

	fmt.Print(b.String())
}

// resetScrollRegion resets the scroll region to the full screen and moves the
// cursor to the bottom so the shell prompt appears in a clean spot.
func resetScrollRegion(height int) {
	// \033[r  -- DECSTBM with no args: restore full-screen scrolling
	fmt.Printf("\033[r\033[%d;1H\n", height)
}

// --- Entry point called by launchProject ---

// launchWithWatch opens a new terminal window for the project where the inline
// resource-monitor header is pinned at the top.
func launchWithWatch(projectPath, projectName string, runCmd []string, _ io.Reader, out io.Writer, errOut io.Writer) error {
	termName, err := startInlineWatchTerminal(projectName, projectPath, runCmd)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "   ↳ opened in %s\n", termName)
	return nil
}

// --- Launchpad watch mode ---

// launchProjectsWatchMode opens each project in its own terminal window, each
// running "lo --_watch-inline" to display the inline resource-monitor header.
func launchProjectsWatchMode(launchpadName string, projects []projectEntry, _ io.Reader, out io.Writer, errOut io.Writer) error {
	if len(projects) == 0 {
		return errors.New("launchpad has no projects")
	}

	fmt.Fprintf(out, "Watch mode -- launchpad '%s' (%d projects)\n\n", launchpadName, len(projects))

	launched := 0
	for _, project := range projects {
		target, installCmd, runCmd, err := detectProjectRunner(project.Path)
		if err != nil {
			fmt.Fprintf(errOut, "warning: %s: %v\n", project.Name, err)
			continue
		}

		if len(installCmd) > 0 {
			fmt.Fprintf(out, "Installing dependencies for %s...\n", project.Name)
			if err := runCommandInDir(project.Path, installCmd, out, errOut); err != nil {
				fmt.Fprintf(errOut, "warning: %s: install failed: %v\n", project.Name, err)
				continue
			}
		}

		fmt.Fprintf(out, "Launching %s with %s\n", project.Name, target)
		termName, err := startInlineWatchTerminal(project.Name, project.Path, runCmd)
		if err != nil {
			fmt.Fprintf(errOut, "warning: %s: %v\n", project.Name, err)
			continue
		}
		fmt.Fprintf(out, "  opened in %s\n", termName)
		launched++
	}

	if launched == 0 {
		return errors.New("no project could be launched in watch mode")
	}
	return nil
}

// startInlineWatchTerminal opens a new terminal window that runs
// "lo --_watch-inline <name> <path> <cmd...>".
func startInlineWatchTerminal(projectName, projectPath string, runCmd []string) (string, error) {
	if runtime.GOOS != "linux" {
		return "", errors.New("watch mode with external terminal is currently supported on Linux only")
	}

	loExe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("could not resolve lo executable: %w", err)
	}

	// Build: lo --_watch-inline <name> <path> <cmd...>
	allArgs := append([]string{loExe, "--_watch-inline", projectName, projectPath}, runCmd...)
	shellLine := shellJoin(allArgs)

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
		return termDef.name, nil
	}

	return "", errors.New("could not open a terminal (tried: ghostty, kitty, alacritty, gnome-terminal)")
}

// --- Process tree stats ---

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
