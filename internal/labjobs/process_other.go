//go:build !linux

package labjobs

import (
	"os"
	"os/exec"
)

func prepare(command *exec.Cmd)         {}
func interrupt(command *exec.Cmd) error { return command.Process.Signal(os.Interrupt) }
func terminate(command *exec.Cmd) error { return command.Process.Kill() }
