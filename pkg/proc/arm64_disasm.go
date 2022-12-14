// TODO: disassembler support should be compiled in unconditionally,
// instead of being decided by the build-target architecture, and be
// part of the Arch object instead.

package proc

import (
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"

	"golang.org/x/arch/arm64/arm64asm"
)

func arm64AsmDecode(asmInst *AsmInstruction, mem []byte, regs *op.DwarfRegisters, memrw MemoryReadWriter, bi *BinaryInfo) error {
	asmInst.Size = 4
	asmInst.Bytes = mem[:asmInst.Size]

	inst, err := arm64asm.Decode(mem)
	if err != nil {
		asmInst.Inst = (*arm64ArchInst)(nil)
		return err
	}

	asmInst.Inst = (*arm64ArchInst)(&inst)
	asmInst.Kind = OtherInstruction

	switch inst.Op {
	case arm64asm.BL, arm64asm.BLR:
		asmInst.Kind = CallInstruction
	case arm64asm.RET, arm64asm.ERET:
		asmInst.Kind = RetInstruction
	case arm64asm.B, arm64asm.BR:
		asmInst.Kind = JmpInstruction
	case arm64asm.BRK:
		asmInst.Kind = HardBreakInstruction
	}

	asmInst.DestLoc = resolveCallArgARM64(&inst, asmInst.Loc.PC, asmInst.AtPC, regs, memrw, bi)

	return nil
}

func resolveCallArgARM64(inst *arm64asm.Inst, instAddr uint64, currentGoroutine bool, regs *op.DwarfRegisters, mem MemoryReadWriter, bininfo *BinaryInfo) *Location {
	switch inst.Op {
	case arm64asm.BL, arm64asm.BLR, arm64asm.B, arm64asm.BR:
		// ok
	default:
		return nil
	}

	var pc uint64
	var err error

	switch arg := inst.Args[0].(type) {
	case arm64asm.Imm:
		pc = uint64(arg.Imm)
	case arm64asm.Reg:
		if !currentGoroutine || regs == nil {
			return nil
		}
		pc, err = bininfo.Arch.getAsmRegister(regs, int(arg))
		if err != nil {
			return nil
		}
	case arm64asm.PCRel:
		pc = uint64(instAddr) + uint64(arg)
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
// $GOROOT/src/cmd/internal/obj/arm64/obj7.go.
var prologuesARM64 []opcodeSeq

func init() {
	var tinyStacksplit = opcodeSeq{uint64(arm64asm.MOV), uint64(arm64asm.CMP), uint64(arm64asm.B)}
	var smallStacksplit = opcodeSeq{uint64(arm64asm.SUB), uint64(arm64asm.CMP), uint64(arm64asm.B)}
	var bigStacksplit = opcodeSeq{uint64(arm64asm.CMP), uint64(arm64asm.B), uint64(arm64asm.ADD), uint64(arm64asm.SUB), uint64(arm64asm.MOV), uint64(arm64asm.CMP), uint64(arm64asm.B)}
	var unixGetG = opcodeSeq{uint64(arm64asm.LDR)}

	prologuesARM64 = make([]opcodeSeq, 0, 3)
	for _, getG := range []opcodeSeq{unixGetG} {
		for _, stacksplit := range []opcodeSeq{tinyStacksplit, smallStacksplit, bigStacksplit} {
			prologue := make(opcodeSeq, 0, len(getG)+len(stacksplit))
			prologue = append(prologue, getG...)
			prologue = append(prologue, stacksplit...)
			prologuesARM64 = append(prologuesARM64, prologue)
		}
	}
}

type arm64ArchInst arm64asm.Inst

func (inst *arm64ArchInst) Text(flavour AssemblyFlavour, pc uint64, symLookup func(uint64) (string, uint64)) string {
	if inst == nil {
		return "?"
	}

	var text string

	switch flavour {
	case GNUFlavour:
		text = arm64asm.GNUSyntax(arm64asm.Inst(*inst))
	default:
		text = arm64asm.GoSyntax(arm64asm.Inst(*inst), pc, symLookup, nil)
	}

	return text
}

func (inst *arm64ArchInst) OpcodeEquals(op uint64) bool {
	if inst == nil {
		return false
	}
	return uint64(inst.Op) == op
}

var arm64AsmRegisters = func() map[int]asmRegister {
	r := make(map[int]asmRegister)
	for i := arm64asm.X0; i <= arm64asm.X30; i++ {
		r[int(i)] = asmRegister{regnum.ARM64_X0 + uint64(i-arm64asm.X0), 0, 0}
	}
	return r
}()
