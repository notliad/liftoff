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

const version = "0.4.1"

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run is the testable entry point — parses flags and dispatches to the
// appropriate workflow (launch, list, pad, config, watch, etc.).
func run(args []string, in io.Reader, out io.Writer, errOut io.Writer) error {
	fs := flag.NewFlagSet("lo", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	showHelp := fs.Bool("help", false, "show help")
	showHelpShort := fs.Bool("h", false, "show help")
	showVersion := fs.Bool("version", false, "show version")
	showVersionShort := fs.Bool("v", false, "show version")
	editConfig := fs.Bool("edit", false, "edit projects dir")
	editConfigShort := fs.Bool("e", false, "edit")
	printConfig := fs.Bool("print-config", false, "print config")
	printConfigShort := fs.Bool("c", false, "print config")
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

	isEdit := *editConfig || *editConfigShort
	isPrintConfig := *printConfig || *printConfigShort
	isPadMode := *padMode || *padModeShort
	isListMode := *listMode || *listModeShort
	isWatchMode := *watchMode || *watchModeShort

	cfgPath, legacyPath, err := configPaths()
	if err != nil {
		return fmt.Errorf("❌ could not resolve config path: %w", err)
	}
	if isEdit && !isPadMode {
		existingCfg, _ := loadConfig(cfgPath, legacyPath)
		cfg, err := promptProjectsDir(existingCfg, in, out)
		if err != nil {
			return err
		}
		cfg.Launchpads = existingCfg.Launchpads
		if err := saveConfig(cfgPath, cfg); err != nil {
			return fmt.Errorf("❌ failed saving config: %w", err)
		}
		fmt.Fprintf(out, "✅ Saved config to %s\n", cfgPath)
		return nil
	}

	cfg, err := loadOrInitConfig(cfgPath, legacyPath, in, out)
	if err != nil {
		return err
	}
	if err := migrateLegacyLaunchpads(cfgPath, &cfg, out); err != nil {
		return err
	}

	if isPrintConfig {
		dirs := effectiveProjectDirs(cfg)
		fmt.Fprintf(out, "📁 %s\n", strings.Join(dirs, ", "))
		return nil
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
			return errors.New("❌ usage: lo --pad [name] [--edit|--list]")
		}
		if len(remaining) == 1 {
			padName = strings.TrimSpace(remaining[0])
		}

		if isListMode {
			return listLaunchpadsFlow(cfg, padName, out)
		}

		if isEdit {
			return editLaunchpadFlow(cfgPath, &cfg, projectEntries, padName, in, out)
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
	fmt.Fprintln(tw, "  lo --pad --edit [name]\tedit a launchpad")
	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "  lo --edit, -e\tchange projects directories")
	fmt.Fprintln(tw, "  lo --watch, -w\trun in watch mode and monitors project resources")
	fmt.Fprintln(tw, "  lo --print-config, -c\tdisplay current directories")
	fmt.Fprintln(tw, "  lo --version, -v\tdisplay version")
	fmt.Fprintln(tw, "  lo --help, -h\tshow this :)")
	fmt.Fprintln(tw, "")
	fmt.Fprintln(tw, "\n* A launchpad is a named group of projects that can be started together.")

	tw.Flush()
	fmt.Fprintln(w, "")
}
