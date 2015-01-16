package frame

import (
	"fmt"

	"github.com/derekparker/rbtree"
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

type FrameDescriptionEntries struct {
	*rbtree.RedBlackTree
}

func NewFrameIndex() *FrameDescriptionEntries {
	return &FrameDescriptionEntries{rbtree.New()}
}

func (fdes *FrameDescriptionEntries) FDEForPC(pc uint64) (*FrameDescriptionEntry, error) {
	node, ok := fdes.Find(Addr(pc))
	if !ok {
		return nil, fmt.Errorf("Could not find FDE for %#v", pc)
	}

	return node.(*FrameDescriptionEntry), nil
}

func (frame *FrameDescriptionEntry) Less(item rbtree.Item) bool {
	return frame.Begin() < item.(*FrameDescriptionEntry).Begin()
}

func (frame *FrameDescriptionEntry) More(item rbtree.Item) bool {
	f := item.(*FrameDescriptionEntry)
	return frame.End() > f.End()
}

type Addr uint64

func (a Addr) Less(item rbtree.Item) bool {
	return uint64(a) < item.(*FrameDescriptionEntry).Begin()
}

func (a Addr) More(item rbtree.Item) bool {
	f := item.(*FrameDescriptionEntry)
	return uint64(a) > f.End()
}
