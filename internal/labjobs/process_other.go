//go:build !linux

package labjobs

import (
	"errors"
	"os"
	"os/exec"
)

func prepare(command *exec.Cmd)         {}
func interrupt(command *exec.Cmd) error { return command.Process.Signal(os.Interrupt) }
func terminate(command *exec.Cmd) error { return command.Process.Kill() }

func lockHistory(root *os.Root) (*os.File, error) {
	return nil, errors.New("lab history locking requires Linux")
}
