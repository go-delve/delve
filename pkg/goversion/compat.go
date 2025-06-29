package goversion

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/go-delve/delve/pkg/logflags"
)

//lint:file-ignore ST1005 errors here can be capitalized

var (
	MinSupportedVersionOfGoMajor = 1
	MinSupportedVersionOfGoMinor = 21
	MaxSupportedVersionOfGoMajor = 1
	MaxSupportedVersionOfGoMinor = 25
	goTooOldErr                  = fmt.Sprintf("Go version %%s is too old for this version of Delve (minimum supported version %d.%d, suppress this error with --check-go-version=false)", MinSupportedVersionOfGoMajor, MinSupportedVersionOfGoMinor)
	goTooOldWarn                 = fmt.Sprintf("WARNING: undefined behavior - Go version %%s is too old for this version of Delve (minimum supported version %d.%d)", MinSupportedVersionOfGoMajor, MinSupportedVersionOfGoMinor)
	dlvTooOldErr                 = fmt.Sprintf("Version of Delve is too old for Go version %%s (maximum supported version %d.%d, suppress this error with --check-go-version=false)", MaxSupportedVersionOfGoMajor, MaxSupportedVersionOfGoMinor)
	dlvTooOldWarn                = fmt.Sprintf("WARNING: undefined behavior - version of Delve is too old for Go version %%s (maximum supported version %d.%d)", MaxSupportedVersionOfGoMajor, MaxSupportedVersionOfGoMinor)
)

// Compatible checks that the version specified in the producer string is compatible with
// this version of delve.
func Compatible(dwarfVer uint8, producer string, warnonly bool) error {
	ver := ParseProducer(producer)
	if ver.IsOldDevel() {
		return nil
	}
	if runtime.GOARCH == "riscv64" && !ver.AfterOrEqual(GoVersion{1, 24, versionedDevel, "", ""}) {
		if warnonly {
			logflags.WriteError(fmt.Sprintf(goTooOldWarn, ver.String()))
			return nil
		}
		return fmt.Errorf(goTooOldErr, ver.String())
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
	if dwarfVer >= 5 {
		if !VersionAfterOrEqual(runtime.Version(), 1, 25) {
			const errstr = "To debug executables using DWARFv5 or later Delve must be built with Go version 1.25.0 or later"
			if warnonly {
				logflags.WriteError(errstr)
				return nil
			}
			return errors.New(errstr)
		}
	}
	return nil
}
