// Package gobuild provides utilities for building programs and tests
// for the debugging session.

package gobuild

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/go-delve/delve/pkg/config"
	"github.com/go-delve/delve/pkg/goversion"
)

// Remove the file at path and issue a warning to stderr if this fails.
// This can be used to remove the temporary binary generated for the session.
func Remove(path string) {
	err := os.Remove(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not remove %v: %v\n", path, err)
	}
}

// optflags generates default build flags to turn off optimization and inlining.
func optflags(args []string) []string {
	// after go1.9 building with -gcflags='-N -l' and -a simultaneously works.
	// after go1.10 specifying -a is unnecessary because of the new caching strategy,
	// but we should pass -gcflags=all=-N -l to have it applied to all packages
	// see https://github.com/golang/go/commit/5993251c015dfa1e905bdf44bdb41572387edf90

	ver, _ := goversion.Installed()
	switch {
	case ver.Major < 0 || ver.AfterOrEqual(goversion.GoVersion{1, 10, -1, 0, 0, ""}):
		args = append(args, "-gcflags", "all=-N -l")
	case ver.AfterOrEqual(goversion.GoVersion{1, 9, -1, 0, 0, ""}):
		args = append(args, "-gcflags", "-N -l", "-a")
	default:
		args = append(args, "-gcflags", "-N -l")
	}
	return args
}

// GoBuild builds non-test files in 'pkgs' with the specified 'buildflags'
// and writes the output at 'debugname'.
func GoBuild(debugname string, pkgs []string, buildflags string) error {
	args := []string{"-o", debugname}
	args = optflags(args)
	if buildflags != "" {
		args = append(args, config.SplitQuotedFields(buildflags, '\'')...)
	}
	args = append(args, pkgs...)
	return gocommand("build", args...)
}

// GoBuild builds test files 'pkgs' with the specified 'buildflags'
// and writes the output at 'debugname'.
func GoTestBuild(debugname string, pkgs []string, buildflags string) error {
	args := []string{"-c", "-o", debugname}
	args = optflags(args)
	if buildflags != "" {
		args = append(args, config.SplitQuotedFields(buildflags, '\'')...)
	}
	args = append(args, pkgs...)
	return gocommand("test", args...)
}

func gocommand(command string, args ...string) error {
	allargs := []string{command}
	allargs = append(allargs, args...)
	goBuild := exec.Command("go", allargs...)
	goBuild.Stderr = os.Stderr
	return goBuild.Run()
}
