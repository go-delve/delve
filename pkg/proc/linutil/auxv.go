package linutil

import (
	"bytes"
	"encoding/binary"
)

const (
	_AT_NULL  = 0
	_AT_BASE  = 7
	_AT_ENTRY = 9
)

// searchAuxv searches the elf auxiliary vector for the given tag and returns
// its value. Returns 0 if the tag is not found.
// For a description of the auxiliary vector (auxv) format see:
// System V Application Binary Interface, AMD64 Architecture Processor
// Supplement, section 3.4.3.
// System V Application Binary Interface, Intel386 Architecture Processor
// Supplement (fourth edition), section 3-28.
func searchAuxv(auxv []byte, ptrSize int, target uint64) uint64 {
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
		case target:
			return val
		}
	}
}

// BaseFromAuxv searches the elf auxiliary vector for the AT_BASE value,
// which is the base address of the dynamic linker (interpreter).
func BaseFromAuxv(auxv []byte, ptrSize int) uint64 {
	return searchAuxv(auxv, ptrSize, _AT_BASE)
}

// EntryPointFromAuxv searches the elf auxiliary vector for the entry point
// address.
func EntryPointFromAuxv(auxv []byte, ptrSize int) uint64 {
	return searchAuxv(auxv, ptrSize, _AT_ENTRY)
}
