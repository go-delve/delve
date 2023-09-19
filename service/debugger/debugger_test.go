package debugger

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
		t.Setenv("GOARCH", "amd64")
	}
	if runtime.GOARCH == "ppc64le" && runtime.GOOS == "linux" {
		t.Setenv("GOARCH", "amd64")
	}
	t.Setenv("GOOS", switchOS[runtime.GOOS])
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
		t.Fatalf("expected error %q got \"%v\"", api.ErrNotExecutable, err)
	}
}

func TestDebugger_LaunchCurrentDir(t *testing.T) {
	fixturesDir := protest.FindFixturesDir()
	testDir := filepath.Join(fixturesDir, "buildtest")
	debugname := "debug"
	exepath := filepath.Join(testDir, debugname)
	originalPath, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(originalPath)
	defer func() {
		if err := os.Remove(exepath); err != nil {
			t.Fatalf("error removing executable %v", err)
		}
	}()
	if err := gobuild.GoBuild(debugname, []string{testDir}, fmt.Sprintf("-o %s", exepath)); err != nil {
		t.Fatalf("go build error %v", err)
	}

	os.Chdir(testDir)

	d := new(Debugger)
	d.config = &Config{}
	_, err = d.Launch([]string{debugname}, ".")
	if err == nil {
		t.Fatal("expected error but none was generated")
	}
	if err != nil && !strings.Contains(err.Error(), "unknown backend") {
		t.Fatal(err)
	}
}
