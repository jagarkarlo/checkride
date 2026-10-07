package labjobs

import (
	"os/exec"
	"syscall"
)

func prepare(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func interrupt(command *exec.Cmd) error { return syscall.Kill(-command.Process.Pid, syscall.SIGINT) }
func terminate(command *exec.Cmd) error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }
