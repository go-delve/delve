//go:build !windows
// +build !windows

package proc_test

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/go-delve/delve/pkg/proc"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

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
