// +build !windows

package debugger

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-delve/delve/pkg/gobuild"
	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service/api"
)

func TestDebugger_LaunchNoExecutablePerm(t *testing.T) {
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
	if err := gobuild.GoBuild(debugname, []string{buildtestdir}, fmt.Sprintf("-o %s", exepath)); err != nil {
		t.Fatalf("go build error %v", err)
	}
	defer os.Remove(exepath)
	if err := os.Chmod(exepath, 0644); err != nil {
		t.Fatal(err)
	}
	d := new(Debugger)
	_, err := d.Launch([]string{exepath}, ".")
	if err == nil {
		t.Fatalf("expected error but none was generated")
	}
	if err != api.ErrNotExecutable {
		t.Fatalf("expected error \"%s\" got \"%v\"", api.ErrNotExecutable, err)
	}
}
