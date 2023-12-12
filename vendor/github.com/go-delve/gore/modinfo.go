// This file is part of GoRE.
//
// Copyright (C) 2019-2021 GoRE Authors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.

package gore

import (
	"debug/buildinfo"
	"errors"
	"fmt"
	"runtime/debug"
)

var (
	// ErrNoBuildInfo is returned if the file has no build information available.
	ErrNoBuildInfo = errors.New("no build info available")
)

// BuildInfo that was extracted from the file.
type BuildInfo struct {
	// Compiler version. Can be nil.
	Compiler *GoVersion
	// ModInfo holds information about the Go modules in this file.
	// Can be nil.
	ModInfo *debug.BuildInfo
}

func (f *GoFile) extractBuildInfo() (*BuildInfo, error) {
	info, err := buildinfo.Read(f.fh.getFile())
	if err != nil {
		return nil, fmt.Errorf("error when extracting build information: %w", err)
	}

	result := &BuildInfo{
		Compiler: ResolveGoVersion(info.GoVersion),
		ModInfo:  info,
	}

	return result, nil
}
