package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
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
	Major: "1", Minor: "24", Patch: "2", Metadata: "",
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

func fixBuild(v *Version) {
	// Return if v.Build already set, but not if it is Git ident expand file blob hash
	if !strings.HasPrefix(v.Build, "$Id$") {
		return
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}

	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			v.Build = setting.Value
			return
		}
	}

	// If we didn't find vcs.revision, try the old key for backward compatibility
	for _, setting := range info.Settings {
		if setting.Key == "gitrevision" {
			v.Build = setting.Value
			return
		}
	}
}
