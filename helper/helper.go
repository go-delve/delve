package helper

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/derekparker/delve/proctl"
)

func WithTestProcess(name string, t *testing.T, fn func(p *proctl.DebuggedProcess)) {
	runtime.LockOSThread()
	base := filepath.Base(name)
	if err := exec.Command("go", "build", "-gcflags=-N -l", "-o", base, name+".go").Run(); err != nil {
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
