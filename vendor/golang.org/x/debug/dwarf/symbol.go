// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dwarf

// This file provides simple methods to access the symbol table by name and address.

import "fmt"

// lookupEntry returns the Entry for the name. If tag is non-zero, only entries
// with that tag are considered.
func (d *Data) lookupEntry(name string, tag Tag) (*Entry, error) {
	r := d.Reader()
	for {
		entry, err := r.Next()
		if err != nil {
			return nil, err
		}
		if entry == nil {
			// TODO: why don't we get an error here?
			break
		}
		if tag != 0 && tag != entry.Tag {
			continue
		}
		nameAttr := entry.Val(AttrName)
		if nameAttr == nil {
			continue
		}
		if nameAttr.(string) == name {
			return entry, nil
		}
	}
	return nil, fmt.Errorf("DWARF entry for %q not found", name)
}

// LookupEntry returns the Entry for the named symbol.
func (d *Data) LookupEntry(name string) (*Entry, error) {
	return d.lookupEntry(name, 0)
}

// LookupFunction returns the address of the named symbol, a function.
func (d *Data) LookupFunction(name string) (uint64, error) {
	entry, err := d.lookupEntry(name, TagSubprogram)
	if err != nil {
		return 0, err
	}
	addrAttr := entry.Val(AttrLowpc)
	if addrAttr == nil {
		return 0, fmt.Errorf("symbol %q has no LowPC attribute", name)
	}
	addr, ok := addrAttr.(uint64)
	if !ok {
		return 0, fmt.Errorf("symbol %q has non-uint64 LowPC attribute", name)
	}
	return addr, nil
}

// TODO: should LookupVariable handle both globals and locals? Locals don't
// necessarily have a fixed address. They may be in a register, or otherwise
// move around.

// LookupVariable returns the location of a named symbol, a variable.
func (d *Data) LookupVariable(name string) (uint64, error) {
	entry, err := d.lookupEntry(name, TagVariable)
	if err != nil {
		return 0, fmt.Errorf("variable %s: %s", name, err)
	}
	loc, err := d.EntryLocation(entry)
	if err != nil {
		return 0, fmt.Errorf("variable %s: %s", name, err)
	}
	return loc, nil
}

// EntryLocation returns the address of the object referred to by the given Entry.
func (d *Data) EntryLocation(e *Entry) (uint64, error) {
	loc, _ := e.Val(AttrLocation).([]byte)
	if len(loc) == 0 {
		return 0, fmt.Errorf("DWARF entry has no Location attribute")
	}
	// TODO: implement the DWARF Location bytecode. What we have here only
	// recognizes a program with a single literal opAddr bytecode.
	if asize := d.unit[0].asize; loc[0] == opAddr && len(loc) == 1+asize {
		switch asize {
		case 1:
			return uint64(loc[1]), nil
		case 2:
			return uint64(d.order.Uint16(loc[1:])), nil
		case 4:
			return uint64(d.order.Uint32(loc[1:])), nil
		case 8:
			return d.order.Uint64(loc[1:]), nil
		}
	}
	return 0, fmt.Errorf("DWARF entry has an unimplemented Location op")
}

// EntryTypeOffset returns the offset in the given Entry's type attribute.
func (d *Data) EntryTypeOffset(e *Entry) (Offset, error) {
	v := e.Val(AttrType)
	if v == nil {
		return 0, fmt.Errorf("DWARF entry has no Type attribute")
	}
	off, ok := v.(Offset)
	if !ok {
		return 0, fmt.Errorf("DWARF entry has an invalid Type attribute")
	}
	return off, nil
}

// LookupPC returns the name of a symbol at the specified PC.
func (d *Data) LookupPC(pc uint64) (string, error) {
	entry, _, err := d.EntryForPC(pc)
	if err != nil {
		return "", err
	}
	nameAttr := entry.Val(AttrName)
	if nameAttr == nil {
		// TODO: this shouldn't be possible.
		return "", fmt.Errorf("LookupPC: TODO")
	}
	name, ok := nameAttr.(string)
	if !ok {
		return "", fmt.Errorf("name for PC %#x is not a string", pc)
	}
	return name, nil
}

// EntryForPC returns the entry and address for a symbol at the specified PC.
func (d *Data) EntryForPC(pc uint64) (entry *Entry, lowpc uint64, err error) {
	// TODO: do something better than a linear scan?
	r := d.Reader()
	for {
		entry, err := r.Next()
		if err != nil {
			return nil, 0, err
		}
		if entry == nil {
			// TODO: why don't we get an error here.
			break
		}
		if entry.Tag != TagSubprogram {
			continue
		}
		lowpc, lok := entry.Val(AttrLowpc).(uint64)
		highpc, hok := entry.Val(AttrHighpc).(uint64)
		if !lok || !hok || pc < lowpc || highpc <= pc {
			continue
		}
		return entry, lowpc, nil
	}
	return nil, 0, fmt.Errorf("PC %#x not found", pc)
}
