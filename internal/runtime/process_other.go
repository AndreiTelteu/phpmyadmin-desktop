//go:build !windows

package runtime

import "os/exec"

// hideProcessWindow is a no-op off Windows.
func hideProcessWindow(cmd *exec.Cmd) {}
