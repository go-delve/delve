package proctl

import (
	"debug/dwarf"
	"fmt"
)

// Retrieve the type entry using the offset in another entry such as a variable or member
func typeEntryFromEntry(entry *dwarf.Entry, reader *dwarf.Reader) (*dwarf.Entry, error) {
	offset, ok := entry.Val(dwarf.AttrType).(dwarf.Offset)
	if !ok {
		return nil, fmt.Errorf("unable to retrieve type entry offset")
	}
	reader.Seek(offset)
	return reader.Next()
}

// Same as typeEntryFromEntry but resolves the type down to its base type optionally turning pointer types into their base types
func baseTypeEntryFromEntry(entry *dwarf.Entry, reader *dwarf.Reader, resolvePointerTypes bool) (*dwarf.Entry, error) {
	typeEntry, err := typeEntryFromEntry(entry, reader)
	if err != nil {
		return nil, err
	}
	return resolveTypeEntry(typeEntry, reader, resolvePointerTypes)
}

// Resolves a type entry to its base type, optionally turning pointer types into their base types
func resolveTypeEntry(entry *dwarf.Entry, reader *dwarf.Reader, resolvePointerTypes bool) (*dwarf.Entry, error) {
	var err error
	baseEntry := entry

	// Keep following the type attribute until the base is found, which will not contain one
	for offset, ok := baseEntry.Val(dwarf.AttrType).(dwarf.Offset); ok; offset, ok = baseEntry.Val(dwarf.AttrType).(dwarf.Offset) {
		// we have reached the base and don't want to follow pointers
		if baseEntry.Tag == dwarf.TagPointerType && !resolvePointerTypes {
			break
		}

		reader.Seek(offset)
		baseEntry, err = reader.Next()

		if err != nil {
			return nil, err
		}
	}

	return baseEntry, nil
}

// Find the type member data entry from a struct
func findStructMemberEntry(entry *dwarf.Entry, reader *dwarf.Reader, member string) (*dwarf.Entry, error) {

	typeEntry, err := baseTypeEntryFromEntry(entry, reader, true)
	if err != nil {
		return nil, err
	}

	if typeEntry.Tag != dwarf.TagStructType {
		return nil, fmt.Errorf("not a struct type")
	}

	reader.Seek(typeEntry.Offset)
	// Descend into child nodes
	_, err = reader.Next()
	if err != nil {
		return nil, err
	}

	for memberEntry, err := reader.Next(); memberEntry != nil; memberEntry, err = reader.Next() {
		if err != nil {
			return nil, err
		}

		if memberEntry.Tag == 0 {
			return nil, fmt.Errorf("member variable %s not found", member)
		}

		if memberEntry.Tag == dwarf.TagMember {
			n, ok := memberEntry.Val(dwarf.AttrName).(string)
			if ok && n == member {
				return memberEntry, nil
			}
		}

		// All members will be top level
		reader.SkipChildren()
	}

	return nil, fmt.Errorf("member variable %s not found", member)
}
