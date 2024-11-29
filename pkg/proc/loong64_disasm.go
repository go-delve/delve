package proc

import (
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"golang.org/x/arch/loong64/loong64asm"
)

func loong64AsmDecode(asmInst *AsmInstruction, mem []byte, regs *op.DwarfRegisters, memrw MemoryReadWriter, bi *BinaryInfo) error {
	asmInst.Size = 4
	asmInst.Bytes = mem[:asmInst.Size]

	inst, err := loong64asm.Decode(mem)
	if err != nil {
		asmInst.Inst = (*loong64ArchInst)(nil)
		return err
	}

	asmInst.Inst = (*loong64ArchInst)(&inst)
	asmInst.Kind = OtherInstruction

	switch inst.Op {
	case loong64asm.JIRL:
		rd, _ := inst.Args[0].(loong64asm.Reg)
		rj, _ := inst.Args[1].(loong64asm.Reg)
		if rd == loong64asm.R1 {
			asmInst.Kind = CallInstruction
		} else if rd == loong64asm.R0 && rj == loong64asm.R1 {
			asmInst.Kind = RetInstruction
		} else {
			asmInst.Kind = JmpInstruction
		}

	case loong64asm.BL:
		asmInst.Kind = CallInstruction

	case loong64asm.BEQZ,
		loong64asm.BNEZ,
		loong64asm.BCEQZ,
		loong64asm.BCNEZ,
		loong64asm.B,
		loong64asm.BEQ,
		loong64asm.BNE,
		loong64asm.BLT,
		loong64asm.BGE,
		loong64asm.BLTU,
		loong64asm.BGEU:
		asmInst.Kind = JmpInstruction

	case loong64asm.BREAK:
		asmInst.Kind = HardBreakInstruction

	default:
		asmInst.Kind = OtherInstruction
	}

	asmInst.DestLoc = resolveCallArgLOONG64(&inst, asmInst.Loc.PC, asmInst.AtPC, regs, memrw, bi)

	return nil
}

func resolveCallArgLOONG64(inst *loong64asm.Inst, instAddr uint64, currentGoroutine bool, regs *op.DwarfRegisters, mem MemoryReadWriter, bininfo *BinaryInfo) *Location {
	var pc uint64
	var err error

	switch inst.Op {
	// Format: op offs26
	// Target: offs26
	case loong64asm.B, loong64asm.BL:
		switch arg := inst.Args[0].(type) {
		case loong64asm.OffsetSimm:
			pc = uint64(int64(instAddr) + int64(arg.Imm))
		default:
			return nil
		}

	// Format: op rd,rj,offs16
	// Target: offs16
	case loong64asm.BEQ,
		loong64asm.BNE,
		loong64asm.BLT,
		loong64asm.BGE,
		loong64asm.BLTU,
		loong64asm.BGEU:

		switch arg := inst.Args[2].(type) {
		case loong64asm.OffsetSimm:
			pc = uint64(int64(instAddr) + int64(arg.Imm))
		default:
			return nil
		}

	// Format: op rd,rj,offs16
	// Target: rj + offs16
	case loong64asm.JIRL:
		if !currentGoroutine || regs == nil {
			return nil
		}
		switch arg1 := inst.Args[1].(type) {
		case loong64asm.Reg:
			switch arg2 := inst.Args[2].(type) {
			case loong64asm.OffsetSimm:
				pc, err = bininfo.Arch.getAsmRegister(regs, int(arg1))
				if err != nil {
					return nil
				}
				pc = uint64(int64(pc) + int64(arg2.Imm))
			}
		}

	// Format: op rj,offs21
	// Target: offs21
	case loong64asm.BEQZ,
		loong64asm.BNEZ,
		loong64asm.BCEQZ,
		loong64asm.BCNEZ:

		if (!currentGoroutine) || (regs == nil) {
			return nil
		}

		switch arg := inst.Args[1].(type) {
		case loong64asm.OffsetSimm:
			pc = uint64(int64(instAddr) + int64(arg.Imm))
		default:
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

type loong64ArchInst loong64asm.Inst

func (inst *loong64ArchInst) Text(flavour AssemblyFlavour, pc uint64, symLookup func(uint64) (string, uint64)) string {
	if inst == nil {
		return "?"
	}

	var text string

	switch flavour {
	case GNUFlavour:
		text = loong64asm.GNUSyntax(loong64asm.Inst(*inst))
	default:
		text = loong64asm.GoSyntax(loong64asm.Inst(*inst), pc, symLookup)
	}

	return text
}

func (inst *loong64ArchInst) OpcodeEquals(op uint64) bool {
	if inst == nil {
		return false
	}

	return uint64(inst.Op) == op
}

var loong64AsmRegisters = func() map[int]asmRegister {
	r := make(map[int]asmRegister)

	for i := loong64asm.R0; i <= loong64asm.R31; i++ {
		r[int(i)] = asmRegister{regnum.LOONG64_R0 + uint64(i), 0, 0}
	}

	return r
}()
