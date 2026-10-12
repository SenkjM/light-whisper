// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

//go:build !windows

package cudartime

import "os/exec"

func hideConsole(cmd *exec.Cmd) {}
