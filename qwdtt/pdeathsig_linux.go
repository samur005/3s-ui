package qwdtt

import (
	"os/exec"
	"syscall"
)

// setPdeathsig makes the kernel stop the server when the panel dies, so a
// crashed panel never leaves an orphan holding the DTLS port.
func setPdeathsig(cmd *exec.Cmd) {
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
