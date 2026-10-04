package main

import (
	"os/exec"
	"syscall"
)

// quiet has cmd run without a console window of its own.
func quiet(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
