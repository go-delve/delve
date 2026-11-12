//go:build unix

package dap

import (
	"syscall"
	"testing"

	protest "github.com/go-delve/delve/pkg/proc/test"
)

func TestExecFixture_CleanupKillsProcess(t *testing.T) {
	var pid int
	t.Run("start", func(t *testing.T) {
		fixture := protest.BuildFixture(t, "loopprog", protest.AllNonOptimized)
		cmd := execFixture(t, fixture)
		pid = cmd.Process.Pid
		if err := syscall.Kill(pid, 0); err != nil {
			t.Fatalf("expected fixture process %d to be running: %v", pid, err)
		}
	})
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("fixture pid %d still alive after subtest cleanup", pid)
	}
}
