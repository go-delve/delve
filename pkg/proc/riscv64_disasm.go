package proc

import (
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"golang.org/x/arch/riscv64/riscv64asm"
)

func riscv64AsmDecode(asmInst *AsmInstruction, mem []byte, regs *op.DwarfRegisters, memrw MemoryReadWriter, bi *BinaryInfo) error {
	inst, err := riscv64asm.Decode(mem)
	if err != nil {
		asmInst.Inst = (*riscv64ArchInst)(nil)
		return err
	}

	asmInst.Size = inst.Len
	asmInst.Bytes = mem[:asmInst.Size]
	asmInst.Inst = (*riscv64ArchInst)(&inst)
	asmInst.Kind = OtherInstruction

	switch inst.Op {
	case riscv64asm.JALR:
		rd, _ := inst.Args[0].(riscv64asm.Reg)
		rs1 := inst.Args[1].(riscv64asm.RegOffset).OfsReg
		if rd == riscv64asm.X1 {
			asmInst.Kind = CallInstruction
		} else if rd == riscv64asm.X0 && rs1 == riscv64asm.X1 {
			asmInst.Kind = RetInstruction
		} else {
			asmInst.Kind = JmpInstruction
		}

	case riscv64asm.JAL:
		rd, _ := inst.Args[0].(riscv64asm.Reg)
		if rd == riscv64asm.X1 {
			asmInst.Kind = CallInstruction
		} else {
			asmInst.Kind = JmpInstruction
		}

	case riscv64asm.BEQ,
		riscv64asm.BNE,
		riscv64asm.BLT,
		riscv64asm.BGE,
		riscv64asm.BLTU,
		riscv64asm.BGEU:
		asmInst.Kind = JmpInstruction

	case riscv64asm.EBREAK:
		asmInst.Kind = HardBreakInstruction

	default:
		asmInst.Kind = OtherInstruction
	}

	asmInst.DestLoc = resolveCallArgRISCV64(&inst, asmInst.Loc.PC, asmInst.AtPC, regs, memrw, bi)

	return nil
}

func resolveCallArgRISCV64(inst *riscv64asm.Inst, instAddr uint64, currentGoroutine bool, regs *op.DwarfRegisters, mem MemoryReadWriter, bininfo *BinaryInfo) *Location {
	var pc uint64
	var err error

	switch inst.Op {
	// Format: op rs1, rs2, bimm12
	// Target: bimm12
	case riscv64asm.BEQ,
		riscv64asm.BNE,
		riscv64asm.BLT,
		riscv64asm.BGE,
		riscv64asm.BLTU,
		riscv64asm.BGEU:

		switch arg := inst.Args[2].(type) {
		case riscv64asm.Simm:
			pc = uint64(int64(instAddr) + int64(arg.Imm))
		default:
			return nil
		}

	// Format: op rd, jimm20
	// Target: simm20
	case riscv64asm.JAL:
		switch arg := inst.Args[1].(type) {
		case riscv64asm.Simm:
			pc = uint64(int64(instAddr) + int64(arg.Imm))
		default:
			return nil
		}

	// Format: op rd, rs1, imm12
	// Target: rj + offs16
	case riscv64asm.JALR:
		if !currentGoroutine || regs == nil {
			return nil
		}
		switch arg := inst.Args[1].(type) {
		case riscv64asm.RegOffset:
			pc, err = bininfo.Arch.getAsmRegister(regs, int(arg.OfsReg))
			if err != nil {
				return nil
			}
			pc = uint64(int64(pc) + int64(arg.Ofs.Imm))
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

type riscv64ArchInst riscv64asm.Inst

func (inst *riscv64ArchInst) Text(flavour AssemblyFlavour, pc uint64, symLookup func(uint64) (string, uint64)) string {
	if inst == nil {
		return "?"
	}

	var text string

	switch flavour {
	case GNUFlavour:
		text = riscv64asm.GNUSyntax(riscv64asm.Inst(*inst))
	default:
		text = riscv64asm.GoSyntax(riscv64asm.Inst(*inst), pc, symLookup, nil)
	}

	return text
}

func (inst *riscv64ArchInst) OpcodeEquals(op uint64) bool {
	if inst == nil {
		return false
	}

	return uint64(inst.Op) == op
}

var riscv64AsmRegisters = func() map[int]asmRegister {
	r := make(map[int]asmRegister)

	for i := riscv64asm.X0; i <= riscv64asm.X31; i++ {
		r[int(i)] = asmRegister{regnum.RISCV64_X0 + uint64(i), 0, 0}
	}

	return r
}()
