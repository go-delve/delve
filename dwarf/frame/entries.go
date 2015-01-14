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

func (fde *FrameDescriptionEntry) Cover(addr uint64) bool {
	if (addr - fde.begin) < fde.end {
		return true
	}
	return false
}

// Represents a Frame Descriptor Entry in the
// Dwarf .debug_frame section.
type FrameDescriptionEntry struct {
	Length       uint32
	CIE          *CommonInformationEntry
	Instructions []byte
	begin, end   uint64
}

func (fde *FrameDescriptionEntry) Begin() uint64 {
	return fde.begin
}

func (fde *FrameDescriptionEntry) End() uint64 {
	return fde.begin + fde.end
}

func (fde *FrameDescriptionEntry) EstablishFrame(pc uint64) *FrameContext {
	return executeDwarfProgramUntilPC(fde, pc)
}

func (fde *FrameDescriptionEntry) ReturnAddressOffset(pc uint64) int64 {
	frame := fde.EstablishFrame(pc)
	return frame.cfa.offset + frame.regs[fde.CIE.ReturnAddressRegister].offset
}

type FrameDescriptionEntries []*FrameDescriptionEntry

func NewFrameIndex() FrameDescriptionEntries {
	return make(FrameDescriptionEntries, 0, 1000)
}

func (fdes FrameDescriptionEntries) FDEForPC(pc uint64) (*FrameDescriptionEntry, error) {
	fmt.Println("fdes for pc")
	idx := sort.Search(len(fdes), func(i int) bool {
		if fdes[i].Cover(pc) {
			return true
		}
		if fdes[i].More(pc) {
			return false
		}
		return true

	})
	if idx == len(fdes) {
		return nil, fmt.Errorf("could not find FDE for PC %#v", pc)
	}
	fmt.Println("fin")
	return fdes[idx], nil
}

func (frame *FrameDescriptionEntry) Less(pc uint64) bool {
	return frame.Begin() > pc
}

func (frame *FrameDescriptionEntry) More(pc uint64) bool {
	return frame.End() < pc
}
