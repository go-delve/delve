package goversion

import (
	"fmt"
)

var (
	MinSupportedVersionOfGoMajor = 1
	MinSupportedVersionOfGoMinor = 15
	MaxSupportedVersionOfGoMajor = 1
	MaxSupportedVersionOfGoMinor = 17
	goTooOldErr                  = fmt.Errorf("Version of Go is too old for this version of Delve (minimum supported version %d.%d, suppress this error with --check-go-version=false)", MinSupportedVersionOfGoMajor, MinSupportedVersionOfGoMinor)
	dlvTooOldErr                 = fmt.Errorf("Version of Delve is too old for this version of Go (maximum supported version %d.%d, suppress this error with --check-go-version=false)", MaxSupportedVersionOfGoMajor, MaxSupportedVersionOfGoMinor)
)

// Compatible checks that the version specified in the producer string is compatible with
// this version of delve.
func Compatible(producer string) error {
	ver := ParseProducer(producer)
	if ver.IsDevel() {
		return nil
	}
	if !ver.AfterOrEqual(GoVersion{MinSupportedVersionOfGoMajor, MinSupportedVersionOfGoMinor, -1, 0, 0, ""}) {
		return goTooOldErr
	}
	if ver.AfterOrEqual(GoVersion{MaxSupportedVersionOfGoMajor, MaxSupportedVersionOfGoMinor + 1, -1, 0, 0, ""}) {
		return dlvTooOldErr
	}
	return nil
}
