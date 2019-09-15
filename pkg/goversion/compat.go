package goversion

import (
	"fmt"
)

var (
	minSupportedVersionOfGoMinor = 11
	maxSupportedVersionOfGoMinor = 13
	goTooOldErr                  = fmt.Errorf("Version of Go is too old for this version of Delve (minimum supported version 1.%d, suppress this error with --check-go-version=false)", minSupportedVersionOfGoMinor)
	dlvTooOldErr                 = fmt.Errorf("Version of Delve is too old for this version of Go (maximum supported version 1.%d, suppress this error with --check-go-version=false)", maxSupportedVersionOfGoMinor)
)

// Compatible checks that the version specified in the producer string is compatible with
// this version of delve.
func Compatible(producer string) error {
	ver := parseProducer(producer)
	if ver.IsDevel() {
		return nil
	}
	if !ver.AfterOrEqual(GoVersion{1, minSupportedVersionOfGoMinor, -1, 0, 0, ""}) {
		return goTooOldErr
	}
	if ver.AfterOrEqual(GoVersion{1, maxSupportedVersionOfGoMinor + 1, -1, 0, 0, ""}) {
		return dlvTooOldErr
	}
	return nil
}
