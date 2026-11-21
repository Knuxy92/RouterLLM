//go:build !windows

package relay

import (
	"os/exec"
	"syscall"
)

// configureSysProcAttr gives the relay child SIGTERM when the RouterLLM
// process dies, so restarts don't leak orphaned bun children holding ports.
// Spawn must run on a locked OS thread (see startProc): Pdeathsig fires when
// the parent *thread* dies, and the Go runtime migrates goroutines freely.
func configureSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
