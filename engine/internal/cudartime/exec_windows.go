// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build windows

package cudartime

import (
	"os/exec"
	"syscall"
)

// hideConsole prevents black console flashes when spawning nvidia-smi / powershell
// from the GUI shell (CREATE_NO_WINDOW / HideWindow).
func hideConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}
