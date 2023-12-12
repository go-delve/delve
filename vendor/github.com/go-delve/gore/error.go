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

import "errors"

var (
	// ErrNotEnoughBytesRead is returned if read call returned less bytes than what is needed.
	ErrNotEnoughBytesRead = errors.New("not enough bytes read")
	// ErrUnsupportedFile is returned if the file process is unsupported.
	ErrUnsupportedFile = errors.New("unsupported file")
	// ErrSectionDoesNotExist is returned when accessing a section that does not exist.
	ErrSectionDoesNotExist = errors.New("section does not exist")
	// ErrNoGoVersionFound is returned if no goversion was found in the binary.
	ErrNoGoVersionFound = errors.New("no goversion found")
	// ErrNoPCLNTab is returned if no PCLN table can be located.
	ErrNoPCLNTab = errors.New("no pclntab located")
	// ErrInvalidGoVersion is returned if the go version set for the file is either invalid
	// or does not match a known version by the library.
	ErrInvalidGoVersion = errors.New("invalid go version")
	// ErrNoGoRootFound is returned if no goroot was found in the binary.
	ErrNoGoRootFound = errors.New("no goroot found")
)
