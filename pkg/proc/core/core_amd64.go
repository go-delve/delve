package core

import (
	"errors"
)

type openFn func(string, string) (*Process, error)

var openFns = []openFn{readLinuxAMD64Core, readAMD64Minidump}

// ErrUnrecognizedFormat is returned when the core file is not recognized as
// any of the supported formats.
var ErrUnrecognizedFormat = errors.New("unrecognized core format")

// OpenCore will open the core file and return a Process struct.
// If the DWARF information cannot be found in the binary, Delve will look
// for external debug files in the directories passed in.
func OpenCore(corePath, exePath string, debugInfoDirs []string) (*Process, error) {
	var p *Process
	var err error
	for _, openFn := range openFns {
		p, err = openFn(corePath, exePath)
		if err != ErrUnrecognizedFormat {
			break
		}
	}
	if err != nil {
		return nil, err
	}

	if err := p.initialize(exePath, debugInfoDirs); err != nil {
		return nil, err
	}

	return p, nil
}
