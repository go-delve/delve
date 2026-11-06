package debugger

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestDebuggerAttachExecutablePath(t *testing.T) {
	fixture := protest.BuildFixture(t, "cat", 0)
	// Use a different path so executable discovery cannot satisfy the test.
	path := filepath.Join(t.TempDir(), filepath.Base(fixture.Path))
	data, err := os.ReadFile(fixture.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0755); err != nil {
		t.Fatal(err)
	}
	backends := []string{"default"}
	if runtime.GOOS != "darwin" {
		// The native backend on macOS requires the optional macnative build tag.
		backends = append(backends, "native")
	}
	for _, backend := range backends {
		for _, scenario := range []string{"explicit", "missing"} {
			t.Run(backend+"/"+scenario, func(t *testing.T) {
				cmd := exec.Command(fixture.Path)
				stdout, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				stdin, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				defer func() {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}()

				output := make(chan string, 1)
				go func() {
					defer close(output)
					scanner := bufio.NewScanner(stdout)
					for scanner.Scan() {
						output <- scanner.Text()
					}
				}()
				checkOutput := func(input string) {
					t.Helper()
					if _, err := fmt.Fprintln(stdin, input); err != nil {
						t.Fatal(err)
					}
					select {
					case line := <-output:
						if want := fmt.Sprintf("read %q", input); line != want {
							t.Fatalf("target output = %q, want %q", line, want)
						}
					case <-time.After(10 * time.Second):
						t.Fatal("target did not respond")
					}
				}
				// In particular, wait for the Windows loader to finish before attaching.
				checkOutput("before attach")

				executable := path
				if scenario == "missing" {
					executable = filepath.Join(t.TempDir(), "missing-executable")
				}
				d := &Debugger{config: &Config{Backend: backend}}
				group, err := d.Attach(cmd.Process.Pid, executable, nil)
				if group != nil {
					defer func() {
						if err := group.Detach(false); err != nil {
							t.Error(err)
						}
					}()
				}
				if scenario == "missing" {
					var binaryErr *proc.ErrBadBinaryInfo
					if !errors.As(err, &binaryErr) || !errors.Is(binaryErr.Err, os.ErrNotExist) {
						t.Fatalf("expected missing executable error, got %v", err)
					}
					// This change preserves targets on native attach failure. LLDB's
					// existing initialization-error cleanup still kills the target.
					if runtime.GOOS != "darwin" {
						checkOutput("after failed attach")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if got := group.Selected.BinInfo().Images[0].Path; got != executable {
					t.Fatalf("executable path = %q, want %q", got, executable)
				}
			})
		}
	}
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
	if runtime.GOARCH == "loong64" && runtime.GOOS == "linux" {
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
	bins := [][]proc.Function{{}}
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
