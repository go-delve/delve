package main

import (
	"os"

	"github.com/go-delve/delve/cmd/dlv/cmds"
	"github.com/go-delve/delve/pkg/version"
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
		err := os.Setenv(cgoCflagsEnv, "-O0 -g")
		if err != nil {
			logrus.WithFields(logrus.Fields{"layer": "dlv"}).Warnf("CGO_CFLAGS could not be set, Cgo code could be optimized: %v", err)
		}
	} else {
		logrus.WithFields(logrus.Fields{"layer": "dlv"}).Warnln("CGO_CFLAGS already set, Cgo code could be optimized.")
	}

	_ = cmds.New(false).Execute()
}
