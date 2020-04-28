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

func TestDebugger_LaunchNoMain(t *testing.T) {
	fixturesDir := protest.FindFixturesDir()
	nomaindir := filepath.Join(fixturesDir, "nomaindir")
	debugname := "debug"
	exepath := filepath.Join(nomaindir, debugname)
	defer os.Remove(exepath)
	if err := gobuild.GoBuild(debugname, []string{nomaindir}, fmt.Sprintf("-o %s", exepath)); err != nil {
		t.Fatalf("go build error %v", err)
	}

	d := new(Debugger)
	_, err := d.Launch([]string{exepath}, ".")
	if err == nil {
		t.Fatalf("expected error but none was generated")
	}
	if err != api.ErrNotExecutable {
		t.Fatalf("expected error \"%v\" got \"%v\"", api.ErrNotExecutable, err)
	}
}

func TestDebugger_LaunchInvalidFormat(t *testing.T) {
	goos := os.Getenv("GOOS")
	goarch := os.Getenv("GOARCH")
	defer func() {
		// restore environment values
		os.Setenv("GOOS", goos)
		os.Setenv("GOARCH", goarch)
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
	if err := gobuild.GoBuild(debugname, []string{buildtestdir}, fmt.Sprintf("-o %s", exepath)); err != nil {
		t.Fatalf("go build error %v", err)
	}
	defer os.Remove(exepath)

	d := new(Debugger)
	_, err := d.Launch([]string{exepath}, ".")
	if err == nil {
		t.Fatalf("expected error but none was generated")
	}
	if err != api.ErrNotExecutable {
		t.Fatalf("expected error \"%s\" got \"%v\"", api.ErrNotExecutable, err)
	}
}
