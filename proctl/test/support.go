package test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// RunTestsWithFixtures will pre-compile test fixtures before running test
// methods. Test binaries are deleted before exiting.
func RunTestsWithFixtures(m *testing.M) {
	// Find the fixtures directory; this is necessary because the test file's
	// nesting level can vary. Only look up to a maxdepth of 10 (which seems
	// like a sane alternative to recursion).
	parent := ".."
	fixturesDir := filepath.Join(parent, "_fixtures")
	for depth := 0; depth < 10; depth++ {
		if _, err := os.Stat(fixturesDir); err == nil {
			break
		}
		fixturesDir = filepath.Join(parent, fixturesDir)
	}
	if _, err := os.Stat(fixturesDir); err != nil {
		fmt.Println("Couldn't locate fixtures directory")
		os.Exit(1)
	}

	// Collect all files which look like fixture source files.
	sources, err := ioutil.ReadDir(fixturesDir)
	if err != nil {
		fmt.Printf("Couldn't read fixtures dir: %v\n", err)
		os.Exit(1)
	}

	// Compile the fixtures.
	for _, src := range sources {
		if src.IsDir() {
			continue
		}
		// Make a (good enough) random temporary file name
		r := make([]byte, 4)
		rand.Read(r)
		path := filepath.Join(fixturesDir, src.Name())
		name := strings.TrimSuffix(src.Name(), filepath.Ext(src.Name()))
		tmpfile := filepath.Join(os.TempDir(), fmt.Sprintf("%s.%s", name, hex.EncodeToString(r)))

		// Build the test binary
		if err := exec.Command("go", "build", "-gcflags=-N -l", "-o", tmpfile, path).Run(); err != nil {
			fmt.Printf("Error compiling %s: %s\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("Compiled test binary %s: %s\n", name, tmpfile)

		source, _ := filepath.Abs(path)
		Fixtures[name] = Fixture{Name: name, Path: tmpfile, Source: source}
	}

	status := m.Run()

	// Remove the fixtures.
	// TODO(danmace): Not sure why yet, but doing these removes in a defer isn't
	// working.
	for _, f := range Fixtures {
		os.Remove(f.Path)
	}

	os.Exit(status)
}
