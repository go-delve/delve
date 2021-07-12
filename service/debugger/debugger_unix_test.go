// +build !windows

package debugger

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/creack/pty"
	"github.com/go-delve/delve/pkg/gobuild"
	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service/api"
)

func TestDebugger_LaunchNoExecutablePerm(t *testing.T) {
	defer func() {
		os.Setenv("GOOS", runtime.GOOS)
		os.Setenv("GOARCH", runtime.GOARCH)
	}()
	fixturesDir := protest.FindFixturesDir()
	buildtestdir := filepath.Join(fixturesDir, "buildtest")
	debugname := "debug"
	switchOS := map[string]string{
		"darwin":  "linux",
		"windows": "linux",
		"freebsd": "windows",
		"linux":   "windows",
	}
	if runtime.GOARCH == "arm64" && runtime.GOOS == "linux" {
		os.Setenv("GOARCH", "amd64")
	}
	os.Setenv("GOOS", switchOS[runtime.GOOS])
	exepath := filepath.Join(buildtestdir, debugname)
	defer os.Remove(exepath)
	if err := gobuild.GoBuild(debugname, []string{buildtestdir}, fmt.Sprintf("-o %s", exepath)); err != nil {
		t.Fatalf("go build error %v", err)
	}
	if err := os.Chmod(exepath, 0644); err != nil {
		t.Fatal(err)
	}
	d := new(Debugger)
	_, err := d.Launch([]string{exepath}, nil, ".")
	if err == nil {
		t.Fatalf("expected error but none was generated")
	}
	if err != api.ErrNotExecutable {
		t.Fatalf("expected error \"%s\" got \"%v\"", api.ErrNotExecutable, err)
	}
}

func TestDebugger_LaunchWithTTY(t *testing.T) {
	if os.Getenv("CI") == "true" {
		if _, err := exec.LookPath("lsof"); err != nil {
			t.Skip("skipping test in CI, system does not contain lsof")
		}
	}
	// Ensure no env meddling is leftover from previous tests.
	os.Setenv("GOOS", runtime.GOOS)
	os.Setenv("GOARCH", runtime.GOARCH)

	p, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	defer tty.Close()

	fixturesDir := protest.FindFixturesDir()
	buildtestdir := filepath.Join(fixturesDir, "buildtest")
	debugname := "debugtty"
	exepath := filepath.Join(buildtestdir, debugname)
	if err := gobuild.GoBuild(debugname, []string{buildtestdir}, fmt.Sprintf("-o %s", exepath)); err != nil {
		t.Fatalf("go build error %v", err)
	}
	defer os.Remove(exepath)
	var backend string
	protest.DefaultTestBackend(&backend)
	conf := &Config{TTY: tty.Name(), Backend: backend}
	pArgs := []string{exepath}
	d, err := New(conf, pArgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("lsof", "-p", fmt.Sprintf("%d", d.ProcessPid()))
	result, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result, []byte(tty.Name())) {
		t.Fatal("process open file list does not contain expected tty")
	}
}
