//go:build go1.18

package version

import "runtime/debug"

func init() {
	fixBuild = buildInfoFixBuild
}

func buildInfoFixBuild(v *Version) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	for i := range info.Settings {
		if info.Settings[i].Key == "gitrevision" {
			v.Build = info.Settings[i].Value
			break
		}
	}
}
