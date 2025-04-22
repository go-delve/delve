//go:build go1.18

package version

import (
	"runtime/debug"
	"strings"
)

func init() {
	fixBuild = buildInfoFixBuild
}

func buildInfoFixBuild(v *Version) {
	// Return if v.Build already set, but not if it is Git ident expand file blob hash
	if !strings.HasPrefix(v.Build, "$Id: ") && !strings.HasSuffix(v.Build, " $") {
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
