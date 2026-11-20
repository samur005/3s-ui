//go:build !windows

package qwdtt

import (
	"os/exec"
	"syscall"
)

func setProcAttr(cmd *exec.Cmd) {
	// Own process group, so a signal aimed at the panel is not also delivered
	// to the server before the panel has torn the routing down.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	setPdeathsig(cmd)
}

func terminate(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
}

func kill(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
