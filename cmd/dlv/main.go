package main

import (
	"os"

	"github.com/derekparker/delve/cmd/dlv/cmds"
	"github.com/derekparker/delve/pkg/version"
	"github.com/sirupsen/logrus"
)

// Build is the git sha of this binaries build.
var Build string

func main() {
	if Build != "" {
		version.DelveVersion.Build = Build
	}
	const cgoCflagsEnv = "CGO_CFLAGS"
	if os.Getenv(cgoCflagsEnv) == "" {
		os.Setenv(cgoCflagsEnv, "-O -g")
	} else {
		logrus.WithFields(logrus.Fields{"layer": "dlv"}).Warnln("CGO_CFLAGS already set, Cgo code could be optimized.")
	}
	cmds.New(false).Execute()
}
