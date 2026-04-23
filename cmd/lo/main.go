// Package main implements the `lo` CLI — a fast, cross-platform project launcher.
//
// lo scans configured workspace directories, detects project runtimes and
// frameworks, and launches them in detached terminal windows. It also supports
// launchpads (named project groups) and a real-time watch-mode dashboard.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

const version = "0.4.3"

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run is the testable entry point — parses flags and dispatches to the
// appropriate workflow (launch, list, pad, config, watch, etc.).
func run(args []string, in io.Reader, out io.Writer, errOut io.Writer) error {
	// Hidden subcommand used by watch terminals for inline monitoring.
	// Syntax: lo --_watch-inline <projectName> <projectPath> <cmd...>
	if len(args) >= 4 && args[0] == "--_watch-inline" {
		return runInlineWatch(args[1], args[2], args[3:])
	}

	fs := flag.NewFlagSet("lo", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	showHelp := fs.Bool("help", false, "show help")
	showHelpShort := fs.Bool("h", false, "show help")
	showVersion := fs.Bool("version", false, "show version")
	showVersionShort := fs.Bool("v", false, "show version")
	cfgMode := fs.Bool("config", false, "open config menu")
	cfgModeShort := fs.Bool("c", false, "open config menu")
	padMode := fs.Bool("pad", false, "launchpad mode")
	padModeShort := fs.Bool("p", false, "launchpad mode")
	listMode := fs.Bool("list", false, "list mode")
	listModeShort := fs.Bool("l", false, "list mode")
	watchMode := fs.Bool("watch", false, "watch mode")
	watchModeShort := fs.Bool("w", false, "watch mode")

	if err := fs.Parse(args); err != nil {
		writeUsage(errOut)
		return fmt.Errorf("❌ %w", err)
	}

	if *showHelp || *showHelpShort {
		writeUsage(out)
		return nil
	}
	if *showVersion || *showVersionShort {
		fmt.Fprintf(out, "lo %s\n", version)
		return nil
	}

	isConfigMode := *cfgMode || *cfgModeShort
	isPadMode := *padMode || *padModeShort
	isListMode := *listMode || *listModeShort
	isWatchMode := *watchMode || *watchModeShort

	cfgPath, legacyPath, err := configPaths()
	if err != nil {
		return fmt.Errorf("❌ could not resolve config path: %w", err)
	}

	cfg, err := loadOrInitConfig(cfgPath, legacyPath, in, out)
	if err != nil {
		return err
	}
	if err := migrateLegacyLaunchpads(cfgPath, &cfg, out); err != nil {
		return err
	}

	if isConfigMode {
		return runConfigMenu(cfgPath, &cfg, in, out)
	}

	remaining := fs.Args()
	if isPadMode && isListMode {
		padName := ""
		if len(remaining) > 1 {
			return errors.New("❌ usage: lo --pad --list [name]")
		}
		if len(remaining) == 1 {
			padName = strings.TrimSpace(remaining[0])
		}
		return listLaunchpadsFlow(cfg, padName, out)
	}

	projectDirs := effectiveProjectDirs(cfg)
	fmt.Fprintf(out, "\n🚀 Liftoff\n\n")

	projectEntries, err := listProjects(projectDirs)
	if err != nil {
		return err
	}
	if len(projectEntries) == 0 {
		return fmt.Errorf("⚠️ no projects found in %s", strings.Join(projectDirs, ", "))
	}

	if isPadMode {
		padName := ""
		if len(remaining) > 1 {
			return errors.New("❌ usage: lo --pad [name] [--list]")
		}
		if len(remaining) == 1 {
			padName = strings.TrimSpace(remaining[0])
		}

		if isListMode {
			return listLaunchpadsFlow(cfg, padName, out)
		}

		if padName == "" {
			return errors.New("❌ launchpad name required: lo --pad <name>")
		}
		return runLaunchpadFlow(cfgPath, &cfg, projectEntries, padName, isWatchMode, in, out, errOut)
	}

	if isListMode {
		if len(remaining) > 0 {
			return errors.New("❌ usage: lo --list")
		}
		listProjectsFlow(projectEntries, out)
		return nil
	}

	if len(remaining) > 1 {
		return errors.New("❌ usage: lo [project-name]")
	}

	query := ""
	if len(remaining) == 1 {
		query = strings.TrimSpace(remaining[0])
	}

	project, err := chooseProject(projectEntries, query, in, out)
	if err != nil {
		return err
	}

	return launchProject(project.Path, project.Name, isWatchMode, in, out, errOut)
}

func writeUsage(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	fmt.Fprintln(w, "Usage:")

	fmt.Fprintln(tw, "  lo [project-name]\tlaunch a project")
	fmt.Fprintln(tw, "  lo --list, -l\tlist projects")
	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "  lo --pad [name]\tcreate/run a launchpad*")
	fmt.Fprintln(tw, "  lo --pad --list [name]\tlist launchpads")
	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "  lo --config, -c\topen config menu (edit directories and launchpads)")
	fmt.Fprintln(tw, "  lo --watch, -w\trun in watch mode and monitors project resources")
	fmt.Fprintln(tw, "  lo --version, -v\tdisplay version")
	fmt.Fprintln(tw, "  lo --help, -h\tshow this :)")
	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "\n* A launchpad is a named group of projects that can be started together.")

	tw.Flush()
	fmt.Fprintln(w, "")
}
