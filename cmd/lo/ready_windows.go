//go:build windows

package main

import (
	"os/exec"
)

// setSysProcAttr is a no-op on Windows; process detachment is handled
// by the CREATE_NEW_CONSOLE flag set in launchCrossPlatform.
func setSysProcAttr(cmd *exec.Cmd) {}
