//go:build windows
// +build windows

package proc_test

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-delve/delve/pkg/proc"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

func testGenRediretByFile(t *testing.T, fixture protest.Fixture, stdoutExpectFile string, stderrExpectFile string, errChan chan error) (redirect proc.Redirect, canceFunc func(), err error) {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return redirect, nil, err
	}

	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		return redirect, nil, err
	}

	redirect = proc.NewRedirectByFile([3]*os.File{nil, stderrReader, stdoutReader}, [3]*os.File{nil, stdoutWriter, stderrWriter})
	reader := func(mode string, f *os.File, expectFile string) {
		expect, err := os.ReadFile(filepath.Join(fixture.BuildDir, expectFile))
		if err != nil {
			errChan <- err
			return
		}

		out, err := io.ReadAll(f)
		if err != nil {
			errChan <- err
			return
		}

		if string(expect) != string(out) {
			errChan <- fmt.Errorf("%s,Not as expected!\nexpect:%s\nout:%s", mode, expect, out)
			return
		}
		errChan <- nil
	}

	canceFunc = func() {
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
	}

	go reader("stdout", stdoutReader, stdoutExpectFile)
	go reader("stderr", stderrReader, stderrExpectFile)

	return redirect, canceFunc, nil
}

func testGenRediretByPath(t *testing.T, fixture protest.Fixture, stdoutExpectFile string, stderrExpectFile string, errChan chan error) (redirect proc.Redirect, canceFunc func(), err error) {
	return proc.NewEmptyRedirectByPath(), nil, nil
}
