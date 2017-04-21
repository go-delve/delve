package test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Fixture is a test binary.
type Fixture struct {
	// Name is the short name of the fixture.
	Name string
	// Path is the absolute path to the test binary.
	Path string
	// Source is the absolute path of the test binary source.
	Source string
}

// Fixtures is a map of Fixture.Name to Fixture.
var Fixtures map[string]Fixture = make(map[string]Fixture)

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

func BuildFixture(name string) Fixture {
	if f, ok := Fixtures[name]; ok {
		return f
	}

	fixturesDir := FindFixturesDir()

	// Make a (good enough) random temporary file name
	r := make([]byte, 4)
	rand.Read(r)
	dir := fixturesDir
	path := filepath.Join(fixturesDir, name+".go")
	if name[len(name)-1] == '/' {
		dir = filepath.Join(dir, name)
		path = ""
		name = name[:len(name)-1]
	}
	tmpfile := filepath.Join(os.TempDir(), fmt.Sprintf("%s.%s", name, hex.EncodeToString(r)))

	buildFlags := []string{"build"}
	if runtime.GOOS == "windows" {
		// Work-around for https://github.com/golang/go/issues/13154
		buildFlags = append(buildFlags, "-ldflags=-linkmode internal")
	}
	buildFlags = append(buildFlags, "-gcflags=-N -l", "-o", tmpfile)
	if path != "" {
		buildFlags = append(buildFlags, name+".go")
	}

	cmd := exec.Command("go", buildFlags...)
	cmd.Dir = dir

	// Build the test binary
	if err := cmd.Run(); err != nil {
		fmt.Printf("Error compiling %s: %s\n", path, err)
		os.Exit(1)
	}

	source, _ := filepath.Abs(path)
	source = filepath.ToSlash(source)

	Fixtures[name] = Fixture{Name: name, Path: tmpfile, Source: source}
	return Fixtures[name]
}

// RunTestsWithFixtures will pre-compile test fixtures before running test
// methods. Test binaries are deleted before exiting.
func RunTestsWithFixtures(m *testing.M) int {
	status := m.Run()

	// Remove the fixtures.
	for _, f := range Fixtures {
		os.Remove(f.Path)
	}
	return status
}

var recordingAllowed = map[string]bool{}
var recordingAllowedMu sync.Mutex

// testName returns the name of the test being run using runtime.Caller.
// On go1.8 t.Name() could be called instead, this is a workaround to
// support <=go1.7
func testName(t testing.TB) string {
	for i := 1; i < 10; i++ {
		pc, _, _, ok := runtime.Caller(i)
		if !ok {
			break
		}
		fn := runtime.FuncForPC(pc)
		if fn == nil {
			continue
		}
		name := fn.Name()
		v := strings.Split(name, ".")
		if strings.HasPrefix(v[len(v)-1], "Test") {
			return name
		}
	}
	return "unknown"
}

// AllowRecording allows the calling test to be used with a recording of the
// fixture.
func AllowRecording(t testing.TB) {
	recordingAllowedMu.Lock()
	defer recordingAllowedMu.Unlock()
	name := testName(t)
	t.Logf("enabling recording for %s", name)
	recordingAllowed[name] = true
}

// MustHaveRecordingAllowed skips this test if recording is not allowed
//
// Not all the tests can be run with a recording:
// - some fixtures never terminate independently (loopprog,
//   testnextnethttp) and can not be recorded
// - some tests assume they can interact with the target process (for
//   example TestIssue419, or anything changing the value of a variable),
//   which we can't do on with a recording
// - some tests assume that the Pid returned by the process is valid, but
//   it won't be at replay time
// - some tests will start the fixture but not never execute a single
//   instruction, for some reason rr doesn't like this and will print an
//   error if it happens
// - many tests will assume that we can return from a runtime.Breakpoint,
//   with a recording this is not possible because when the fixture ran it
//   wasn't attached to a debugger and in those circumstances a
//   runtime.Breakpoint leads directly to a crash
//
// Some of the tests using runtime.Breakpoint (anything involving variable
// evaluation and TestWorkDir) have been adapted to work with a recording.
func MustHaveRecordingAllowed(t testing.TB) {
	recordingAllowedMu.Lock()
	defer recordingAllowedMu.Unlock()
	name := testName(t)
	if !recordingAllowed[name] {
		t.Skipf("recording not allowed for %s", name)
	}
}

// SafeRemoveAll removes dir and its contents but only as long as dir does
// not contain directories.
func SafeRemoveAll(dir string) {
	dh, err := os.Open(dir)
	if err != nil {
		return
	}
	defer dh.Close()
	fis, err := dh.Readdir(-1)
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
