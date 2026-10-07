package labjobs

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func prepare(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func interrupt(command *exec.Cmd) error { return syscall.Kill(-command.Process.Pid, syscall.SIGINT) }
func terminate(command *exec.Cmd) error { return syscall.Kill(-command.Process.Pid, syscall.SIGKILL) }

func lockHistory(root *os.Root) (*os.File, error) {
	file, err := root.OpenFile(".manager.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		err = errors.New("history lock must be a private regular file")
	}
	if err == nil {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}
