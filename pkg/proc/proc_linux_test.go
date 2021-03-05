package proc_test

import (
	"os"
	"path/filepath"
	"testing"

	exec "golang.org/x/sys/execabs"

	"github.com/go-delve/delve/pkg/proc/native"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

func TestLoadingExternalDebugInfo(t *testing.T) {
	fixture := protest.BuildFixture("locationsprog", 0)
	defer os.Remove(fixture.Path)
	stripAndCopyDebugInfo(fixture, t)
	p, err := native.Launch(append([]string{fixture.Path}, ""), "", 0, []string{filepath.Dir(fixture.Path)}, "", [3]string{})
	if err != nil {
		t.Fatal(err)
	}
	p.Detach(true)
}

func stripAndCopyDebugInfo(f protest.Fixture, t *testing.T) {
	name := filepath.Base(f.Path)
	// Copy the debug information to an external file.
	copyCmd := exec.Command("objcopy", "--only-keep-debug", name, name+".debug")
	copyCmd.Dir = filepath.Dir(f.Path)
	if err := copyCmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Strip the original binary of the debug information.
	stripCmd := exec.Command("strip", "--strip-debug", "--strip-unneeded", name)
	stripCmd.Dir = filepath.Dir(f.Path)
	if err := stripCmd.Run(); err != nil {
		t.Fatal(err)
	}
}
