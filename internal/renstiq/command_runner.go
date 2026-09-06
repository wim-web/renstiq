package renstiq

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// ExecRunner is the only post-command component aware of OS process semantics.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, spec CommandSpec) (int, error) {
	if len(spec.Args) == 0 {
		return -1, errors.New("command is empty")
	}
	cmd := exec.CommandContext(ctx, spec.Args[0], spec.Args[1:]...)
	cmd.Dir = spec.Dir
	cmd.Stdin = bytes.NewReader(spec.Input)
	cmd.Stdout, cmd.Stderr = spec.Output, spec.Output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), err
	}
	return -1, err
}
