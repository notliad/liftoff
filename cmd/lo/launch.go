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

// resolveProjectRunner returns the target label, optional install command, run command,
// and working directory for the given project. in/out are forwarded for interactive
// package-manager selection when no lockfile is present.
func resolveProjectRunner(project projectEntry, in io.Reader, out io.Writer) (target string, installCmd []string, runCmd []string, runDir string, err error) {
	target, installCmd, runCmd, err = detectProjectRunner(project.Path, project.Variant, project.ScriptOverride, in, out)
	runDir = project.Path
	return
}

// launchProject installs dependencies (if needed), then starts the project
// in a detached terminal or the current shell.
func launchProject(project projectEntry, watchMode bool, in io.Reader, out io.Writer, errOut io.Writer, cfg *config) error {
	target, installCmd, runCmd, runDir, err := resolveProjectRunner(project, in, out)
	if err != nil {
		return err
	}

	if len(installCmd) > 0 {
		fmt.Fprintf(out, "📦 Installing dependencies for %s...\n", project.Name)
		if err := runCommandInDir(runDir, installCmd, out, errOut); err != nil {
			return fmt.Errorf("dependency installation failed (%s): %w", strings.Join(installCmd, " "), err)
		}
		updateLockHash(runDir)
	}

	fmt.Fprintf(out, "🚀 Launching %s with %s\n", project.Name, target)
	if watchMode {
		return launchWithWatch(runDir, project.Name, runCmd, in, out, errOut, cfg)
	}
	if err := launchCrossPlatform(runDir, runCmd, out, errOut, cfg); err != nil {
		return err
	}
	return nil
}

// launchProjectsParallel starts multiple projects concurrently and collects errors.
func launchProjectsParallel(launchpadName string, projects []projectEntry, out io.Writer, errOut io.Writer, cfg *config) error {
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
			if err := launchProject(project, false, os.Stdin, out, errOut, cfg); err != nil {
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

// launchWithTmux runs a command in a new tmux window (tab) or vertical split pane.
// Returns true if tmux was available and the launch was attempted.
func launchWithTmux(projectPath string, runCmd []string, tmuxTarget string) bool {
	if !hasCommand("tmux") {
		return false
	}
	if os.Getenv("TMUX") == "" {
		return false
	}

	shellLine := withErrorPause(
		fmt.Sprintf("cd %s && %s", shellQuote(projectPath), shellJoin(runCmd)),
	)

	switch tmuxTarget {
	case "pane":
		exec.Command("tmux", "split-window", "-h", shellLine).Start()
	default:
		exec.Command("tmux", "new-window", shellLine).Start()
	}
	return true
}

// launchCrossPlatform opens a platform-appropriate terminal window to run the command.
// Falls back to the current shell when no supported terminal is found.
// If tmux is configured and available, it uses tmux instead.
func launchCrossPlatform(projectPath string, runCmd []string, out io.Writer, errOut io.Writer, cfg *config) error {
	if cfg != nil && cfg.UseTmux {
		if launchWithTmux(projectPath, runCmd, cfg.TmuxTarget) {
			fmt.Fprintln(out, "   ↳ launched in tmux")
			return nil
		}
	}

	shellLine := withErrorPause(
		fmt.Sprintf("cd %s && %s", shellQuote(projectPath), shellJoin(runCmd)),
	)

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

// withErrorPause wraps a POSIX shell command so that if the process exits with
// a non-zero code the terminal window stays open long enough for the user to
// read the error output. On clean exit the window closes normally.
func withErrorPause(shellCmd string) string {
	return shellCmd +
		"; _lo_ec=$?;" +
		" if [ \"$_lo_ec\" -ne 0 ]; then" +
		" printf '\\n\\033[31m\u274c Process exited with code %d\\033[0m\\n' \"$_lo_ec\";" +
		" read -rp 'Press Enter to close... ';" +
		" fi"
}
