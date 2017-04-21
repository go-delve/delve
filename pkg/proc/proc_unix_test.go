// +build linux darwin

package proc_test

import (
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/derekparker/delve/pkg/proc"
	protest "github.com/derekparker/delve/pkg/proc/test"
	"github.com/derekparker/delve/pkg/target"
)

func TestIssue419(t *testing.T) {
	if testBackend == "lldb" && runtime.GOOS == "darwin" {
		// debugserver bug?
		return
	}
	// SIGINT directed at the inferior should be passed along not swallowed by delve
	withTestProcess("issue419", t, func(p target.Interface, fixture protest.Fixture) {
		_, err := setFunctionBreakpoint(p, "main.main")
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(proc.Continue(p), t, "Continue()")
		go func() {
			for {
				time.Sleep(500 * time.Millisecond)
				if p.Running() {
					time.Sleep(2 * time.Second)
					if p.Pid() <= 0 {
						// if we don't stop the inferior the test will never finish
						p.RequestManualStop()
						p.Kill()
						t.Fatalf("Pid is zero or negative: %d", p.Pid())
						return
					}
					err := syscall.Kill(p.Pid(), syscall.SIGINT)
					assertNoError(err, t, "syscall.Kill")
					return
				}
			}
		}()
		err = proc.Continue(p)
		if _, exited := err.(proc.ProcessExitedError); !exited {
			t.Fatalf("Unexpected error after Continue(): %v\n", err)
		}
	})
}
