package helper

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/derekparker/delve/proctl"
)

type testfunc func(p *proctl.DebuggedProcess)

func WithTestProcess(name string, t *testing.T, fn testfunc) {
	runtime.LockOSThread()
	base, err := compileTestProg(name)
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

func compileTestProg(source string) (string, error) {
	base := filepath.Base(source)
	return base, exec.Command("go", "build", "-gcflags=-N -l", "-o", base, source+".go").Run()
}
