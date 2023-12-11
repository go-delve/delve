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
	"bytes"
	"encoding/binary"
	"fmt"
)

var (
	goNoteNameELF  = []byte("Go\x00\x00")
	goNoteRawStart = []byte("\xff Go build ID: \"")
	goNoteRawEnd   = []byte("\"\n \xff")
)

func parseBuildIDFromElf(data []byte, byteOrder binary.ByteOrder) (string, error) {
	r := bytes.NewReader(data)
	var nameLen uint32
	var idLen uint32
	var tag uint32
	err := binary.Read(r, byteOrder, &nameLen)
	if err != nil {
		return "", fmt.Errorf("error when reading the BuildID name length: %w", err)
	}
	err = binary.Read(r, byteOrder, &idLen)
	if err != nil {
		return "", fmt.Errorf("error when reading the BuildID ID length: %w", err)
	}
	err = binary.Read(r, byteOrder, &tag)
	if err != nil {
		return "", fmt.Errorf("error when reading the BuildID tag: %w", err)
	}

	if tag != uint32(4) {
		return "", fmt.Errorf("build ID does not match expected value. 0x%x parsed", tag)
	}

	noteName := data[12 : 12+int(nameLen)]
	if !bytes.Equal(noteName, goNoteNameELF) {
		return "", fmt.Errorf("note name not as expected")
	}
	return string(data[16 : 16+int(idLen)]), nil
}

func parseBuildIDFromRaw(data []byte) (string, error) {
	idx := bytes.Index(data, goNoteRawStart)
	if idx < 0 {
		// No Build ID
		return "", nil
	}
	end := bytes.Index(data, goNoteRawEnd)
	if end < 0 {
		return "", fmt.Errorf("malformed Build ID")
	}
	return string(data[idx+len(goNoteRawStart) : end]), nil
}
