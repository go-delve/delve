//go:build linux || darwin
// +build linux darwin

package proc_test

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
	withTestProcess("issue419", t, func(p *proc.Target, fixture protest.Fixture) {
		grp := proc.NewGroup(p)
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
	p, err := native.Attach(cmd.Process.Pid, []string{})
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

func testGenRedireByPath(t *testing.T, fixture protest.Fixture, stdoutExpectFile string, stderrExpectFile string, errChan chan error) (redirect proc.Redirect, canceFunc func(), err error) {
	var (
		stdoutPath = "./stdout"
		stderrPath = "./stderr"
	)

	if err = syscall.Mkfifo(stdoutPath, 0o600); err != nil {
		return redirect, nil, err
	}

	if err = syscall.Mkfifo(stderrPath, 0o600); err != nil {
		os.Remove(stdoutPath)
		return redirect, nil, err
	}

	redirect = proc.NewRedirectByPath([3]string{"", stdoutPath, stderrPath})
	reader := func(mode string, path string, expectFile string) {
		defer os.Remove(path)
		outFile, err := os.OpenFile(path, os.O_RDONLY, os.ModeNamedPipe)
		if err != nil {
			errChan <- err
			return
		}

		expect, err := os.ReadFile(filepath.Join(fixture.BuildDir, expectFile))
		if err != nil {
			errChan <- err
			return
		}

		out, err := io.ReadAll(outFile)
		if err != nil {
			errChan <- err
			return
		}

		if string(expect) == string(out) {
			errChan <- fmt.Errorf("%s,Not as expected!\nexpect:%s\nout:%s", mode, expect, out)
			return
		}
		errChan <- nil
	}

	canceFunc = func() {
		stdout, err := os.OpenFile(stdoutPath, os.O_WRONLY, os.ModeNamedPipe)
		if err == nil {
			stdout.Close()
		}
		stderr, err := os.OpenFile(stderrPath, os.O_WRONLY, os.ModeNamedPipe)
		if err == nil {
			stderr.Close()
		}
	}

	go reader("stdout", stdoutPath, stdoutExpectFile)
	go reader("stderr", stderrPath, stderrExpectFile)

	return redirect, canceFunc, nil
}

func testGenRedireByFile(t *testing.T, fixture protest.Fixture, stdoutExpectFile string, stderrExpectFile string, errChan chan error) (redirect proc.Redirect, canceFunc func(), err error) {
	return proc.NewEmptyRedirectByFile(), nil, nil
}
