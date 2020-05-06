package main

import (
	"os"

	"github.com/go-delve/delve/cmd/dlv/cmds"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/version"
)

// Build is the git sha of this binaries build.
var Build string

func main() {
	if Build != "" {
		version.DelveVersion.Build = Build
	}
	const cgoCflagsEnv = "CGO_CFLAGS"
	if os.Getenv(cgoCflagsEnv) == "" {
		os.Setenv(cgoCflagsEnv, "-O0 -g")
	} else {
		logflags.MakeLogger(true, logflags.Fields{"layer": "dlv"}).Warn("CGO_CFLAGS already set, Cgo code could be optimized.")
	}
	cmds.New(false).Execute()
}
