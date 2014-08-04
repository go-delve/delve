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
	ReturnAddressRegister uint64
	InitialInstructions   []byte
}

type addrange struct {
	begin, end uint64
}

func (r *addrange) Begin() uint64 {
	return r.begin
}

func (r *addrange) End() uint64 {
	return r.end
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

	return frame.cfa.offset + frame.regs[fde.CIE.ReturnAddressRegister].offset
}

const (
	RED   = true
	BLACK = false
)

type FrameDescriptionEntries struct {
	root *FrameNode
}

type FrameNode struct {
	entry       *FrameDescriptionEntry
	left, right *FrameNode
	color       bool
}

func NewFrameIndex() *FrameDescriptionEntries {
	return &FrameDescriptionEntries{}
}

func (fs *FrameDescriptionEntries) Find(pc uint64) (*FrameDescriptionEntry, bool) {
	return find(fs.root, pc)
}

func find(fn *FrameNode, pc uint64) (*FrameDescriptionEntry, bool) {
	switch {
	case fn == nil:
		return nil, false
	case fn.entry.AddressRange.Cover(pc):
		return fn.entry, true
	case pc < fn.entry.AddressRange.begin:
		return find(fn.left, pc)
	case pc > fn.entry.AddressRange.begin+fn.entry.AddressRange.end:
		return find(fn.right, pc)
	}

	return nil, false
}

func (fs *FrameDescriptionEntries) Put(entry *FrameDescriptionEntry) {
	fs.root = put(fs.root, entry)
	fs.root.color = BLACK
}

func put(fn *FrameNode, entry *FrameDescriptionEntry) *FrameNode {
	switch {
	case fn == nil:
		return &FrameNode{entry: entry, color: RED}
	case entry.AddressRange.begin < fn.entry.AddressRange.begin:
		fn.left = put(fn.left, entry)
	case entry.AddressRange.begin > fn.entry.AddressRange.begin:
		fn.right = put(fn.right, entry)
	}

	leftRed := isRed(fn.left)
	rightRed := isRed(fn.right)

	if !leftRed && rightRed {
		fn = rotateLeft(fn)
	} else if leftRed && isRed(fn.left.left) {
		fn = rotateRight(fn)
	}

	if leftRed && rightRed {
		fn.left.color = BLACK
		fn.right.color = BLACK
		fn.color = RED
	}

	return fn
}

func isRed(fn *FrameNode) bool {
	if fn == nil {
		return false
	}

	return fn.color
}

func rotateLeft(fn *FrameNode) *FrameNode {
	x := fn.right
	fn.right = x.left
	x.left = fn

	x.color = fn.color
	fn.color = RED

	return x
}

func rotateRight(fn *FrameNode) *FrameNode {
	x := fn.left
	fn.left = x.right
	x.right = fn

	x.color = fn.color
	fn.color = RED

	return x
}

func (fdes FrameDescriptionEntries) FDEForPC(pc uint64) (*FrameDescriptionEntry, error) {
	fde, ok := fdes.Find(pc)
	if !ok {
		return nil, fmt.Errorf("Could not find FDE for %#v", pc)
	}

	return fde, nil
}
