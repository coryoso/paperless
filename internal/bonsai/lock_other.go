//go:build !darwin && !linux

package bonsai

import (
	"errors"
	"os/exec"
)

func lockInstall(string) (func(), error) {
	return nil, errors.New("automatic Bonsai installation is unavailable on this platform")
}

func configureInstallCommand(cmd *exec.Cmd) {}
