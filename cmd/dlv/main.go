package main

import (
	"github.com/derekparker/delve/cmd/dlv/cmds"
	"github.com/derekparker/delve/version"
)

// Build is the git sha of this binaries build.
var Build string

func main() {
	version.DelveVersion.Build = Build
	cmds.New().Execute()
}
