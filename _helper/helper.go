package helper

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/derekparker/dbg/proctl"
)

type testfunc func(p *proctl.DebuggedProcess)

func GetRegisters(p *proctl.DebuggedProcess, t *testing.T) *syscall.PtraceRegs {
	regs, err := p.Registers()
	if err != nil {
		t.Fatal("Registers():", err)
	}

	return regs
}

func WithTestProcess(name string, t *testing.T, fn testfunc) {
	runtime.LockOSThread()
	base, err := CompileTestProg(name)
	if err != nil {
		t.Fatalf("Could not compile %s due to %s", name, err)
	}
	defer os.Remove("./" + base)

	cmd, err := startTestProcess(base)
	if err != nil {
		t.Fatal("Starting test process:", err)
	}

	pid := cmd.Process.Pid
	p, err := proctl.NewDebugProcess(pid)
	if err != nil {
		t.Fatal("NewDebugProcess():", err)
	}

	defer cmd.Process.Kill()

	fn(p)
}

func CompileTestProg(source string) (string, error) {
	base := filepath.Base(source)
	return base, exec.Command("go", "build", "-gcflags=-N -l", "-o", base, source+".go").Run()
}

func startTestProcess(name string) (*exec.Cmd, error) {
	cmd := exec.Command("./" + name)

	return cmd, cmd.Start()
}
