package main

import (
	"os"

	"github.com/go-delve/delve/cmd/dlv/cmds"
	"github.com/go-delve/delve/pkg/version"
	"github.com/sirupsen/logrus"
	"golang.org/x/telemetry"
)

// Build is the git sha of this binaries build.
var Build string

func main() {
	telemetry.Start(telemetry.Config{
		ReportCrashes: true,
	})

	if Build != "" {
		version.DelveVersion.Build = Build
	}

	const cgoCflagsEnv = "CGO_CFLAGS"
	if os.Getenv(cgoCflagsEnv) == "" {
		os.Setenv(cgoCflagsEnv, "-O0 -g")
	} else {
		logrus.WithFields(logrus.Fields{"layer": "dlv"}).Warnln("CGO_CFLAGS already set, Cgo code could be optimized.")
	}

	cmds.New(false).Execute()
}
