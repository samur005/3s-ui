//go:build !linux && !windows

package qwdtt

import "os/exec"

func setPdeathsig(*exec.Cmd) {}
