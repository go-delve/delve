package frame

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// Represents a Common Information Entry in
// the Dwarf .debug_frame section.
type CommonInformationEntry struct {
	Length                uint32
	CIE_id                uint32
	Version               uint8
	Augmentation          string
	CodeAlignmentFactor   uint64
	DataAlignmentFactor   int64
	ReturnAddressRegister uint64
	InitialInstructions   []byte
	staticBase            uint64
}

// Represents a Frame Descriptor Entry in the
// Dwarf .debug_frame section.
type FrameDescriptionEntry struct {
	Length       uint32
	CIE          *CommonInformationEntry
	Instructions []byte
	begin, size  uint64
	order        binary.ByteOrder
}

// Returns whether or not the given address is within the
// bounds of this frame.
func (fde *FrameDescriptionEntry) Cover(addr uint64) bool {
	return (addr - fde.begin) < fde.size
}

// Address of first location for this frame.
func (fde *FrameDescriptionEntry) Begin() uint64 {
	return fde.begin
}

// Address of last location for this frame.
func (fde *FrameDescriptionEntry) End() uint64 {
	return fde.begin + fde.size
}

// Set up frame for the given PC.
func (fde *FrameDescriptionEntry) EstablishFrame(pc uint64) *FrameContext {
	return executeDwarfProgramUntilPC(fde, pc)
}

type FrameDescriptionEntries []*FrameDescriptionEntry

func NewFrameIndex() FrameDescriptionEntries {
	return make(FrameDescriptionEntries, 0, 1000)
}

type ErrNoFDEForPC struct {
	PC uint64
}

func (err *ErrNoFDEForPC) Error() string {
	return fmt.Sprintf("could not find FDE for PC %#v", err.PC)
}

// Returns the Frame Description Entry for the given PC.
func (fdes FrameDescriptionEntries) FDEForPC(pc uint64) (*FrameDescriptionEntry, error) {
	idx := sort.Search(len(fdes), func(i int) bool {
		return fdes[i].Cover(pc) || fdes[i].Begin() >= pc
	})
	if idx == len(fdes) || !fdes[idx].Cover(pc) {
		return nil, &ErrNoFDEForPC{pc}
	}
	return fdes[idx], nil
}
