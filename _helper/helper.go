package helper

import (
	"os"
	"os/exec"
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
	err := CompileTestProg(name)
	if err != nil {
		t.Fatalf("Could not compile %s due to %s", name, err)
	}

	cmd, err := startTestProcess(name)
	if err != nil {
		t.Fatal("Starting test process:", err)
	}

	pid := cmd.Process.Pid
	p, err := proctl.NewDebugProcess(pid)
	if err != nil {
		t.Fatal("NewDebugProcess():", err)
	}
	defer func() {
		cmd.Process.Kill()
		os.Remove(name)
	}()

	fn(p)
}

func CompileTestProg(source string) error {
	return exec.Command("go", "build", "-gcflags=-N -l", "-o", source, source+".go").Run()
}

func startTestProcess(name string) (*exec.Cmd, error) {
	cmd := exec.Command(name)

	err := cmd.Start()
	if err != nil {
		return nil, err
	}

	return cmd, nil
}
