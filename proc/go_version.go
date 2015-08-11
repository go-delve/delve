package proc

import (
	"strconv"
	"strings"
)

type GoVersion struct {
	Major int
	Minor int
	Rev   int
	Beta  int
	RC    int
}

func parseVersionString(ver string) (GoVersion, bool) {
	var r GoVersion
	var err1, err2, err3 error

	if strings.HasPrefix(ver, "devel") {
		return GoVersion{-1, 0, 0, 0, 0}, true
	}

	if strings.HasPrefix(ver, "go") {
		v := strings.SplitN(ver[2:], ".", 3)
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
				}
			}
			if len(vr) < 2 {
				return GoVersion{}, false
			}

			r.Minor, err2 = strconv.Atoi(vr[0])
			r.Rev = -1

			if err1 != nil || err2 != nil || err3 != nil {
				return GoVersion{}, false
			}

			return r, true

		case 3:

			r.Major, err1 = strconv.Atoi(v[0])
			r.Minor, err2 = strconv.Atoi(v[1])
			r.Rev, err3 = strconv.Atoi(v[2])
			if err1 != nil || err2 != nil || err3 != nil {
				return GoVersion{}, false
			}

			return r, true

		default:
			return GoVersion{}, false
		}
	}

	return GoVersion{}, false
}

func (a *GoVersion) AfterOrEqual(b GoVersion) bool {
	if a.Major < b.Major {
		return false
	} else if a.Major > b.Major {
		return true
	}

	if a.Minor < b.Minor {
		return false
	} else if a.Minor > b.Minor {
		return true
	}

	if a.Rev < b.Rev {
		return false
	} else if a.Rev > b.Rev {
		return true
	}

	if a.Beta < b.Beta {
		return false
	}

	if a.RC < b.RC {
		return false
	}

	return true
}

func (v *GoVersion) IsDevel() bool {
	return v.Major < 0
}
