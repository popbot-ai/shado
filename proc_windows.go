//go:build windows

package main

import (
	"fmt"
	"os"
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

// runHook runs an optional post-mount command (PowerShell on Windows) with the
// shadow's path exported as SHADO_MOUNT.
func runHook(hook, mount string) {
	if hook == "" {
		return
	}
	info("  running hook: %s", hook)
	cmd := exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", hook)
	cmd.Env = append(os.Environ(), "SHADO_MOUNT="+mount)
	hideConsole(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("  hook failed: %v\n%s\n", err, string(out))
	} else if verbose && len(out) > 0 {
		os.Stdout.Write(out)
	}
}
