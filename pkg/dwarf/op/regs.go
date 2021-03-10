package op

import (
	"bytes"
	"encoding/binary"
)

// DwarfRegisters holds the value of stack program registers.
type DwarfRegisters struct {
	StaticBase uint64

	CFA       int64
	FrameBase int64
	ObjBase   int64
	regs      []*DwarfRegister

	ByteOrder  binary.ByteOrder
	PCRegNum   uint64
	SPRegNum   uint64
	BPRegNum   uint64
	LRRegNum   uint64
	ChangeFunc RegisterChangeFunc

	FloatLoadError   error // error produced when loading floating point registers
	loadMoreCallback func()
}

type DwarfRegister struct {
	Uint64Val uint64
	Bytes     []byte
}

type RegisterChangeFunc func(regNum uint64, reg *DwarfRegister) error

// NewDwarfRegisters returns a new DwarfRegisters object.
func NewDwarfRegisters(staticBase uint64, regs []*DwarfRegister, byteOrder binary.ByteOrder, pcRegNum, spRegNum, bpRegNum, lrRegNum uint64) *DwarfRegisters {
	return &DwarfRegisters{
		StaticBase: staticBase,
		regs:       regs,
		ByteOrder:  byteOrder,
		PCRegNum:   pcRegNum,
		SPRegNum:   spRegNum,
		BPRegNum:   bpRegNum,
		LRRegNum:   lrRegNum,
	}
}

// SetLoadMoreCallback sets a callback function that will be called the
// first time the user of regs tries to access an undefined register.
func (regs *DwarfRegisters) SetLoadMoreCallback(fn func()) {
	regs.loadMoreCallback = fn
}

// CurrentSize returns the current number of known registers. This number might be
// wrong if loadMoreCallback has been set.
func (regs *DwarfRegisters) CurrentSize() int {
	return len(regs.regs)
}

// Uint64Val returns the uint64 value of register idx.
func (regs *DwarfRegisters) Uint64Val(idx uint64) uint64 {
	reg := regs.Reg(idx)
	if reg == nil {
		return 0
	}
	return regs.regs[idx].Uint64Val
}

// Bytes returns the bytes value of register idx, nil if the register is not
// defined.
func (regs *DwarfRegisters) Bytes(idx uint64) []byte {
	reg := regs.Reg(idx)
	if reg == nil {
		return nil
	}
	if reg.Bytes == nil {
		var buf bytes.Buffer
		binary.Write(&buf, regs.ByteOrder, reg.Uint64Val)
		reg.Bytes = buf.Bytes()
	}
	return reg.Bytes
}

func (regs *DwarfRegisters) loadMore() {
	if regs.loadMoreCallback == nil {
		return
	}
	regs.loadMoreCallback()
	regs.loadMoreCallback = nil
}

// Reg returns register idx or nil if the register is not defined.
func (regs *DwarfRegisters) Reg(idx uint64) *DwarfRegister {
	if idx >= uint64(len(regs.regs)) {
		regs.loadMore()
		if idx >= uint64(len(regs.regs)) {
			return nil
		}
	}
	if regs.regs[idx] == nil {
		regs.loadMore()
	}
	return regs.regs[idx]
}

func (regs *DwarfRegisters) PC() uint64 {
	return regs.Uint64Val(regs.PCRegNum)
}

func (regs *DwarfRegisters) SP() uint64 {
	return regs.Uint64Val(regs.SPRegNum)
}

func (regs *DwarfRegisters) BP() uint64 {
	return regs.Uint64Val(regs.BPRegNum)
}

// AddReg adds register idx to regs.
func (regs *DwarfRegisters) AddReg(idx uint64, reg *DwarfRegister) {
	if idx >= uint64(len(regs.regs)) {
		newRegs := make([]*DwarfRegister, idx+1)
		copy(newRegs, regs.regs)
		regs.regs = newRegs
	}
	regs.regs[idx] = reg
}

// ClearRegisters clears all registers.
func (regs *DwarfRegisters) ClearRegisters() {
	regs.loadMoreCallback = nil
	for regnum := range regs.regs {
		regs.regs[regnum] = nil
	}
}

func DwarfRegisterFromUint64(v uint64) *DwarfRegister {
	return &DwarfRegister{Uint64Val: v}
}

func DwarfRegisterFromBytes(bytes []byte) *DwarfRegister {
	var v uint64
	switch len(bytes) {
	case 1:
		v = uint64(bytes[0])
	case 2:
		x := binary.LittleEndian.Uint16(bytes)
		v = uint64(x)
	case 4:
		x := binary.LittleEndian.Uint32(bytes)
		v = uint64(x)
	default:
		if len(bytes) >= 8 {
			v = binary.LittleEndian.Uint64(bytes[:8])
		}
	}
	return &DwarfRegister{Uint64Val: v, Bytes: bytes}
}

// FillBytes fills the Bytes slice of reg using Uint64Val.
func (reg *DwarfRegister) FillBytes() {
	if reg.Bytes != nil {
		return
	}
	reg.Bytes = make([]byte, 8)
	binary.LittleEndian.PutUint64(reg.Bytes, reg.Uint64Val)
}
