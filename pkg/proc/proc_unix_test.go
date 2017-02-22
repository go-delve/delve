// +build linux darwin

package proc

import (
	"syscall"
	"testing"
	"time"

	protest "github.com/derekparker/delve/pkg/proc/test"
)

func TestIssue419(t *testing.T) {
	// SIGINT directed at the inferior should be passed along not swallowed by delve
	withTestProcess("issue419", t, func(p *Process, fixture protest.Fixture) {
		go func() {
			for {
				if p.Running() {
					time.Sleep(2 * time.Second)
					err := syscall.Kill(p.pid, syscall.SIGINT)
					assertNoError(err, t, "syscall.Kill")
					return
				}
			}
		}()
		err := Continue(p)
		if _, exited := err.(ProcessExitedError); !exited {
			t.Fatalf("Unexpected error after Continue(): %v\n", err)
		}
	})
}
