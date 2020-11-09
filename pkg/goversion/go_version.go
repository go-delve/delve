package goversion

import (
	"os/exec"
	"strconv"
	"strings"
)

// GoVersion represents the Go version of
// the Go compiler version used to compile
// the target binary.
type GoVersion struct {
	Major    int
	Minor    int
	Rev      int
	Beta     int
	RC       int
	Proposal string
}

var (
	GoVer18Beta = GoVersion{1, 8, -1, 0, 0, ""}
)

// Parse parses a go version string
func Parse(ver string) (GoVersion, bool) {
	var r GoVersion
	var err1, err2, err3 error

	if strings.HasPrefix(ver, "devel") {
		return GoVersion{-1, 0, 0, 0, 0, ""}, true
	}

	if strings.HasPrefix(ver, "go") {
		ver := strings.Split(ver, " ")[0]
		v := strings.SplitN(ver[2:], ".", 4)
		switch len(v) {
		case 2:
			r.Major, err1 = strconv.Atoi(v[0])
			vr := strings.SplitN(v[1], "beta", 2)
			if len(vr) == 2 {
				r.Beta, err3 = strconv.Atoi(vr[1])
			} else {
				vr = strings.SplitN(v[1], "rc", 2)
				if len(vr) == 2 {
					r.RC, err3 = strconv.Atoi(vr[1])
				} else {
					r.Minor, err2 = strconv.Atoi(v[1])
					if err2 != nil {
						return GoVersion{}, false
					}
					return r, true
				}
			}

			r.Minor, err2 = strconv.Atoi(vr[0])
			r.Rev = -1
			r.Proposal = ""

			if err1 != nil || err2 != nil || err3 != nil {
				return GoVersion{}, false
			}

			return r, true

		case 3:

			r.Major, err1 = strconv.Atoi(v[0])
			r.Minor, err2 = strconv.Atoi(v[1])
			r.Rev, err3 = strconv.Atoi(v[2])
			r.Proposal = ""
			if err1 != nil || err2 != nil || err3 != nil {
				return GoVersion{}, false
			}

			return r, true

		case 4:

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

	if v.Beta < b.Beta {
		return false
	}

	if v.RC < b.RC {
		return false
	}

	return true
}

// IsDevel returns whether the GoVersion
// is a development version.
func (v *GoVersion) IsDevel() bool {
	return v.Major < 0
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
	return VersionAfterOrEqualRev(version, major, minor, -1)
}

// VersionAfterOrEqualRev checks that version (as returned by runtime.Version()
// or go version) is major.minor or a later version, or a development
// version.
func VersionAfterOrEqualRev(version string, major, minor, rev int) bool {
	ver, _ := Parse(version)
	if ver.IsDevel() {
		return true
	}
	return ver.AfterOrEqual(GoVersion{major, minor, rev, 0, 0, ""})
}

const producerVersionPrefix = "Go cmd/compile "

// ProducerAfterOrEqual checks that the DW_AT_producer version is
// major.minor or a later version, or a development version.
func ProducerAfterOrEqual(producer string, major, minor int) bool {
	ver := parseProducer(producer)
	if ver.IsDevel() {
		return true
	}
	return ver.AfterOrEqual(GoVersion{major, minor, -1, 0, 0, ""})
}

func parseProducer(producer string) GoVersion {
	if strings.HasPrefix(producer, producerVersionPrefix) {
		producer = producer[len(producerVersionPrefix):]
	}
	ver, _ := Parse(producer)
	return ver
}
