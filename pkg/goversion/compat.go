package goversion

import (
	"fmt"
	"os"
)

var (
	MinSupportedVersionOfGoMajor = 1
	MinSupportedVersionOfGoMinor = 15
	MaxSupportedVersionOfGoMajor = 1
	MaxSupportedVersionOfGoMinor = 17
	goTooOldErr                  = fmt.Errorf("Version of Go is too old for this version of Delve (minimum supported version %d.%d, suppress this error with --check-go-version=false)", MinSupportedVersionOfGoMajor, MinSupportedVersionOfGoMinor)
	goTooOldWarn                 = fmt.Errorf("Warning: version of Go is too old for this version of Delve (minimum supported version %d.%d)", MinSupportedVersionOfGoMajor, MinSupportedVersionOfGoMinor)
	dlvTooOldErr                 = fmt.Errorf("Version of Delve is too old for this version of Go (maximum supported version %d.%d, suppress this error with --check-go-version=false)", MaxSupportedVersionOfGoMajor, MaxSupportedVersionOfGoMinor)
	dlvTooOldWarn                = fmt.Errorf("Warning: version of Delve is too old for this version of Go (maximum supported version %d.%d)", MaxSupportedVersionOfGoMajor, MaxSupportedVersionOfGoMinor)
)

// Compatible checks that the version specified in the producer string is compatible with
// this version of delve.
func Compatible(producer string, warnonly bool) error {
	ver := ParseProducer(producer)
	if ver.IsDevel() {
		return nil
	}
	if !ver.AfterOrEqual(GoVersion{MinSupportedVersionOfGoMajor, MinSupportedVersionOfGoMinor, -1, 0, 0, ""}) {
		if warnonly {
			fmt.Fprintf(os.Stderr, "%v\n", goTooOldWarn)
			return nil
		}
		return goTooOldErr
	}
	if ver.AfterOrEqual(GoVersion{MaxSupportedVersionOfGoMajor, MaxSupportedVersionOfGoMinor + 1, -1, 0, 0, ""}) {
		if warnonly {
			fmt.Fprintf(os.Stderr, "%v\n", dlvTooOldWarn)
			return nil
		}
		return dlvTooOldErr
	}
	return nil
}
