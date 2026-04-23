//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr configures cmd to start in a new session (Setsid).
// This ensures the child process is not part of lo's terminal session,
// so it won't receive SIGHUP when lo's controlling terminal closes,
// and it won't be killed by a Ctrl-C sent to lo's process group.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
