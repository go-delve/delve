// Package gobuild provides utilities for building programs and tests
// for the debugging session.
package gobuild

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/go-delve/delve/pkg/config"
)

// Remove the file at path and issue a warning to stderr if this fails.
// This can be used to remove the temporary binary generated for the session.
func Remove(path string) {
	var err error
	for i := 0; i < 20; i++ {
		err = os.Remove(path)
		// Open files can be removed on Unix, but not on Windows, where there also appears
		// to be a delay in releasing the binary when the process exits.
		// Leaving temporary files behind can be annoying to users, so we try again.
		if err == nil || runtime.GOOS != "windows" {
			break
		}
		time.Sleep(1 * time.Millisecond)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not remove %v: %v\n", path, err)
	}
}

// GoBuild builds non-test files in 'pkgs' with the specified 'buildflags'
// and writes the output at 'debugname'.
func GoBuild(debugname string, pkgs []string, buildflags string) error {
	args := goBuildArgs(debugname, pkgs, buildflags, false)
	return gocommandRun("build", args...)
}

// GoBuildCombinedOutput builds non-test files in 'pkgs' with the specified 'buildflags'
// and writes the output at 'debugname'.
func GoBuildCombinedOutput(debugname string, pkgs []string, buildflags string) (string, []byte, error) {
	args := goBuildArgs(debugname, pkgs, buildflags, false)
	return gocommandCombinedOutput("build", args...)
}

// GoTestBuild builds test files 'pkgs' with the specified 'buildflags'
// and writes the output at 'debugname'.
func GoTestBuild(debugname string, pkgs []string, buildflags string) error {
	args := goBuildArgs(debugname, pkgs, buildflags, true)
	return gocommandRun("test", args...)
}

// GoTestBuildCombinedOutput builds test files 'pkgs' with the specified 'buildflags'
// and writes the output at 'debugname'.
func GoTestBuildCombinedOutput(debugname string, pkgs []string, buildflags string) (string, []byte, error) {
	args := goBuildArgs(debugname, pkgs, buildflags, true)
	return gocommandCombinedOutput("test", args...)
}

func goBuildArgs(debugname string, pkgs []string, buildflags string, isTest bool) []string {
	args := []string{"-o", debugname}
	if isTest {
		args = append([]string{"-c"}, args...)
	}
	args = append(args, "-gcflags", "all=-N -l")
	if buildflags != "" {
		args = append(args, config.SplitQuotedFields(buildflags, '\'')...)
	}
	args = append(args, pkgs...)
	return args
}

func gocommandRun(command string, args ...string) error {
	_, goBuild := gocommandExecCmd(command, args...)
	goBuild.Stderr = os.Stdout
	goBuild.Stdout = os.Stderr
	return goBuild.Run()
}

func gocommandCombinedOutput(command string, args ...string) (string, []byte, error) {
	buildCmd, goBuild := gocommandExecCmd(command, args...)
	out, err := goBuild.CombinedOutput()
	return buildCmd, out, err
}

func gocommandExecCmd(command string, args ...string) (string, *exec.Cmd) {
	allargs := []string{command}
	allargs = append(allargs, args...)
	goBuild := exec.Command("go", allargs...)
	return strings.Join(append([]string{"go"}, allargs...), " "), goBuild
}
