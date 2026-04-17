package main

// Cross-platform project launching and command execution.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
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
	return fmt.Errorf("❌ launchpad completed with %d errors (first: %v)", len(errs), errs[0])
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
