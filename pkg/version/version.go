package version

import (
	"fmt"
	"runtime"
)

// Version represents the current version of Delve.
type Version struct {
	Major    string
	Minor    string
	Patch    string
	Metadata string
	Build    string
}

// DelveVersion is the current version of Delve.
var DelveVersion = Version{
	Major: "1", Minor: "24", Patch: "0", Metadata: "",
	Build: "$Id$",
}

func (v Version) String() string {
	fixBuild(&v)
	ver := fmt.Sprintf("Version: %s.%s.%s", v.Major, v.Minor, v.Patch)
	if v.Metadata != "" {
		ver += "-" + v.Metadata
	}
	return fmt.Sprintf("%s\nBuild: %s", ver, v.Build)
}

var buildInfo = func() string {
	return ""
}

func BuildInfo() string {
	return fmt.Sprintf("%s\n%s", runtime.Version(), buildInfo())
}

var fixBuild = func(v *Version) {
	// does nothing
}
