// TODO: disassembler support should be compiled in unconditionally,
// instead of being decided by the build-target architecture, and be
// part of the Arch object instead.

package proc

import (
	"fmt"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"golang.org/x/arch/loong64/loong64asm"
)

var (
	errType = fmt.Errorf("unknown type")
)

func loong64AsmDecode(asmInst *AsmInstruction, mem []byte, regs *op.DwarfRegisters,
	memrw MemoryReadWriter, bi *BinaryInfo) error {

	asmInst.Size = 4
	asmInst.Bytes = mem[:asmInst.Size]

	inst, err := loong64asm.Decode(mem)
	if err != nil {
		asmInst.Inst = (*loong64ArchInst)(nil)
		return err
	}

	asmInst.Inst = (*loong64ArchInst)(&inst)

	switch inst.Op {
	case loong64asm.BL:
		//write return addr to ra
		asmInst.Kind = CallInstruction

	case loong64asm.JIRL:
		asmInst.Kind = RetInstruction

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

func resolveCallArgLOONG64(inst *loong64asm.Inst, instAddr uint64, currentGoroutine bool,
	regs *op.DwarfRegisters, mem MemoryReadWriter, bininfo *BinaryInfo) *Location {
	var pc uint64

	switch inst.Op {
	// Format : INST offs
	case loong64asm.B,
	     loong64asm.BL:

		switch arg := inst.Args[0].(type) {
		case loong64asm.Imm32:
			if arg.Imm < 0 {
				pc = instAddr - uint64(arg.Imm*(-1))
			} else {
				pc = instAddr + uint64(arg.Imm)
			}
		default:
			return nil
		}

	//Format: inst rj,rd,offs16
	case loong64asm.BEQ,
	     loong64asm.BNE,
	     loong64asm.BLT,
	     loong64asm.BGE,
	     loong64asm.BLTU,
	     loong64asm.BGEU,
	     loong64asm.JIRL:

		switch arg := inst.Args[2].(type) {
		case loong64asm.Imm32:
			if arg.Imm < 0 {
				pc = instAddr - uint64(arg.Imm*(-1))
			} else {
				pc = instAddr + uint64(arg.Imm)
			}
		default:
			return nil
		}

	//Format: inst rj,offs21
	case loong64asm.BEQZ,
	     loong64asm.BNEZ,
	     loong64asm.BCEQZ,
	     loong64asm.BCNEZ:

		if (!currentGoroutine) || (regs == nil) {
			return nil
		}

		switch arg := inst.Args[1].(type) {
		case loong64asm.Imm32:
			if arg.Imm < 0 {
				pc = instAddr - uint64(arg.Imm*(-1))
			} else {
				pc = instAddr + uint64(arg.Imm)
			}
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

// Possible stacksplit prologues are inserted by stacksplit in
// $GOROOT/src/cmd/internal/obj/loong64/obj0.go.
var prologuesLOONG64 []opcodeSeq

func init() {
	var tinyStacksplit = opcodeSeq{
		uint64(loong64asm.SLTU),
		uint64(loong64asm.JIRL),
	}

	var smallStacksplit = opcodeSeq{
		uint64(loong64asm.ADD_W),
		uint64(loong64asm.SLTU),
		uint64(loong64asm.JIRL),
	}

	var bigStacksplit = opcodeSeq{
		uint64(loong64asm.OR),
		uint64(loong64asm.BEQ),
		uint64(loong64asm.ADD_W),
		uint64(loong64asm.SUB_W),
		uint64(loong64asm.SLTU),
		uint64(loong64asm.JIRL),
	}

	var unixGetG = opcodeSeq{uint64(loong64asm.LD_W)}

	prologuesLOONG64 = make([]opcodeSeq, 0, 3)
	for _, getG := range []opcodeSeq{unixGetG} {
		for _, stacksplit := range []opcodeSeq{tinyStacksplit, smallStacksplit, bigStacksplit} {
			prologue := make(opcodeSeq, 0, len(getG)+len(stacksplit))
			prologue = append(prologue, getG...)
			prologue = append(prologue, stacksplit...)
			prologuesLOONG64 = append(prologuesLOONG64, prologue)
		}
	}
}

type loong64ArchInst loong64asm.Inst

func (inst *loong64ArchInst) Text(flavour AssemblyFlavour, pc uint64,
	symLookup func(uint64) (string, uint64)) string {

	if inst == nil {
		return "?"
	}

	var text string

	switch flavour {
	case GoFlavour:
		// Unsupport Goflavour: GoSyntax
		text = "??"

	default:
		text = loong64asm.GNUSyntax(loong64asm.Inst(*inst))
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
