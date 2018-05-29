package linutil

import (
	"bytes"
	"encoding/binary"
)

const (
	_AT_NULL_AMD64  = 0
	_AT_ENTRY_AMD64 = 9
)

// EntryPointFromAuxv searches the elf auxiliary vector for the entry point
// address.
// For a description of the auxiliary vector (auxv) format see:
// System V Application Binary Interface, AMD64 Architecture Processor
// Supplement, section 3.4.3
func EntryPointFromAuxvAMD64(auxv []byte) uint64 {
	rd := bytes.NewBuffer(auxv)

	for {
		var tag, val uint64
		err := binary.Read(rd, binary.LittleEndian, &tag)
		if err != nil {
			return 0
		}
		err = binary.Read(rd, binary.LittleEndian, &val)
		if err != nil {
			return 0
		}

		switch tag {
		case _AT_NULL_AMD64:
			return 0
		case _AT_ENTRY_AMD64:
			return val
		}
	}
}
