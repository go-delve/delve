// +build linux darwin

package proc

import (
	"bufio"
	"bytes"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	protest "github.com/derekparker/delve/pkg/proc/test"
)

// Checks that p.Pid() is the pid of fixture.
func checkPid(t *testing.T, p *Process, fixture protest.Fixture) {
	if p.Pid() <= 0 {
		t.Fatalf("pid is zero or negative: %d", p.Pid())
	}

	psauxbuf, err := exec.Command("/bin/ps", "aux").CombinedOutput()
	if err != nil {
		t.Fatalf("error calling ps: %v", err)
	}

	pidstr := strconv.Itoa(p.Pid())
	found := false
	scanner := bufio.NewScanner(bytes.NewReader(psauxbuf))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Index(line, pidstr) >= 0 && strings.Index(line, filepath.Base(fixture.Path)) >= 0 {
			found = true
			break
		}
	}

	if !found {
		t.Fatalf("pid of %s is not %d", filepath.Base(fixture.Path), p.Pid())
	}

	return
}

func TestIssue419(t *testing.T) {
	// SIGINT directed at the inferior should be passed along not swallowed by delve
	withTestProcess("issue419", t, func(p *Process, fixture protest.Fixture) {
		_, err := setFunctionBreakpoint(p, "main.main")
		assertNoError(err, t, "SetBreakpoint()")
		assertNoError(p.Continue(), t, "Continue()")
		checkPid(t, p, fixture)
		go func() {
			for {
				time.Sleep(1 * time.Second)
				if p.Running() {
					time.Sleep(2 * time.Second)
					err := syscall.Kill(p.Pid(), syscall.SIGINT)
					assertNoError(err, t, "syscall.Kill")
					return
				}
			}
		}()
		err = p.Continue()
		if _, exited := err.(ProcessExitedError); !exited {
			t.Fatalf("Unexpected error after Continue(): %v\n", err)
		}
	})
}
