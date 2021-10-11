package proc

import (
	"github.com/go-delve/delve/pkg/dwarf/op"

	"golang.org/x/arch/x86/x86asm"
)

type x86Inst x86asm.Inst

// AsmDecode decodes the assembly instruction starting at mem[0:] into asmInst.
// It assumes that the Loc and AtPC fields of asmInst have already been filled.
func x86AsmDecode(asmInst *AsmInstruction, mem []byte, regs *op.DwarfRegisters, memrw MemoryReadWriter, bi *BinaryInfo, bit int) error {
	inst, err := x86asm.Decode(mem, bit)
	if err != nil {
		asmInst.Inst = (*x86Inst)(nil)
		asmInst.Size = 1
		asmInst.Bytes = mem[:asmInst.Size]
		return err
	}

	asmInst.Size = inst.Len
	asmInst.Bytes = mem[:asmInst.Size]
	patchPCRelX86(asmInst.Loc.PC, &inst)
	asmInst.Inst = (*x86Inst)(&inst)
	asmInst.Kind = OtherInstruction

	switch inst.Op {
	case x86asm.JMP, x86asm.LJMP:
		asmInst.Kind = JmpInstruction
	case x86asm.CALL, x86asm.LCALL:
		asmInst.Kind = CallInstruction
	case x86asm.RET, x86asm.LRET:
		asmInst.Kind = RetInstruction
	case x86asm.INT:
		asmInst.Kind = HardBreakInstruction
	}

	asmInst.DestLoc = resolveCallArgX86(&inst, asmInst.Loc.PC, asmInst.AtPC, regs, memrw, bi)
	return nil
}

// converts PC relative arguments to absolute addresses
func patchPCRelX86(pc uint64, inst *x86asm.Inst) {
	for i := range inst.Args {
		rel, isrel := inst.Args[i].(x86asm.Rel)
		if isrel {
			inst.Args[i] = x86asm.Imm(int64(pc) + int64(rel) + int64(inst.Len))
		}
	}
}

func (inst *x86Inst) Text(flavour AssemblyFlavour, pc uint64, symLookup func(uint64) (string, uint64)) string {
	if inst == nil {
		return "?"
	}

	var text string

	switch flavour {
	case GNUFlavour:
		text = x86asm.GNUSyntax(x86asm.Inst(*inst), pc, symLookup)
	case GoFlavour:
		text = x86asm.GoSyntax(x86asm.Inst(*inst), pc, symLookup)
	case IntelFlavour:
		fallthrough
	default:
		text = x86asm.IntelSyntax(x86asm.Inst(*inst), pc, symLookup)
	}

	return text
}

func (inst *x86Inst) OpcodeEquals(op uint64) bool {
	if inst == nil {
		return false
	}
	return uint64(inst.Op) == op
}

func resolveCallArgX86(inst *x86asm.Inst, instAddr uint64, currentGoroutine bool, regs *op.DwarfRegisters, mem MemoryReadWriter, bininfo *BinaryInfo) *Location {
	switch inst.Op {
	case x86asm.CALL, x86asm.LCALL, x86asm.JMP, x86asm.LJMP:
		// ok
	default:
		return nil
	}

	var pc uint64
	var err error

	switch arg := inst.Args[0].(type) {
	case x86asm.Imm:
		pc = uint64(arg)
	case x86asm.Reg:
		if !currentGoroutine || regs == nil {
			return nil
		}
		pc, err = bininfo.Arch.getAsmRegister(regs, int(arg))
		if err != nil {
			return nil
		}
	case x86asm.Mem:
		if !currentGoroutine || regs == nil {
			return nil
		}
		if arg.Segment != 0 {
			return nil
		}
		base, err1 := bininfo.Arch.getAsmRegister(regs, int(arg.Base))
		index, err2 := bininfo.Arch.getAsmRegister(regs, int(arg.Index))
		if err1 != nil || err2 != nil {
			return nil
		}
		addr := uint64(int64(base) + int64(index*uint64(arg.Scale)) + arg.Disp)
		pc, err = readUintRaw(mem, addr, int64(inst.MemBytes))
		if err != nil {
			return nil
		}
	default:
		return nil
	}

	file, line, fn := bininfo.PCToLine(pc)
	if fn == nil {
		return &Location{PC: pc}
	}
	return &Location{PC: pc, File: file, Line: line, Fn: fn}
}
