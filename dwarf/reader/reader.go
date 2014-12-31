package reader

import (
	"debug/dwarf"
	"fmt"
)

type Reader struct {
	*dwarf.Reader
	depth int
}

// New returns a reader for the specified dwarf data
func New(data *dwarf.Data) *Reader {
	return &Reader{data.Reader(), 0}
}

// Seek moves the reader to an arbitrary offset
func (reader *Reader) Seek(off dwarf.Offset) {
	reader.depth = 0
	reader.Reader.Seek(off)
}

// SeekToEntry moves the reader to an arbitrary entry
func (reader *Reader) SeekToEntry(entry *dwarf.Entry) error {
	reader.Seek(entry.Offset)
	// Consume the current entry so .Next works as intended
	_, err := reader.Next()
	return err
}

// SeekToFunctionEntry moves the reader to the function that includes the
// specified program counter.
func (reader *Reader) SeekToFunction(pc uint64) (*dwarf.Entry, error) {
	reader.Seek(0)
	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			return nil, err
		}

		if entry.Tag != dwarf.TagSubprogram {
			continue
		}

		lowpc, ok := entry.Val(dwarf.AttrLowpc).(uint64)
		if !ok {
			continue
		}

		highpc, ok := entry.Val(dwarf.AttrHighpc).(uint64)
		if !ok {
			continue
		}

		if lowpc <= pc && highpc >= pc {
			return entry, nil
		}
	}

	return nil, fmt.Errorf("unable to find function context")
}

// NextScopeVariable moves the reader to the next debug entry that describes a local variable
func (reader *Reader) NextScopeVariable() (*dwarf.Entry, error) {
	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			return nil, err
		}

		// All scope variables will be at the same depth
		reader.SkipChildren()

		// End of the current depth
		if entry.Tag == 0 {
			break
		}

		if entry.Tag == dwarf.TagVariable || entry.Tag == dwarf.TagFormalParameter {
			return entry, nil
		}
	}

	// No more items
	return nil, nil
}
