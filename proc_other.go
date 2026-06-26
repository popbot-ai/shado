//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// No console-window concept on non-Windows; nothing to hide.
func hideConsole(cmd *exec.Cmd) {}

// runHook runs an optional post-mount command (via /bin/sh on Unix) with the
// shadow's path exported as SHADO_MOUNT.
func runHook(hook, mount string) {
	if hook == "" {
		return
	}
	info("  running hook: %s", hook)
	cmd := exec.Command("sh", "-c", hook)
	cmd.Env = append(os.Environ(), "SHADO_MOUNT="+mount)
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("  hook failed: %v\n%s\n", err, string(out))
	} else if verbose && len(out) > 0 {
		os.Stdout.Write(out)
	}
}
