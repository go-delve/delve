package debugger

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-delve/delve/pkg/gobuild"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
	protest "github.com/go-delve/delve/pkg/proc/test"
	"github.com/go-delve/delve/service/api"
)

func TestMain(m *testing.M) {
	var logConf string
	flag.StringVar(&logConf, "log", "", "configures logging")
	flag.Parse()
	logflags.Setup(logConf != "", logConf, "")
	protest.RunTestsWithFixtures(m)
}

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
	if runtime.GOARCH == "riscv64" && runtime.GOOS == "linux" {
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

func guessSubstitutePathHelper(t *testing.T, args *api.GuessSubstitutePathIn, fnpaths [][2]string, tgt map[string]string) {
	const base = 0x40000
	t.Helper()
	bins := [][]proc.Function{[]proc.Function{}}
	for i, fnpath := range fnpaths {
		bins[0] = append(bins[0], proc.Function{Name: fnpath[0], Entry: uint64(base + i)})
	}
	out := guessSubstitutePath(args, bins, func(_ int, fn *proc.Function) string {
		return fnpaths[fn.Entry-base][1]
	})
	t.Logf("%#v\n", out)
	if len(out) != len(tgt) {
		t.Errorf("wrong number of entries")
		return
	}
	for k := range out {
		if out[k] != tgt[k] {
			t.Errorf("mismatch for directory %q", k)
			return
		}
	}
}

func TestGuessSubstitutePathMinimalMain(t *testing.T) {
	// When the main module only contains a single package check that its mapping still works
	guessSubstitutePathHelper(t,
		&api.GuessSubstitutePathIn{
			ImportPathOfMainPackage: "github.com/ccampo133/go-docker-alpine-remote-debug",
			ClientGOROOT:            "/user/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.23.0.linux-amd64",
			ClientModuleDirectories: map[string]string{
				"github.com/ccampo133/go-docker-alpine-remote-debug": "/user/gohome/go-docker-alpine-remote-debug",
			},
		},
		[][2]string{
			{"main.main", "/app/main.go"},
			{"main.hello", "/app/main.go"},
			{"runtime.main", "/usr/local/go/src/runtime/main.go"}},
		map[string]string{
			"/usr/local/go": "/user/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.23.0.linux-amd64",
			"/app":          "/user/gohome/go-docker-alpine-remote-debug"})
}
