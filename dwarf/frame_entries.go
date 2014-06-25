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

type FrameDescriptionEntries []*FrameDescriptionEntry

// Represents a Frame Descriptor Entry in the
// Dwarf .debug_frame section.
type FrameDescriptionEntry struct {
	Length       uint32
	CIE          *CommonInformationEntry
	AddressRange *addrange
	Instructions []byte
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

func (fdes FrameDescriptionEntries) FindReturnAddressOffset(pc uint64) (uint64, error) {
	for _, fde := range fdes {
		if fde.AddressRange.Cover(pc) {
			offset := unwind(fde, pc)
			return offset, nil
		}
	}

	return 0, fmt.Errorf("Could not find return address.")
}
