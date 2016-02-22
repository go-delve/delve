package reader

import (
	"errors"
	"fmt"
	"golang.org/x/debug/dwarf"

	"github.com/derekparker/delve/dwarf/op"
)

type Reader struct {
	*dwarf.Reader
	depth int
}

// New returns a reader for the specified dwarf data.
func New(data *dwarf.Data) *Reader {
	return &Reader{data.Reader(), 0}
}

// Seek moves the reader to an arbitrary offset.
func (reader *Reader) Seek(off dwarf.Offset) {
	reader.depth = 0
	reader.Reader.Seek(off)
}

// SeekToEntry moves the reader to an arbitrary entry.
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

		if lowpc <= pc && highpc > pc {
			return entry, nil
		}
	}

	return nil, fmt.Errorf("unable to find function context")
}

// Returns the address for the named entry.
func (reader *Reader) AddrFor(name string) (uint64, error) {
	entry, err := reader.FindEntryNamed(name, false)
	if err != nil {
		return 0, err
	}
	instructions, ok := entry.Val(dwarf.AttrLocation).([]byte)
	if !ok {
		return 0, fmt.Errorf("type assertion failed")
	}
	addr, err := op.ExecuteStackProgram(0, instructions)
	if err != nil {
		return 0, err
	}
	return uint64(addr), nil
}

// Returns the address for the named struct member. Expects the reader to be at the parent entry
// or one of the parents children, thus does not seek to parent by itself.
func (reader *Reader) AddrForMember(member string, initialInstructions []byte) (uint64, error) {
	for {
		entry, err := reader.NextMemberVariable()
		if err != nil {
			return 0, err
		}
		if entry == nil {
			return 0, fmt.Errorf("nil entry for member named %s", member)
		}
		name, ok := entry.Val(dwarf.AttrName).(string)
		if !ok || name != member {
			continue
		}
		instructions, ok := entry.Val(dwarf.AttrDataMemberLoc).([]byte)
		if !ok {
			continue
		}
		addr, err := op.ExecuteStackProgram(0, append(initialInstructions, instructions...))
		return uint64(addr), err
	}
}

var TypeNotFoundErr = errors.New("no type entry found")

// SeekToType moves the reader to the type specified by the entry,
// optionally resolving typedefs and pointer types. If the reader is set
// to a struct type the NextMemberVariable call can be used to walk all member data.
func (reader *Reader) SeekToType(entry *dwarf.Entry, resolveTypedefs bool, resolvePointerTypes bool) (*dwarf.Entry, error) {
	offset, ok := entry.Val(dwarf.AttrType).(dwarf.Offset)
	if !ok {
		return nil, fmt.Errorf("entry does not have a type attribute")
	}

	// Seek to the first type offset
	reader.Seek(offset)

	// Walk the types to the base
	for typeEntry, err := reader.Next(); typeEntry != nil; typeEntry, err = reader.Next() {
		if err != nil {
			return nil, err
		}

		if typeEntry.Tag == dwarf.TagTypedef && !resolveTypedefs {
			return typeEntry, nil
		}

		if typeEntry.Tag == dwarf.TagPointerType && !resolvePointerTypes {
			return typeEntry, nil
		}

		offset, ok = typeEntry.Val(dwarf.AttrType).(dwarf.Offset)
		if !ok {
			return typeEntry, nil
		}

		reader.Seek(offset)
	}

	return nil, TypeNotFoundErr
}

func (reader *Reader) NextType() (*dwarf.Entry, error) {
	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			return nil, err
		}

		switch entry.Tag {
		case dwarf.TagArrayType, dwarf.TagBaseType, dwarf.TagClassType, dwarf.TagStructType, dwarf.TagUnionType, dwarf.TagConstType, dwarf.TagVolatileType, dwarf.TagRestrictType, dwarf.TagEnumerationType, dwarf.TagPointerType, dwarf.TagSubroutineType, dwarf.TagTypedef, dwarf.TagUnspecifiedType:
			return entry, nil
		}
	}

	return nil, nil
}

// SeekToTypeNamed moves the reader to the type specified by the name.
// If the reader is set to a struct type the NextMemberVariable call
// can be used to walk all member data.
func (reader *Reader) SeekToTypeNamed(name string) (*dwarf.Entry, error) {
	// Walk the types to the base
	for entry, err := reader.NextType(); entry != nil; entry, err = reader.NextType() {
		if err != nil {
			return nil, err
		}

		n, ok := entry.Val(dwarf.AttrName).(string)
		if !ok {
			continue
		}

		if n == name {
			return entry, nil
		}
	}

	return nil, TypeNotFoundErr
}

// Finds the entry for 'name'.
func (reader *Reader) FindEntryNamed(name string, member bool) (*dwarf.Entry, error) {
	depth := 1
	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			return nil, err
		}

		if entry.Children {
			depth++
		}

		if entry.Tag == 0 {
			depth--
			if depth <= 0 {
				return nil, fmt.Errorf("could not find symbol value for %s", name)
			}
		}

		if member {
			if entry.Tag != dwarf.TagMember {
				continue
			}
		} else {
			if entry.Tag != dwarf.TagVariable && entry.Tag != dwarf.TagFormalParameter && entry.Tag != dwarf.TagStructType {
				continue
			}
		}

		n, ok := entry.Val(dwarf.AttrName).(string)
		if !ok || n != name {
			continue
		}
		return entry, nil
	}
	return nil, fmt.Errorf("could not find symbol value for %s", name)
}

func (reader *Reader) InstructionsForEntryNamed(name string, member bool) ([]byte, error) {
	entry, err := reader.FindEntryNamed(name, member)
	if err != nil {
		return nil, err
	}
	var attr dwarf.Attr
	if member {
		attr = dwarf.AttrDataMemberLoc
	} else {
		attr = dwarf.AttrLocation
	}
	instr, ok := entry.Val(attr).([]byte)
	if !ok {
		return nil, errors.New("invalid typecast for Dwarf instructions")
	}
	return instr, nil
}

func (reader *Reader) InstructionsForEntry(entry *dwarf.Entry) ([]byte, error) {
	if entry.Tag == dwarf.TagMember {
		instructions, ok := entry.Val(dwarf.AttrDataMemberLoc).([]byte)
		if !ok {
			return nil, fmt.Errorf("member data has no data member location attribute")
		}
		// clone slice to prevent stomping on the dwarf data
		return append([]byte{}, instructions...), nil
	}

	// non-member
	instructions, ok := entry.Val(dwarf.AttrLocation).([]byte)
	if !ok {
		return nil, fmt.Errorf("entry has no location attribute")
	}

	// clone slice to prevent stomping on the dwarf data
	return append([]byte{}, instructions...), nil
}

// NextScopeVariable moves the reader to the next debug entry that describes a local variable and returns the entry.
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

// NextMememberVariable moves the reader to the next debug entry that describes a member variable and returns the entry.
func (reader *Reader) NextMemberVariable() (*dwarf.Entry, error) {
	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			return nil, err
		}

		// All member variables will be at the same depth
		reader.SkipChildren()

		// End of the current depth
		if entry.Tag == 0 {
			break
		}

		if entry.Tag == dwarf.TagMember {
			return entry, nil
		}
	}

	// No more items
	return nil, nil
}

// NextPackageVariable moves the reader to the next debug entry that describes a package variable.
// Any TagVariable entry that is not inside a sub prgram entry and is marked external is considered a package variable.
func (reader *Reader) NextPackageVariable() (*dwarf.Entry, error) {
	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			return nil, err
		}

		if entry.Tag == dwarf.TagVariable {
			ext, ok := entry.Val(dwarf.AttrExternal).(bool)
			if ok && ext {
				return entry, nil
			}
		}

		// Ignore everything inside sub programs
		if entry.Tag == dwarf.TagSubprogram {
			reader.SkipChildren()
		}
	}

	// No more items
	return nil, nil
}

func (reader *Reader) NextCompileUnit() (*dwarf.Entry, error) {
	for entry, err := reader.Next(); entry != nil; entry, err = reader.Next() {
		if err != nil {
			return nil, err
		}

		if entry.Tag == dwarf.TagCompileUnit {
			return entry, nil
		}
	}

	return nil, nil
}
