// +build linux darwin

package debug_test

import (
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/go-delve/delve/pkg/debug"
	"github.com/go-delve/delve/pkg/proc"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

type errIssue419 struct {
	pid int
	err error
}

func (npe errIssue419) Error() string {
	return fmt.Sprintf("Pid is zero or negative: %d", npe.pid)
}

func TestIssue419(t *testing.T) {
	if testBackend == "rr" {
		return
	}

	errChan := make(chan error, 2)

	// SIGINT directed at the inferior should be passed along not swallowed by delve
	withTestTarget("issue419", t, func(tgt *debug.Target, fixture protest.Fixture) {
		defer close(errChan)
		setFunctionBreakpoint(tgt, t, "main.main")
		assertNoError(tgt.Continue(), t, "Continue()")
		resumeChan := make(chan struct{}, 1)
		go func() {
			time.Sleep(500 * time.Millisecond)
			<-resumeChan
			if tgt.Pid() <= 0 {
				// if we don't stop the inferior the test will never finish
				tgt.RequestManualStop()
				err := tgt.Detach(true)
				errChan <- errIssue419{pid: tgt.Pid(), err: err}
				return
			}
			err := syscall.Kill(tgt.Pid(), syscall.SIGINT)
			errChan <- errIssue419{pid: tgt.Pid(), err: err}
		}()
		tgt.ResumeNotify(resumeChan)
		errChan <- tgt.Continue()
	})

	for i := 0; i < 2; i++ {
		err := <-errChan

		if v, ok := err.(errIssue419); ok {
			assertNoError(v.err, t, "syscall.Kill")
			continue
		}

		if _, exited := err.(proc.ErrProcessExited); !exited {
			t.Fatalf("Unexpected error after Continue(): %v\n", err)
		}
	}
}
