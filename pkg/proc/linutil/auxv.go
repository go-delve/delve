package linutil

import (
	"bytes"
	"encoding/binary"
)

const (
	_AT_NULL  = 0
	_AT_ENTRY = 9
)

// EntryPointFromAuxv searches the elf auxiliary vector for the entry point
// address.
// For a description of the auxiliary vector (auxv) format see:
// System V Application Binary Interface, AMD64 Architecture Processor
// Supplement, section 3.4.3.
// System V Application Binary Interface, Intel386 Architecture Processor
// Supplement (fourth edition), section 3-28.
func EntryPointFromAuxv(auxv []byte, ptrSize int) uint64 {
	rd := bytes.NewBuffer(auxv)

	for {
		tag, err := readUintRaw(rd, binary.LittleEndian, ptrSize)
		if err != nil {
			return 0
		}
		val, err := readUintRaw(rd, binary.LittleEndian, ptrSize)
		if err != nil {
			return 0
		}

		switch tag {
		case _AT_NULL:
			return 0
		case _AT_ENTRY:
			return val
		}
	}
}
