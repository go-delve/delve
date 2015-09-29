package test

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	path := filepath.Join(fixturesDir, name+".go")
	tmpfile := filepath.Join(os.TempDir(), fmt.Sprintf("%s.%s", name, hex.EncodeToString(r)))

	// Build the test binary
	if err := exec.Command("go", "build", "-gcflags=-N -l", "-o", tmpfile, path).Run(); err != nil {
		fmt.Printf("Error compiling %s: %s\n", path, err)
		os.Exit(1)
	}

	source, _ := filepath.Abs(path)
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
