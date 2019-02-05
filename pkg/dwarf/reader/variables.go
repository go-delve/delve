package reader

import (
	"errors"

	"debug/dwarf"
)

// RelAddr is an address relative to the static base. For normal executables
// this is just a normal memory address, for PIE it's a relative address.
type RelAddr uint64

func ToRelAddr(addr uint64, staticBase uint64) RelAddr {
	return RelAddr(addr - staticBase)
}

// VariableReader provides a way of reading the local variables and formal
// parameters of a function that are visible at the specified PC address.
type VariableReader struct {
	dwarf  *dwarf.Data
	reader *dwarf.Reader
	entry  *dwarf.Entry
	depth  int
	pc     uint64
	line   int
	err    error

	onlyVisible            bool
	skipInlinedSubroutines bool
}

// Variables returns a VariableReader for the function or lexical block at off.
// If onlyVisible is true only variables visible at pc will be returned by
// the VariableReader.
func Variables(dwarf *dwarf.Data, off dwarf.Offset, pc RelAddr, line int, onlyVisible, skipInlinedSubroutines bool) *VariableReader {
	reader := dwarf.Reader()
	reader.Seek(off)
	return &VariableReader{dwarf: dwarf, reader: reader, entry: nil, depth: 0, onlyVisible: onlyVisible, skipInlinedSubroutines: skipInlinedSubroutines, pc: uint64(pc), line: line, err: nil}
}

// Next reads the next variable entry, returns false if there aren't any.
func (vrdr *VariableReader) Next() bool {
	if vrdr.err != nil {
		return false
	}

	for {
		vrdr.entry, vrdr.err = vrdr.reader.Next()
		if vrdr.entry == nil || vrdr.err != nil {
			return false
		}

		switch vrdr.entry.Tag {
		case 0:
			vrdr.depth--
			if vrdr.depth == 0 {
				return false
			}

		case dwarf.TagInlinedSubroutine:
			if vrdr.skipInlinedSubroutines {
				vrdr.reader.SkipChildren()
				continue
			}
			fallthrough
		case dwarf.TagLexDwarfBlock, dwarf.TagSubprogram:

			recur := true
			if vrdr.onlyVisible {
				recur, vrdr.err = entryRangesContains(vrdr.dwarf, vrdr.entry, vrdr.pc)
				if vrdr.err != nil {
					return false
				}
			}

			if recur && vrdr.entry.Children {
				vrdr.depth++
			} else {
				if vrdr.depth == 0 {
					return false
				}
				vrdr.reader.SkipChildren()
			}

		default:
			if vrdr.depth == 0 {
				vrdr.err = errors.New("offset was not lexical block or subprogram")
				return false
			}
			if declLine, ok := vrdr.entry.Val(dwarf.AttrDeclLine).(int64); !ok || vrdr.line >= int(declLine) {
				return true
			}
		}
	}
}

func entryRangesContains(dwarf *dwarf.Data, entry *dwarf.Entry, pc uint64) (bool, error) {
	rngs, err := dwarf.Ranges(entry)
	if err != nil {
		return false, err
	}
	for _, rng := range rngs {
		if pc >= rng[0] && pc < rng[1] {
			return true, nil
		}
	}
	return false, nil
}

// Entry returns the current variable entry.
func (vrdr *VariableReader) Entry() *dwarf.Entry {
	return vrdr.entry
}

// Depth returns the depth of the current scope
func (vrdr *VariableReader) Depth() int {
	return vrdr.depth
}

// Err returns the error if there was one.
func (vrdr *VariableReader) Err() error {
	return vrdr.err
}
