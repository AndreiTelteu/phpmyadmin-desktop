//go:build windows

package runtime

import (
	"os/exec"
	"syscall"
)

// hideProcessWindow prevents FrankenPHP, a console-subsystem executable, from
// flashing a terminal window when it is launched by the GUI application.
func hideProcessWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
