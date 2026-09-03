// Package gobuild provides utilities for building programs and tests
// for the debugging session.
package gobuild

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/go-delve/delve/pkg/config"
	"github.com/go-delve/delve/pkg/logflags"
)

// Remove the file at path and issue a warning to stderr if this fails.
// This can be used to remove the temporary binary generated for the session.
func Remove(path string) {
	var err error
	// Open files can be removed on Unix, but not on Windows, where there also appears
	// to be a delay in releasing the binary when the process exits.
	// Leaving temporary files behind can be annoying to users, so we try again.
	//
	// Does backoff exponentially starting at 1ms all the way to ~400ms for a
	// total of 4.8s of wait time.
	for i := range 66 {
		if i != 0 {
			time.Sleep(time.Millisecond * time.Duration(math.Pow(1.1, float64(i-1))))
		}
		err = os.Remove(path)
		if err == nil || runtime.GOOS != "windows" {
			break
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not remove %v: %v\n", path, err)
	}
}

// GoBuild builds non-test files in 'pkgs' with the specified 'buildflags'
// and writes the output at 'debugname'.
func GoBuild(debugname string, pkgs []string, buildflags any) error {
	args, _ := goBuildArgs2(debugname, pkgs, buildflags, false)
	return gocommandRun("build", args...)
}

// GoBuildCombinedOutput builds non-test files in 'pkgs' with the specified 'buildflags'
// and writes the output at 'debugname'.
func GoBuildCombinedOutput(debugname string, pkgs []string, buildflags any) (string, []byte, error) {
	args, err := goBuildArgs2(debugname, pkgs, buildflags, false)
	if err != nil {
		return "", nil, err
	}
	return gocommandCombinedOutput("build", args...)
}

// GoTestBuild builds test files 'pkgs' with the specified 'buildflags'
// and writes the output at 'debugname'.
func GoTestBuild(debugname string, pkgs []string, buildflags any) error {
	args, _ := goBuildArgs2(debugname, pkgs, buildflags, true)
	return gocommandRun("test", args...)
}

// GoTestBuildCombinedOutput builds test files 'pkgs' with the specified 'buildflags'
// and writes the output at 'debugname'.
func GoTestBuildCombinedOutput(debugname string, pkgs []string, buildflags any) (string, []byte, error) {
	args, err := goBuildArgs2(debugname, pkgs, buildflags, true)
	if err != nil {
		return "", nil, err
	}
	return gocommandCombinedOutput("test", args...)
}

func goBuildArgs(debugname string, pkgs []string, buildflags string, isTest bool) []string {
	leading, bfv := splitLeadingGoDir(config.SplitQuotedFields(buildflags, '\''))
	args := append([]string{}, leading...)
	if isTest {
		args = append(args, "-c")
	}
	args = append(args, "-o", debugname)
	args = append(args, "-gcflags", "all=-N -l")
	if buildflags != "" {
		args = append(args, bfv...)
	}
	args = append(args, pkgs...)
	return args
}

// goBuildArgs2 is like goBuildArgs, but takes either string or []string.
func goBuildArgs2(debugname string, pkgs []string, buildflags any, isTest bool) ([]string, error) {
	var args []string
	switch buildflags := buildflags.(type) {
	case string:
		return goBuildArgs(debugname, pkgs, buildflags, isTest), nil
	case nil:
	case []string:
		leading, rest := splitLeadingGoDir(buildflags)
		args = append(args, leading...)
		if isTest {
			args = append(args, "-c")
		}
		args = append(args, rest...)
		args = append(args, "-o", debugname, "-gcflags", "all=-N -l")
		return append(args, pkgs...), nil
	default:
		return nil, fmt.Errorf("invalid buildflags type %T", buildflags)
	}

	if isTest {
		args = append(args, "-c")
	}
	args = append(args, "-o", debugname)
	args = append(args, "-gcflags", "all=-N -l")
	return append(args, pkgs...), nil
}

func splitLeadingGoDir(args []string) (leading, rest []string) {
	if len(args) >= 2 && args[0] == "-C" {
		return args[:2], args[2:]
	}
	if len(args) >= 1 && strings.HasPrefix(args[0], "-C=") {
		return args[:1], args[1:]
	}
	return nil, args
}

func gocommandRun(command string, args ...string) error {
	_, goBuild := gocommandExecCmd(command, args...)
	goBuild.Stdout = os.Stdout
	goBuild.Stderr = os.Stderr
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
	logflags.DebuggerLogger().Debugf("gobuild args: %v", allargs)
	return strings.Join(append([]string{"go"}, allargs...), " "), goBuild
}
