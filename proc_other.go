//go:build !windows

package main

import "os/exec"

// No console-window concept on non-Windows; nothing to hide.
func hideConsole(cmd *exec.Cmd) {}
