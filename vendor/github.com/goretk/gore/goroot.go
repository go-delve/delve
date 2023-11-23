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
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"

	"golang.org/x/arch/x86/x86asm"
)

func tryFromGOROOT(f *GoFile) (string, error) {
	// Check for non supported architectures.
	if f.FileInfo.Arch != Arch386 && f.FileInfo.Arch != ArchAMD64 {
		return "", nil
	}

	is32 := false
	if f.FileInfo.Arch == Arch386 {
		is32 = true
	}

	// Find runtime.GOROOT function.
	var fcn *Function
	std, err := f.GetSTDLib()
	if err != nil {
		return "", nil
	}

pkgLoop:
	for _, v := range std {
		if v.Name != "runtime" {
			continue
		}
		for _, vv := range v.Functions {
			if vv.Name != "GOROOT" {
				continue
			}
			fcn = vv
			break pkgLoop
		}
	}

	// Check if the functions was found
	if fcn == nil {
		// If we can't find the function there is nothing to do.
		return "", ErrNoGoRootFound
	}
	// Get the raw hex.
	buf, err := f.Bytes(fcn.Offset, fcn.End-fcn.Offset)
	if err != nil {
		return "", nil
	}
	s := 0
	mode := f.FileInfo.WordSize * 8

	for s < len(buf) {
		inst, err := x86asm.Decode(buf[s:], mode)
		if err != nil {
			// If we fail to decode the instruction, something is wrong so
			// bailout.
			return "", nil
		}

		// Update next instruction location.
		s = s + inst.Len

		// Check if it's a "mov" instruction.
		if inst.Op != x86asm.MOV {
			continue
		}
		if inst.Args[0] != x86asm.RAX && inst.Args[0] != x86asm.EAX {
			continue
		}
		arg := inst.Args[1].(x86asm.Mem)

		// First assume that the address is a direct addressing.
		addr := arg.Disp
		if arg.Base == x86asm.EIP || arg.Base == x86asm.RIP {
			// If the addressing is based on the instruction pointer, fix the address.
			addr = addr + int64(fcn.Offset) + int64(s)
		} else if arg.Base == 0 && arg.Disp > 0 {
			// In order to support x32 direct addressing
		} else {
			continue
		}
		// If the addressing is based on the stack pointer, this is not the right
		// instruction.
		if arg.Base == x86asm.ESP || arg.Base == x86asm.RSP {
			continue
		}

		// Resolve the pointer to the string. If we get no data, this is not the
		// right instruction.
		b, _ := f.Bytes(uint64(addr), uint64(0x20))
		if b == nil {
			continue
		}

		r := bytes.NewReader(b)
		ptr, err := readUIntTo64(r, f.FileInfo.ByteOrder, is32)
		if err != nil {
			// Probably not the right instruction, so go to next.
			continue
		}
		l, err := readUIntTo64(r, f.FileInfo.ByteOrder, is32)
		if err != nil {
			// Probably not the right instruction, so go to next.
			continue
		}

		bstr, _ := f.Bytes(ptr, l)
		if bstr == nil {
			continue
		}
		ver := string(bstr)
		if !utf8.ValidString(ver) {
			return "", ErrNoGoRootFound
		}
		return ver, nil
	}

	// for go version vary from 1.5 to 1.9
	s = 0
	var insts []x86asm.Inst
	for s < len(buf) {
		inst, err := x86asm.Decode(buf[s:], mode)
		if err != nil {
			return "", nil
		}
		s = s + inst.Len
		insts = append(insts, inst)
	}
	var length, addr uint64
	// Look up the address from the end of the assembly instruction
	for i := len(insts) - 1; i >= 0; i-- {
		inst := insts[i]

		if inst.Op == x86asm.MOV && length == 0 {
			switch v := inst.Args[1].(type) {
			case x86asm.Imm:
				length = uint64(v)
				continue
			default:
				continue
			}
		}
		if (inst.Op == x86asm.LEA || inst.Op == x86asm.MOV) && addr == 0 {
			switch v := inst.Args[1].(type) {
			case x86asm.Mem:
				arg := v
				if arg.Base == x86asm.ESP || arg.Base == x86asm.RSP {
					continue
				}
				addr = uint64(arg.Disp)
				if arg.Base == x86asm.EIP || arg.Base == x86asm.RIP {
					// If the addressing is based on the instruction pointer, fix the address.
					s = 0
					for i2, inst2 := range insts {
						if i2 > i {
							break
						}
						s += inst2.Len
					}
					addr = addr + fcn.Offset + uint64(s)
				} else if arg.Base == 0 && arg.Disp > 0 {
					// In order to support x32 direct addressing
				} else {
					continue
				}
			case x86asm.Imm:
				addr = uint64(v)
			default:
				continue
			}
		}
		if length > 0 && addr > 0 {
			bstr, _ := f.Bytes(addr, length)
			if bstr == nil {
				continue
			}
			ver := string(bstr)
			if !utf8.ValidString(ver) {
				return "", ErrNoGoRootFound
			}
			return ver, nil
		}
	}
	return "", ErrNoGoRootFound
}

func tryFromTimeInit(f *GoFile) (string, error) {
	// Check for non supported architectures.
	if f.FileInfo.Arch != Arch386 && f.FileInfo.Arch != ArchAMD64 {
		return "", nil
	}

	is32 := false
	if f.FileInfo.Arch == Arch386 {
		is32 = true
	}

	// Find time.init function.
	var fcn *Function
	std, err := f.GetSTDLib()
	if err != nil {
		return "", nil
	}

pkgLoop:
	for _, v := range std {
		if v.Name != "time" {
			continue
		}
		for _, vv := range v.Functions {
			if vv.Name != "init" {
				continue
			}
			fcn = vv
			break pkgLoop
		}
	}

	// Check if the functions was found
	if fcn == nil {
		// If we can't find the function there is nothing to do.
		return "", ErrNoGoRootFound
	}
	// Get the raw hex.
	buf, err := f.Bytes(fcn.Offset, fcn.End-fcn.Offset)
	if err != nil {
		return "", nil
	}
	s := 0
	mode := f.FileInfo.WordSize * 8

	for s < len(buf) {
		inst, err := x86asm.Decode(buf[s:], mode)
		if err != nil {
			// If we fail to decode the instruction, something is wrong so
			// bailout.
			return "", nil
		}

		// Update next instruction location.
		s = s + inst.Len

		// Check if it's a "mov" instruction.
		if inst.Op != x86asm.MOV {
			continue
		}
		if inst.Args[0] != x86asm.RAX && inst.Args[0] != x86asm.ECX {
			continue
		}
		kindof := reflect.TypeOf(inst.Args[1])
		if kindof.String() != "x86asm.Mem" {
			continue
		}
		arg := inst.Args[1].(x86asm.Mem)

		// First assume that the address is a direct addressing.
		addr := arg.Disp
		if arg.Base == x86asm.EIP || arg.Base == x86asm.RIP {
			// If the addressing is based on the instruction pointer, fix the address.
			addr = addr + int64(fcn.Offset) + int64(s)
		} else if arg.Base == 0 && arg.Disp > 0 {
			// In order to support x32 direct addressing
		} else {
			continue
		}
		// Resolve the pointer to the string. If we get no data, this is not the
		// right instruction.
		b, _ := f.Bytes(uint64(addr), uint64(0x20))
		if b == nil {
			continue
		}

		r := bytes.NewReader(b)
		ptr, err := readUIntTo64(r, f.FileInfo.ByteOrder, is32)
		if err != nil {
			// Probably not the right instruction, so go to next.
			continue
		}

		// If the pointer is nil, it's not the right instruction
		if ptr == 0 {
			continue
		}

		l, err := readUIntTo64(r, f.FileInfo.ByteOrder, is32)
		if err != nil {
			// Probably not the right instruction, so go to next.
			continue
		}

		bstr, _ := f.Bytes(ptr, l)
		if bstr == nil {
			continue
		}
		ver := string(bstr)
		if !utf8.ValidString(ver) {
			return "", ErrNoGoRootFound
		}
		return ver, nil
	}
	return "", ErrNoGoRootFound
}

func findGoRootPath(f *GoFile) (string, error) {
	var goroot string
	// There is no GOROOT function may be inlined (after go1.16)
	// at this time GOROOT is obtained through time_init function
	goroot, err := tryFromGOROOT(f)
	if goroot != "" {
		return goroot, nil
	}
	if err != nil && err != ErrNoGoRootFound {
		return "", err
	}

	goroot, err = tryFromTimeInit(f)
	if goroot != "" {
		return goroot, nil
	}
	if err != nil && err != ErrNoGoRootFound {
		return "", err
	}

	// Try determine from std lib package paths.
	pkg, err := f.GetSTDLib()
	if err != nil {
		return "", fmt.Errorf("error when getting standard library packages: %w", err)
	}
	if len(pkg) == 0 {
		return "", fmt.Errorf("no standard library packages found")
	}

	for _, v := range pkg {
		subpath := fmt.Sprintf("/src/%s", v.Name)
		if strings.HasSuffix(v.Filepath, subpath) {
			return strings.TrimSuffix(v.Filepath, subpath), nil
		}
	}

	return "", ErrNoGoRootFound
}
