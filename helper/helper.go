package helper

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/derekparker/delve/proctl"
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

	p, err := proctl.Launch([]string{"./" + base})
	if err != nil {
		t.Fatal("Launch():", err)
	}

	defer p.Process.Kill()

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
