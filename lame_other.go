//go:build !windows

package main

import "os/exec"

// quiet has cmd run as it is: only Windows gives it a window.
func quiet(*exec.Cmd) {}
