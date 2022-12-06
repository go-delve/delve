package goversion

import (
	"fmt"

	"github.com/go-delve/delve/pkg/logflags"
)

var (
	MinSupportedVersionOfGoMajor = 1
	MinSupportedVersionOfGoMinor = 18
	MaxSupportedVersionOfGoMajor = 1
	MaxSupportedVersionOfGoMinor = 20
	goTooOldErr                  = fmt.Sprintf("Go version %%s is too old for this version of Delve (minimum supported version %d.%d, suppress this error with --check-go-version=false)", MinSupportedVersionOfGoMajor, MinSupportedVersionOfGoMinor)
	goTooOldWarn                 = fmt.Sprintf("WARNING: undefined behavior - Go version %%s is too old for this version of Delve (minimum supported version %d.%d)", MinSupportedVersionOfGoMajor, MinSupportedVersionOfGoMinor)
	dlvTooOldErr                 = fmt.Sprintf("Version of Delve is too old for Go version %%s (maximum supported version %d.%d, suppress this error with --check-go-version=false)", MaxSupportedVersionOfGoMajor, MaxSupportedVersionOfGoMinor)
	dlvTooOldWarn                = fmt.Sprintf("WARNING: undefined behavior - version of Delve is too old for Go version %%s (maximum supported version %d.%d)", MaxSupportedVersionOfGoMajor, MaxSupportedVersionOfGoMinor)
)

// Compatible checks that the version specified in the producer string is compatible with
// this version of delve.
func Compatible(producer string, warnonly bool) error {
	ver := ParseProducer(producer)
	if ver.IsDevel() {
		return nil
	}
	verstr := fmt.Sprintf("%d.%d.%d", ver.Major, ver.Minor, ver.Rev)
	if !ver.AfterOrEqual(GoVersion{MinSupportedVersionOfGoMajor, MinSupportedVersionOfGoMinor, -1, 0, 0, ""}) {
		if warnonly {
			logflags.WriteError(fmt.Sprintf(goTooOldWarn, verstr))
			return nil
		}
		return fmt.Errorf(goTooOldErr, verstr)
	}
	if ver.AfterOrEqual(GoVersion{MaxSupportedVersionOfGoMajor, MaxSupportedVersionOfGoMinor + 1, -1, 0, 0, ""}) {
		if warnonly {
			logflags.WriteError(fmt.Sprintf(dlvTooOldWarn, verstr))
			return nil
		}
		return fmt.Errorf(dlvTooOldErr, verstr)
	}
	return nil
}
