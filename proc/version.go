package proc

import (
	"strconv"
	"strings"
)

type GoVersion struct {
	Major int
	Minor int
	Rev   int
}

func parseVersionString(ver string) (GoVersion, bool) {
	if ver[:2] != "go" {
		return GoVersion{}, false
	}
	v := strings.SplitN(ver[2:], ".", 3)
	if len(v) != 3 {
		return GoVersion{}, false
	}

	var r GoVersion
	var err1, err2, err3 error

	r.Major, err1 = strconv.Atoi(v[0])
	r.Minor, err2 = strconv.Atoi(v[1])
	r.Rev, err3 = strconv.Atoi(v[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return GoVersion{}, false
	}

	return r, true
}

func (a *GoVersion) After(b GoVersion) bool {
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
	}

	return true
}
