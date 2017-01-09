package version

import "fmt"

// Version represents the current version of Delve.
type Version struct {
	Major    string
	Minor    string
	Patch    string
	Metadata string
	Build    string
}

var (
	// DelveVersion is the current version of Delve.
	DelveVersion = Version{Major: "0", Minor: "12", Patch: "0", Metadata: ""}
)

func (v Version) String() string {
	return fmt.Sprintf("Version: %s.%s.%s-%s\nBuild: %s", v.Major, v.Minor, v.Patch, v.Metadata, v.Build)
}
