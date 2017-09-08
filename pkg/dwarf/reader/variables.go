package reader

import (
	"errors"

	"debug/dwarf"
)

// VariableReader provides a way of reading the local variables and formal
// parameters of a function that are visible at the specified PC address.
type VariableReader struct {
	dwarf       *dwarf.Data
	reader      *dwarf.Reader
	entry       *dwarf.Entry
	depth       int
	onlyVisible bool
	pc          uint64
	line        int
	err         error
}

// Variables returns a VariableReader for the function or lexical block at off.
// If onlyVisible is true only variables visible at pc will be returned by
// the VariableReader.
func Variables(dwarf *dwarf.Data, off dwarf.Offset, pc uint64, line int, onlyVisible bool) *VariableReader {
	reader := dwarf.Reader()
	reader.Seek(off)
	return &VariableReader{dwarf: dwarf, reader: reader, entry: nil, depth: 0, onlyVisible: onlyVisible, pc: pc, line: line, err: nil}
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

		case dwarf.TagLexDwarfBlock, dwarf.TagSubprogram:
			recur := true
			if vrdr.onlyVisible {
				recur, vrdr.err = vrdr.entryRangesContains()
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

func (vrdr *VariableReader) entryRangesContains() (bool, error) {
	rngs, err := vrdr.dwarf.Ranges(vrdr.entry)
	if err != nil {
		return false, err
	}
	for _, rng := range rngs {
		if vrdr.pc >= rng[0] && vrdr.pc < rng[1] {
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
