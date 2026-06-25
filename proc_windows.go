//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// CREATE_NO_WINDOW: the child console process runs without allocating/showing a
// console window. Essential when shado is spawned by a GUI app (PopBot/Electron)
// or runs elevated — otherwise each powershell/robocopy child flashes a window.
const createNoWindow = 0x08000000

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
