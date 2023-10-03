//go:build linux || darwin

package proc_test

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/native"
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
	withTestProcess("issue419", t, func(p *proc.Target, grp *proc.TargetGroup, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.main")
		assertNoError(grp.Continue(), t, "Continue()")
		resumeChan := make(chan struct{}, 1)
		go func() {
			time.Sleep(500 * time.Millisecond)
			<-resumeChan
			if p.Pid() <= 0 {
				// if we don't stop the inferior the test will never finish
				grp.RequestManualStop()
				err := grp.Detach(true)
				errChan <- errIssue419{pid: p.Pid(), err: err}
				return
			}
			err := syscall.Kill(p.Pid(), syscall.SIGINT)
			errChan <- errIssue419{pid: p.Pid(), err: err}
		}()
		grp.ResumeNotify(resumeChan)
		errChan <- grp.Continue()
	})

	for i := 0; i < 2; i++ {
		err := <-errChan

		t.Logf("error %T %#v\n", err, err)

		if v, ok := err.(errIssue419); ok {
			assertNoError(v.err, t, "syscall.Kill")
			continue
		}

		if _, exited := err.(proc.ErrProcessExited); !exited {
			t.Fatalf("Unexpected error after Continue(): %v\n", err)
		}
	}
}

func TestSignalDeath(t *testing.T) {
	if testBackend != "native" || runtime.GOOS != "linux" {
		t.Skip("skipped on non-linux non-native backends")
	}
	var buildFlags protest.BuildFlags
	if buildMode == "pie" {
		buildFlags |= protest.BuildModePIE
	}
	fixture := protest.BuildFixture("loopprog", buildFlags)
	cmd := exec.Command(fixture.Path)
	stdout, err := cmd.StdoutPipe()
	assertNoError(err, t, "StdoutPipe")
	cmd.Stderr = os.Stderr
	assertNoError(cmd.Start(), t, "starting fixture")
	p, err := native.Attach(cmd.Process.Pid, nil, []string{})
	assertNoError(err, t, "Attach")
	stdout.Close() // target will receive SIGPIPE later on
	err = p.Continue()
	t.Logf("error is %v", err)
	exitErr, isexited := err.(proc.ErrProcessExited)
	if !isexited {
		t.Fatal("did not exit")
	}
	if exitErr.Status != -int(unix.SIGPIPE) {
		t.Fatalf("expected SIGPIPE got %d\n", exitErr.Status)
	}
}
