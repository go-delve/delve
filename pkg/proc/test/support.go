package test

import (
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/go-delve/delve/pkg/goversion"
)

// EnableRace allows to configure whether the race detector is enabled on target process.
var EnableRace = flag.Bool("racetarget", false, "Enables race detector on inferior process")

var runningWithFixtures bool

// Fixture is a test binary.
type Fixture struct {
	// Name is the short name of the fixture.
	Name string
	// Path is the absolute path to the test binary.
	Path string
	// Source is the absolute path of the test binary source.
	Source string
	// BuildDir is the directory where the build command was run.
	BuildDir string
}

// FixtureKey holds the name and builds flags used for a test fixture.
type fixtureKey struct {
	Name  string
	Flags BuildFlags
}

// Fixtures is a map of fixtureKey{ Fixture.Name, buildFlags } to Fixture.
var fixtures = make(map[fixtureKey]Fixture)

// PathsToRemove is a list of files and directories to remove after running all the tests
var PathsToRemove []string

// FindFixturesDir will search for the directory holding all test fixtures
// beginning with the current directory and searching up 10 directories.
func FindFixturesDir() string {
	parent := ".."
	fixturesDir := "_fixtures"
	for depth := 0; depth < 10; depth++ {
		if _, err := os.Stat(fixturesDir); err == nil {
			break
		}
		fixturesDir = filepath.Join(parent, fixturesDir)
	}
	return fixturesDir
}

// BuildFlags used to build fixture.
type BuildFlags uint32

const (
	// LinkStrip enables '-ldflags="-s"'.
	LinkStrip BuildFlags = 1 << iota
	// EnableCGOOptimization will build CGO code with optimizations.
	EnableCGOOptimization
	// EnableInlining will build a binary with inline optimizations turned on.
	EnableInlining
	// EnableOptimization will build a binary with default optimizations.
	EnableOptimization
	// EnableDWZCompression will enable DWZ compression of DWARF sections.
	EnableDWZCompression
	BuildModePIE
	BuildModePlugin
	BuildModeExternalLinker
	AllNonOptimized
	// LinkDisableDWARF enables '-ldflags="-w"'.
	LinkDisableDWARF
)

// TempFile makes a (good enough) random temporary file name
func TempFile(name string) string {
	r := make([]byte, 4)
	rand.Read(r)
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s.%s", name, hex.EncodeToString(r)))
}

// BuildFixture will compile the fixture 'name' using the provided build flags.
func BuildFixture(name string, flags BuildFlags) Fixture {
	if !runningWithFixtures {
		panic("RunTestsWithFixtures not called")
	}
	fk := fixtureKey{name, flags}
	if f, ok := fixtures[fk]; ok {
		return f
	}

	if flags&EnableCGOOptimization == 0 {
		if os.Getenv("CI") == "" || os.Getenv("CGO_CFLAGS") == "" {
			os.Setenv("CGO_CFLAGS", "-O0 -g")
		}
	}

	fixturesDir := FindFixturesDir()

	dir := fixturesDir
	path := filepath.Join(fixturesDir, name+".go")
	if name[len(name)-1] == '/' {
		dir = filepath.Join(dir, name)
		path = ""
		name = name[:len(name)-1]
	}
	tmpfile := TempFile(name)

	buildFlags := []string{"build"}
	var ver goversion.GoVersion
	if ver, _ = goversion.Parse(runtime.Version()); runtime.GOOS == "windows" && ver.Major > 0 && !ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 9, Rev: -1}) {
		// Work-around for https://github.com/golang/go/issues/13154
		buildFlags = append(buildFlags, "-ldflags=-linkmode internal")
	}
	ldflagsv := []string{}
	if flags&LinkStrip != 0 {
		ldflagsv = append(ldflagsv, "-s")
	}
	if flags&LinkDisableDWARF != 0 {
		ldflagsv = append(ldflagsv, "-w")
	}
	buildFlags = append(buildFlags, "-ldflags="+strings.Join(ldflagsv, " "))
	gcflagsv := []string{}
	if flags&EnableInlining == 0 {
		gcflagsv = append(gcflagsv, "-l")
	}
	if flags&EnableOptimization == 0 {
		gcflagsv = append(gcflagsv, "-N")
	}
	var gcflags string
	if flags&AllNonOptimized != 0 {
		gcflags = "-gcflags=all=" + strings.Join(gcflagsv, " ")
	} else {
		gcflags = "-gcflags=" + strings.Join(gcflagsv, " ")
	}
	buildFlags = append(buildFlags, gcflags, "-o", tmpfile)
	if *EnableRace {
		buildFlags = append(buildFlags, "-race")
	}
	if flags&BuildModePIE != 0 {
		buildFlags = append(buildFlags, "-buildmode=pie")
	} else {
		buildFlags = append(buildFlags, "-buildmode=exe")
	}
	if flags&BuildModePlugin != 0 {
		buildFlags = append(buildFlags, "-buildmode=plugin")
	}
	if flags&BuildModeExternalLinker != 0 {
		buildFlags = append(buildFlags, "-ldflags=-linkmode=external")
	}
	if ver.IsDevel() || ver.AfterOrEqual(goversion.GoVersion{Major: 1, Minor: 11, Rev: -1}) {
		if flags&EnableDWZCompression != 0 {
			buildFlags = append(buildFlags, "-ldflags=-compressdwarf=false")
		}
	}
	if path != "" {
		buildFlags = append(buildFlags, name+".go")
	}

	cmd := exec.Command("go", buildFlags...)
	cmd.Dir = dir
	if os.Getenv("CI") != "" {
		cmd.Env = os.Environ()
	}

	// Build the test binary
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Printf("Error compiling %s: %s\n", path, err)
		fmt.Printf("%s\n", string(out))
		os.Exit(1)
	}

	if flags&EnableDWZCompression != 0 {
		cmd := exec.Command("dwz", tmpfile)
		if out, err := cmd.CombinedOutput(); err != nil {
			if regexp.MustCompile(`dwz: Section offsets in (.*?) not monotonically increasing`).FindString(string(out)) == "" {
				fmt.Printf("Error running dwz on %s: %s\n", tmpfile, err)
				fmt.Printf("%s\n", string(out))
				os.Exit(1)
			}
		}
	}

	source, _ := filepath.Abs(path)
	source = filepath.ToSlash(source)
	sympath, err := filepath.EvalSymlinks(source)
	if err == nil {
		source = strings.ReplaceAll(sympath, "\\", "/")
	}

	absdir, _ := filepath.Abs(dir)

	fixture := Fixture{Name: name, Path: tmpfile, Source: source, BuildDir: absdir}

	fixtures[fk] = fixture
	return fixtures[fk]
}

// RunTestsWithFixtures will pre-compile test fixtures before running test
// methods. Test binaries are deleted before exiting.
func RunTestsWithFixtures(m *testing.M) int {
	runningWithFixtures = true
	defer func() {
		runningWithFixtures = false
	}()
	status := m.Run()

	// Remove the fixtures.
	for _, f := range fixtures {
		os.Remove(f.Path)
	}

	for _, p := range PathsToRemove {
		fi, err := os.Stat(p)
		if err != nil {
			panic(err)
		}
		if fi.IsDir() {
			SafeRemoveAll(p)
		} else {
			os.Remove(p)
		}
	}
	return status
}

var recordingAllowed = map[string]bool{}
var recordingAllowedMu sync.Mutex

// AllowRecording allows the calling test to be used with a recording of the
// fixture.
func AllowRecording(t testing.TB) {
	recordingAllowedMu.Lock()
	defer recordingAllowedMu.Unlock()
	name := t.Name()
	t.Logf("enabling recording for %s", name)
	recordingAllowed[name] = true
}

// MustHaveRecordingAllowed skips this test if recording is not allowed
//
// Not all the tests can be run with a recording:
//   - some fixtures never terminate independently (loopprog,
//     testnextnethttp) and can not be recorded
//   - some tests assume they can interact with the target process (for
//     example TestIssue419, or anything changing the value of a variable),
//     which we can't do on with a recording
//   - some tests assume that the Pid returned by the process is valid, but
//     it won't be at replay time
//   - some tests will start the fixture but not never execute a single
//     instruction, for some reason rr doesn't like this and will print an
//     error if it happens
//   - many tests will assume that we can return from a runtime.Breakpoint,
//     with a recording this is not possible because when the fixture ran it
//     wasn't attached to a debugger and in those circumstances a
//     runtime.Breakpoint leads directly to a crash
//
// Some of the tests using runtime.Breakpoint (anything involving variable
// evaluation and TestWorkDir) have been adapted to work with a recording.
func MustHaveRecordingAllowed(t testing.TB) {
	recordingAllowedMu.Lock()
	defer recordingAllowedMu.Unlock()
	name := t.Name()
	if !recordingAllowed[name] {
		t.Skipf("recording not allowed for %s", name)
	}
}

// SafeRemoveAll removes dir and its contents but only as long as dir does
// not contain directories.
func SafeRemoveAll(dir string) {
	fis, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, fi := range fis {
		if fi.IsDir() {
			return
		}
	}
	for _, fi := range fis {
		if err := os.Remove(filepath.Join(dir, fi.Name())); err != nil {
			return
		}
	}
	os.Remove(dir)
}

// MustSupportFunctionCalls skips this test if function calls are
// unsupported on this backend/architecture pair.
func MustSupportFunctionCalls(t *testing.T, testBackend string) {
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 11) {
		t.Skip("this version of Go does not support function calls")
	}

	if runtime.GOOS == "darwin" && testBackend == "native" {
		t.Skip("this backend does not support function calls")
	}

	if runtime.GOARCH == "386" {
		t.Skip(fmt.Errorf("%s does not support FunctionCall for now", runtime.GOARCH))
	}
	if runtime.GOARCH == "arm64" {
		if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 19) || runtime.GOOS == "windows" {
			t.Skip("this version of Go does not support function calls")
		}
	}

	if runtime.GOARCH == "ppc64le" {
		if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 22) {
			t.Skip("On PPC64LE Building with Go lesser than 1.22 does not support function calls")
		}
	}
}

// DefaultTestBackend changes the value of testBackend to be the default
// test backend for the OS, if testBackend isn't already set.
func DefaultTestBackend(testBackend *string) {
	if *testBackend != "" {
		return
	}
	*testBackend = os.Getenv("PROCTEST")
	if *testBackend != "" {
		return
	}
	if runtime.GOOS == "darwin" {
		*testBackend = "lldb"
	} else {
		*testBackend = "native"
	}
}

// WithPlugins builds the fixtures in plugins as plugins and returns them.
// The test calling WithPlugins will be skipped if the current combination
// of OS, architecture and version of GO doesn't support plugins or
// debugging plugins.
func WithPlugins(t *testing.T, flags BuildFlags, plugins ...string) []Fixture {
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 12) {
		t.Skip("versions of Go before 1.12 do not include debug information in packages that import plugin (or they do but it's wrong)")
	}
	if runtime.GOOS != "linux" {
		t.Skip("only supported on linux")
	}

	r := make([]Fixture, len(plugins))
	for i := range plugins {
		r[i] = BuildFixture(plugins[i], flags|BuildModePlugin)
	}
	return r
}

var hasCgo = func() bool {
	out, err := exec.Command("go", "env", "CGO_ENABLED").CombinedOutput()
	if err != nil {
		panic(err)
	}
	if strings.TrimSpace(string(out)) != "1" {
		return false
	}
	_, err1 := exec.LookPath("gcc")
	_, err2 := exec.LookPath("clang")
	return (err1 == nil) || (err2 == nil)
}()

func MustHaveCgo(t *testing.T) {
	if !hasCgo {
		t.Skip("Cgo not enabled")
	}
}

func RegabiSupported() bool {
	// Tracks regabiSupported variable in ParseGOEXPERIMENT internal/buildcfg/exp.go
	switch {
	case goversion.VersionAfterOrEqual(runtime.Version(), 1, 18):
		return runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" || runtime.GOARCH == "ppc64le" || runtime.GOARCH == "ppc64"
	case goversion.VersionAfterOrEqual(runtime.Version(), 1, 17):
		return runtime.GOARCH == "amd64" && (runtime.GOOS == "android" || runtime.GOOS == "linux" || runtime.GOOS == "darwin" || runtime.GOOS == "windows")
	default:
		return false
	}
}
