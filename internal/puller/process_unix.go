//go:build unix

package puller

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup puts cmd in a new process group of its own, so that the
// whole git process tree can be signaled as a unit. It also detaches the tree
// from the terminal's foreground process group: a Ctrl-C no longer reaches
// git directly, and every interrupt arrives through cmd.Cancel instead.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// terminateGroup sends SIGTERM to the process group led by p, reaching the
// fetch and merge that "git pull" spawns as well as the wrapper itself.
func terminateGroup(p *os.Process) error {
	// The group id equals the leader's pid, because setProcessGroup asked
	// for a new group at fork time.
	if err := syscall.Kill(-p.Pid, syscall.SIGTERM); err != nil {
		// The group may already be gone, or (on a kernel that refused
		// setpgid) never have existed; fall back to the process alone.
		return p.Signal(syscall.SIGTERM)
	}
	return nil
}
