//go:build windows

package relay

import "os/exec"

// Windows has no Pdeathsig: a hard RouterLLM kill can orphan the bun child
// until it is killed manually. Normal service shutdown uses Manager.Stop.
func configureSysProcAttr(cmd *exec.Cmd) {}
