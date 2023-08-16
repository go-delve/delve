package goversion

import (
	"fmt"

	"github.com/go-delve/delve/pkg/logflags"
)

var (
	MinSupportedVersionOfGoMajor = 1
	MinSupportedVersionOfGoMinor = 19
	MaxSupportedVersionOfGoMajor = 1
	MaxSupportedVersionOfGoMinor = 21
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
	if !ver.AfterOrEqual(GoVersion{MinSupportedVersionOfGoMajor, MinSupportedVersionOfGoMinor, betaRev(0), "", ""}) {
		if warnonly {
			logflags.WriteError(fmt.Sprintf(goTooOldWarn, ver.String()))
			return nil
		}
		return fmt.Errorf(goTooOldErr, ver.String())
	}
	if ver.AfterOrEqual(GoVersion{MaxSupportedVersionOfGoMajor, MaxSupportedVersionOfGoMinor + 1, betaRev(0), "", ""}) {
		if warnonly {
			logflags.WriteError(fmt.Sprintf(dlvTooOldWarn, ver.String()))
			return nil
		}
		return fmt.Errorf(dlvTooOldErr, ver.String())
	}
	return nil
}
