package proc_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/native"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

func mustHaveObjcopy(t *testing.T) {
	t.Helper()
	if objcopyPath, _ := exec.LookPath("objcopy"); objcopyPath == "" {
		t.Skip("no objcopy in path")
	}
}

func TestLoadingExternalDebugInfo(t *testing.T) {
	mustHaveObjcopy(t)
	fixture := protest.BuildFixture("locationsprog", 0)
	defer os.Remove(fixture.Path)
	stripAndCopyDebugInfo(fixture, t)
	p, err := native.Launch(append([]string{fixture.Path}, ""), "", 0, []string{filepath.Dir(fixture.Path)}, "", "", proc.OutputRedirect{}, proc.OutputRedirect{})
	if err != nil {
		t.Fatal(err)
	}
	p.Detach(true)
}

func TestGnuDebuglink(t *testing.T) {
	mustHaveObjcopy(t)
	// build math.go and make a copy of the executable
	fixture := protest.BuildFixture("math", 0)
	buf, err := os.ReadFile(fixture.Path)
	assertNoError(err, t, "ReadFile")
	debuglinkPath := fixture.Path + "-gnu_debuglink"
	assertNoError(os.WriteFile(debuglinkPath, buf, 0666), t, "WriteFile")
	defer os.Remove(debuglinkPath)

	run := func(exe string, args ...string) {
		cmd := exec.Command(exe, args...)
		out, err := cmd.CombinedOutput()
		assertNoError(err, t, fmt.Sprintf("%s %q: %s", cmd, strings.Join(args, " "), out))
	}

	// convert the executable copy to use .gnu_debuglink
	debuglinkDwoPath := debuglinkPath + ".dwo"
	run("objcopy", "--only-keep-debug", debuglinkPath, debuglinkDwoPath)
	defer os.Remove(debuglinkDwoPath)
	run("objcopy", "--strip-debug", debuglinkPath)
	run("objcopy", "--add-gnu-debuglink="+debuglinkDwoPath, debuglinkPath)

	// open original executable
	normalBinInfo := proc.NewBinaryInfo(runtime.GOOS, runtime.GOARCH)
	assertNoError(normalBinInfo.LoadBinaryInfo(fixture.Path, 0, []string{"/debugdir"}), t, "LoadBinaryInfo (normal exe)")

	// open .gnu_debuglink executable
	debuglinkBinInfo := proc.NewBinaryInfo(runtime.GOOS, runtime.GOARCH)
	assertNoError(debuglinkBinInfo.LoadBinaryInfo(debuglinkPath, 0, []string{"/debugdir"}), t, "LoadBinaryInfo (gnu_debuglink exe)")

	if len(normalBinInfo.Functions) != len(debuglinkBinInfo.Functions) {
		t.Fatalf("function list mismatch")
	}

	for i := range normalBinInfo.Functions {
		normalFn := normalBinInfo.Functions[i]
		debuglinkFn := debuglinkBinInfo.Functions[i]
		if normalFn.Entry != debuglinkFn.Entry || normalFn.Name != debuglinkFn.Name {
			t.Fatalf("function definition mismatch")
		}
	}
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
