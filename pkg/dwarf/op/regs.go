package op

import (
	"bytes"
	"encoding/binary"
)

type DwarfRegisters struct {
	StaticBase uint64

	CFA       int64
	FrameBase int64
	ObjBase   int64
	Regs      []*DwarfRegister

	ByteOrder binary.ByteOrder
	PCRegNum  uint64
	SPRegNum  uint64
	BPRegNum  uint64
	LRRegNum  uint64
}

type DwarfRegister struct {
	Uint64Val uint64
	Bytes     []byte
}

// Uint64Val returns the uint64 value of register idx.
func (regs *DwarfRegisters) Uint64Val(idx uint64) uint64 {
	reg := regs.Reg(idx)
	if reg == nil {
		return 0
	}
	return regs.Regs[idx].Uint64Val
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

// Reg returns register idx or nil if the register is not defined.
func (regs *DwarfRegisters) Reg(idx uint64) *DwarfRegister {
	if idx >= uint64(len(regs.Regs)) {
		return nil
	}
	return regs.Regs[idx]
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

func (regs *DwarfRegisters) LR() uint64 {
	return regs.Uint64Val(regs.LRRegNum)
}

// AddReg adds register idx to regs.
func (regs *DwarfRegisters) AddReg(idx uint64, reg *DwarfRegister) {
	if idx >= uint64(len(regs.Regs)) {
		newRegs := make([]*DwarfRegister, idx+1)
		copy(newRegs, regs.Regs)
		regs.Regs = newRegs
	}
	regs.Regs[idx] = reg
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
