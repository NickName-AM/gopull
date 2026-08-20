//go:build !unix

package puller

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup is a no-op: process groups are a Unix concept, so on other
// platforms only the git process itself can be signaled.
func setProcessGroup(*exec.Cmd) {}

// terminateGroup signals the git process alone. Anything it spawned has to
// notice on its own that its parent is gone.
func terminateGroup(p *os.Process) error {
	return p.Signal(syscall.SIGTERM)
}
