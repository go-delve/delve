package frame

import (
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
}

// Represents a Frame Descriptor Entry in the
// Dwarf .debug_frame section.
type FrameDescriptionEntry struct {
	Length       uint32
	CIE          *CommonInformationEntry
	Instructions []byte
	begin, end   uint64
}

// Returns whether or not the given address is within the
// bounds of this frame.
func (fde *FrameDescriptionEntry) Cover(addr uint64) bool {
	if (addr - fde.begin) < fde.end {
		return true
	}
	return false
}

// Address of first location for this frame.
func (fde *FrameDescriptionEntry) Begin() uint64 {
	return fde.begin
}

// Address of last location for this frame.
func (fde *FrameDescriptionEntry) End() uint64 {
	return fde.begin + fde.end
}

// Set up frame for the given PC.
func (fde *FrameDescriptionEntry) EstablishFrame(pc uint64) *FrameContext {
	return executeDwarfProgramUntilPC(fde, pc)
}

// Return the offset from the current SP that the return address is stored at.
func (fde *FrameDescriptionEntry) ReturnAddressOffset(pc uint64) (frameOffset, returnAddressOffset int64) {
	frame := fde.EstablishFrame(pc)
	return frame.cfa.offset, frame.regs[fde.CIE.ReturnAddressRegister].offset
}

type FrameDescriptionEntries []*FrameDescriptionEntry

func NewFrameIndex() FrameDescriptionEntries {
	return make(FrameDescriptionEntries, 0, 1000)
}

// Returns the Frame Description Entry for the given PC.
func (fdes FrameDescriptionEntries) FDEForPC(pc uint64) (*FrameDescriptionEntry, error) {
	idx := sort.Search(len(fdes), func(i int) bool {
		if fdes[i].Cover(pc) {
			return true
		}
		if fdes[i].LessThan(pc) {
			return false
		}
		return true
	})
	if idx == len(fdes) {
		return nil, fmt.Errorf("could not find FDE for PC %#v", pc)
	}
	return fdes[idx], nil
}

func (frame *FrameDescriptionEntry) LessThan(pc uint64) bool {
	return frame.End() <= pc
}
