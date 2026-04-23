package main

// Cross-platform project launching and command execution.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// launchProject installs dependencies (if needed), then starts the project
// in a detached terminal or the current shell.
func launchProject(projectPath, projectName string, watchMode bool, in io.Reader, out io.Writer, errOut io.Writer) error {
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
	if watchMode {
		return launchWithWatch(projectPath, projectName, runCmd, in, out, errOut)
	}
	if err := launchCrossPlatform(projectPath, runCmd, out, errOut); err != nil {
		return err
	}
	return nil
}

// launchProjectsParallel starts multiple projects concurrently and collects errors.
func launchProjectsParallel(launchpadName string, projects []projectEntry, out io.Writer, errOut io.Writer) error {
	if len(projects) == 0 {
		return errors.New("⚠️ launchpad has no projects")
	}

	fmt.Fprintf(out, "🚀 Launchpad '%s' starting %d projects in parallel\n", launchpadName, len(projects))
	return launchBatch(projects, out, errOut)
}

// launchProjectsSequential starts projects in ordered batches.
// Projects with equal LaunchOrder are started simultaneously within a batch.
// All batches except the last are run in-process (piped stdout) so readiness
// can be detected before the next batch begins.  The final batch is launched
// normally in detached terminal windows.
func launchProjectsSequential(launchpadName string, groups [][]projectEntry, out io.Writer, errOut io.Writer) error {
	total := 0
	for _, g := range groups {
		total += len(g)
	}
	fmt.Fprintf(out, "🚀 Launchpad '%s': %d projects in %d sequential batches\n", launchpadName, total, len(groups))

	for i, group := range groups {
		isLast := i == len(groups)-1
		names := make([]string, len(group))
		for j, p := range group {
			names[j] = p.Name
		}
		fmt.Fprintf(out, "▶ Batch %d/%d: %s\n", i+1, len(groups), strings.Join(names, ", "))
		if err := launchBatchWithDetection(group, !isLast, out, errOut); err != nil {
			return err
		}
	}
	return nil
}

// launchBatchWithDetection starts all projects in group concurrently.
// When waitReady is true each project is started in-process (piped stdout) and
// the function blocks until every project signals readiness (or times out).
// When waitReady is false the projects are launched in detached terminal windows
// via the standard launchBatch path.
func launchBatchWithDetection(group []projectEntry, waitReady bool, out io.Writer, errOut io.Writer) error {
	if !waitReady {
		return launchBatch(group, out, errOut)
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	for _, p := range group {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			sig, err := waitForProjectReady(p, defaultReadyTimeout, out)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", p.Display, err))
				mu.Unlock()
				return
			}
			switch sig.How {
			case "port":
				fmt.Fprintf(out, "  ✅ %s ready on :%d (%.1fs)\n", p.Name, sig.Port, sig.Took.Seconds())
			case "text":
				fmt.Fprintf(out, "  ✅ %s ready (%.1fs)\n", p.Name, sig.Took.Seconds())
			default:
				fmt.Fprintf(out, "  ⚠️  %s still starting after %.0fs, continuing\n", p.Name, sig.Took.Seconds())
			}
		}()
	}

	wg.Wait()
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// launchBatch starts all projects in the slice concurrently.
func launchBatch(projects []projectEntry, out io.Writer, errOut io.Writer) error {
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
			if err := launchProject(project.Path, project.Name, false, os.Stdin, out, errOut); err != nil {
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
	return fmt.Errorf("❌ batch error: %v", errs[0])
}

// groupByLaunchOrder groups project entries by their LaunchOrder field.
// The returned slice is ordered from lowest to highest order number.
func groupByLaunchOrder(entries []projectEntry) [][]projectEntry {
	if len(entries) == 0 {
		return nil
	}
	ordered := make(map[int][]projectEntry)
	for _, e := range entries {
		o := e.LaunchOrder
		if o < 1 {
			o = 1
		}
		ordered[o] = append(ordered[o], e)
	}
	keys := make([]int, 0, len(ordered))
	for k := range ordered {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	groups := make([][]projectEntry, len(keys))
	for i, k := range keys {
		groups[i] = ordered[k]
	}
	return groups
}

// launchCrossPlatform opens a platform-appropriate terminal window to run the command.
// Falls back to the current shell when no supported terminal is found.
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

// launchInTerminalWithLog opens a platform-appropriate terminal window that
// runs the command while simultaneously writing all output to logPath via tee.
// The terminal window remains visible to the user after lo finishes.
func launchInTerminalWithLog(projectPath string, runCmd []string, logPath string, out io.Writer, errOut io.Writer) error {
	inner := fmt.Sprintf("cd %s && %s 2>&1 | tee %s",
		shellQuote(projectPath), shellJoin(runCmd), shellQuote(logPath))

	switch runtime.GOOS {
	case "linux":
		terminals := []struct {
			name string
			args []string
		}{
			{name: "ghostty", args: []string{"-e", "bash", "-lc", inner}},
			{name: "kitty", args: []string{"bash", "-lc", inner}},
			{name: "alacritty", args: []string{"-e", "bash", "-lc", inner}},
			{name: "gnome-terminal", args: []string{"--", "bash", "-lc", inner}},
		}
		for _, term := range terminals {
			if !hasCommand(term.name) {
				continue
			}
			cmd := exec.Command(term.name, term.args...)
			if err := cmd.Start(); err == nil {
				_ = cmd.Process.Release()
				return nil
			}
		}
		// No terminal found: run in-process, output only goes to log.
		return runCommandInDir(projectPath, runCmd, out, errOut)
	case "darwin":
		if hasCommand("osascript") {
			script := fmt.Sprintf(`tell application "Terminal" to do script "%s"`, appleScriptEscape(inner))
			if err := exec.Command("osascript", "-e", script).Start(); err == nil {
				return nil
			}
		}
		return runCommandInDir(projectPath, runCmd, out, errOut)
	case "windows":
		// On Windows use PowerShell's Tee-Object.
		psLine := fmt.Sprintf("cd '%s'; %s 2>&1 | Tee-Object -FilePath '%s'",
			projectPath, windowsJoin(runCmd), logPath)
		cmd := exec.Command("powershell", "-NoLogo", "-Command",
			fmt.Sprintf("Start-Process powershell -ArgumentList '-NoLogo','-Command','%s'", psLine))
		if err := cmd.Start(); err == nil {
			_ = cmd.Process.Release()
			return nil
		}
		return runCommandInDir(projectPath, runCmd, out, errOut)
	default:
		return runCommandInDir(projectPath, runCmd, out, errOut)
	}
}

// runCommandInDir executes a command synchronously in the given directory.
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
