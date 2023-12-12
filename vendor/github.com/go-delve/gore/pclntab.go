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
	"debug/pe"
)

// pclntab12magic is the magic bytes used for binaries compiled with Go
// prior to 1.16
var pclntab12magic = []byte{0xfb, 0xff, 0xff, 0xff, 0x0, 0x0}

// pclntab116magic is the magic bytes used for binaries compiled with
// Go 1.16 and Go 1.17.
var pclntab116magic = []byte{0xfa, 0xff, 0xff, 0xff, 0x0, 0x0}

// pclntab118magic is the magic bytes used for binaries compiled with
// Go 1.18 and Go 1.19.
var pclntab118magic = []byte{0xf0, 0xff, 0xff, 0xff, 0x0, 0x0}

// pclntab120magic is the magic bytes used for binaries compiled with
// Go 1.20 and onwards.
var pclntab120magic = []byte{0xf1, 0xff, 0xff, 0xff, 0x0, 0x0}

// searchFileForPCLNTab will search the .rdata and .text section for the
// PCLN table. Note!! The address returned by this function needs to be
// adjusted by adding the image base address!!!
func searchFileForPCLNTab(f *pe.File) (uint32, []byte, error) {
	for _, v := range []string{".rdata", ".text"} {
		sec := f.Section(v)
		if sec == nil {
			continue
		}
		secData, err := sec.Data()
		if err != nil {
			continue
		}
		tab, err := searchSectionForTab(secData)
		if err == ErrNoPCLNTab {
			continue
		}
		// TODO: Switch to returning a uint64 instead.
		addr := sec.VirtualAddress + uint32(len(secData)-len(tab))
		return addr, tab, err
	}
	return 0, []byte{}, ErrNoPCLNTab
}

// searchSectionForTab looks for the PCLN table within the section.
func searchSectionForTab(secData []byte) ([]byte, error) {
	// First check for the current magic used. If this fails, it could be
	// an older version. So check for the old header.
MAGIC_LOOP:
	for _, magic := range [][]byte{pclntab120magic, pclntab118magic, pclntab116magic, pclntab12magic} {
		off := bytes.LastIndex(secData, magic)
		if off == -1 {
			continue // Try other magic.
		}
		for off != -1 {
			if off != 0 {
				buf := secData[off:]
				if len(buf) < 16 || buf[4] != 0 || buf[5] != 0 ||
					(buf[6] != 1 && buf[6] != 2 && buf[6] != 4) || // pc quantum
					(buf[7] != 4 && buf[7] != 8) { // pointer size
					// Header doesn't match.
					if off-1 <= 0 {
						continue MAGIC_LOOP
					}
					off = bytes.LastIndex(secData[:off-1], magic)
					continue
				}
				// Header match
				return secData[off:], nil
			}
			break
		}
	}
	return nil, ErrNoPCLNTab
}
