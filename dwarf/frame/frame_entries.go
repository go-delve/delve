package frame

import "fmt"

// Represents a Common Information Entry in
// the Dwarf .debug_frame section.
type CommonInformationEntry struct {
	Length                uint32
	CIE_id                uint32
	Version               uint8
	Augmentation          string
	CodeAlignmentFactor   uint64
	DataAlignmentFactor   int64
	ReturnAddressRegister byte
	InitialInstructions   []byte
}

type addrange struct {
	begin, end uint64
}

func (r *addrange) Cover(addr uint64) bool {
	if (addr - r.begin) < r.end {
		return true
	}

	return false
}

// Represents a Frame Descriptor Entry in the
// Dwarf .debug_frame section.
type FrameDescriptionEntry struct {
	Length       uint32
	CIE          *CommonInformationEntry
	AddressRange *addrange
	Instructions []byte
}

func (fde *FrameDescriptionEntry) EstablishFrame(pc uint64) *FrameContext {
	return executeDwarfProgramUntilPC(fde, pc)
}

func (fde *FrameDescriptionEntry) ReturnAddressOffset(pc uint64) int64 {
	frame := fde.EstablishFrame(pc)
	return frame.cfa.offset
}

type FrameDescriptionEntries []*FrameDescriptionEntry

func (fdes FrameDescriptionEntries) FDEForPC(pc uint64) (*FrameDescriptionEntry, error) {
	for _, fde := range fdes {
		if fde.AddressRange.Cover(pc) {
			return fde, nil
		}
	}

	return nil, fmt.Errorf("Could not find FDE for %#v", pc)
}
