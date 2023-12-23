package goversion

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// GoVersion represents the Go version of
// the Go compiler version used to compile
// the target binary.
type GoVersion struct {
	Major     int
	Minor     int
	Rev       int // revision number or negative number for beta and rc releases
	Proposal  string
	Toolchain string
}

const (
	betaStart = -1000
	betaEnd   = -2000
)

func betaRev(beta int) int {
	return beta + betaEnd
}

func rcRev(rc int) int {
	return rc + betaStart
}

var (
	GoVer18Beta = GoVersion{1, 8, betaRev(0), "", ""}
)

// Parse parses a go version string
func Parse(ver string) (GoVersion, bool) {
	var r GoVersion
	var err1, err2, err3 error

	if strings.HasPrefix(ver, "devel") {
		return GoVersion{-1, 0, 0, "", ""}, true
	}

	if strings.HasPrefix(ver, "go") {
		ver := strings.Split(ver, " ")[0]
		v := strings.SplitN(ver[2:], ".", 4)
		switch len(v) {
		case 2:
			r.Major, err1 = strconv.Atoi(v[0])
			var vr []string

			if vr = strings.SplitN(v[1], "beta", 2); len(vr) == 2 {
				// old beta releases goX.YbetaZ
				var beta int
				beta, err3 = strconv.Atoi(vr[1])
				r.Rev = betaRev(beta)
			} else if vr = strings.SplitN(v[1], "b", 2); len(vr) == 2 {
				// old boringcrypto version goX.YbZ
				if _, err := strconv.Atoi(vr[1]); err != nil {
					return GoVersion{}, false
				}
			} else {
				vr = strings.SplitN(v[1], "rc", 2)
				if len(vr) == 2 {
					// rc release goX.YrcZ
					var rc int
					rc, err3 = strconv.Atoi(vr[1])
					r.Rev = rcRev(rc)
				} else {
					r.Minor, err2 = strconv.Atoi(v[1])
					if err2 != nil {
						return GoVersion{}, false
					}
					return r, true
				}
			}

			// old major release (if none of the options above apply) goX.Y

			r.Minor, err2 = strconv.Atoi(vr[0])
			r.Proposal = ""

			if err1 != nil || err2 != nil || err3 != nil {
				return GoVersion{}, false
			}

			return r, true

		case 3:

			r.Major, err1 = strconv.Atoi(v[0])
			r.Minor, err2 = strconv.Atoi(v[1])

			if vr := strings.SplitN(v[2], "-", 2); len(vr) == 2 {
				// minor version with toolchain modifier goX.Y.Z-anything
				r.Rev, err3 = strconv.Atoi(vr[0])
				r.Toolchain = vr[1]
			} else if vr := strings.SplitN(v[2], "b", 2); len(vr) == 2 {
				// old boringcrypto version goX.Y.ZbW
				r.Rev, err3 = strconv.Atoi(vr[0])
			} else {
				// minor version goX.Y.Z
				r.Rev, err3 = strconv.Atoi(v[2])
			}

			r.Proposal = ""
			if err1 != nil || err2 != nil || err3 != nil {
				return GoVersion{}, false
			}

			return r, true

		case 4:

			// old proposal release goX.Y.Z.anything

			r.Major, err1 = strconv.Atoi(v[0])
			r.Minor, err2 = strconv.Atoi(v[1])
			r.Rev, err3 = strconv.Atoi(v[2])
			r.Proposal = v[3]
			if err1 != nil || err2 != nil || err3 != nil || r.Proposal == "" {
				return GoVersion{}, false
			}

			return r, true

		default:
			return GoVersion{}, false
		}
	}

	return GoVersion{}, false
}

// AfterOrEqual returns whether one GoVersion is after or
// equal to the other.
func (v *GoVersion) AfterOrEqual(b GoVersion) bool {
	if v.Major < b.Major {
		return false
	} else if v.Major > b.Major {
		return true
	}

	if v.Minor < b.Minor {
		return false
	} else if v.Minor > b.Minor {
		return true
	}

	if v.Rev < b.Rev {
		return false
	} else if v.Rev > b.Rev {
		return true
	}

	return true
}

// IsDevel returns whether the GoVersion
// is a development version.
func (v *GoVersion) IsDevel() bool {
	return v.Major < 0
}

func (v *GoVersion) String() string {
	switch {
	case v.Rev < betaStart:
		// beta version
		return fmt.Sprintf("go%d.%dbeta%d", v.Major, v.Minor, v.Rev-betaEnd)
	case v.Rev < 0:
		// rc version
		return fmt.Sprintf("go%d.%drc%d", v.Major, v.Minor, v.Rev-betaStart)
	case v.Proposal != "":
		// with proposal
		return fmt.Sprintf("go%d.%d.%d.%s", v.Major, v.Minor, v.Rev, v.Proposal)
	case v.Rev == 0 && v.Minor < 21:
		// old major version
		return fmt.Sprintf("go%d.%d", v.Major, v.Minor)
	case v.Toolchain != "":
		return fmt.Sprintf("go%d.%d.%d-%s", v.Major, v.Minor, v.Rev, v.Toolchain)
	default:
		// post go1.21 major version or minor version
		return fmt.Sprintf("go%d.%d.%d", v.Major, v.Minor, v.Rev)
	}
}

const goVersionPrefix = "go version "

// Installed runs "go version" and parses the output
func Installed() (GoVersion, bool) {
	out, err := exec.Command("go", "version").CombinedOutput()
	if err != nil {
		return GoVersion{}, false
	}

	s := string(out)

	if !strings.HasPrefix(s, goVersionPrefix) {
		return GoVersion{}, false
	}

	return Parse(s[len(goVersionPrefix):])
}

// VersionAfterOrEqual checks that version (as returned by runtime.Version()
// or go version) is major.minor or a later version, or a development
// version.
func VersionAfterOrEqual(version string, major, minor int) bool {
	return VersionAfterOrEqualRev(version, major, minor, betaEnd)
}

// VersionAfterOrEqualRev checks that version (as returned by runtime.Version()
// or go version) is major.minor or a later version, or a development
// version.
func VersionAfterOrEqualRev(version string, major, minor, rev int) bool {
	ver, _ := Parse(version)
	if ver.IsDevel() {
		return true
	}
	return ver.AfterOrEqual(GoVersion{major, minor, rev, "", ""})
}

const producerVersionPrefix = "Go cmd/compile "

// ProducerAfterOrEqual checks that the DW_AT_producer version is
// major.minor or a later version, or a development version.
func ProducerAfterOrEqual(producer string, major, minor int) bool {
	ver := ParseProducer(producer)
	if ver.IsDevel() {
		return true
	}
	return ver.AfterOrEqual(GoVersion{major, minor, 0, "", ""})
}

func ParseProducer(producer string) GoVersion {
	producer = strings.TrimPrefix(producer, producerVersionPrefix)
	ver, _ := Parse(producer)
	return ver
}
