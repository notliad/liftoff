package main

// General-purpose helpers shared across the CLI.

import (
	"os"
	"os/exec"
	"sort"
	"strings"
)

// fileExists returns true if path exists (file or directory).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// readFileOrEmpty reads the entire file at path, returning "" on error.
func readFileOrEmpty(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// hasCommand checks whether the given executable is available in $PATH.
func hasCommand(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

// sortedMapKeys returns the keys of any map[string]V in sorted order.
func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// truncateRunes truncates a string to max runes, appending "..." if needed.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

// --- Shell quoting helpers ---

// shellJoin joins command arguments with proper POSIX shell quoting.
func shellJoin(args []string) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// shellQuote wraps a string in single quotes, escaping embedded quotes.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `"'"'`) + "'"
}

// appleScriptEscape escapes a string for embedding in an AppleScript do-script command.
func appleScriptEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, `"`, `\\"`)
	return s
}

// windowsJoin joins command arguments with Windows cmd.exe quoting.
func windowsJoin(args []string) string {
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, windowsQuote(a))
	}
	return strings.Join(parts, " ")
}

// windowsQuote wraps a string in double quotes if it contains special characters.
func windowsQuote(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\"&|<>()^") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}
