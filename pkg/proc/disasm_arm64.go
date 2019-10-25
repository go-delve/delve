// TODO: disassembler support should be compiled in unconditionally,
// instead of being decided by the build-target architecture, and be
// part of the Arch object instead.

package proc

import (
	//"encoding/binary"

	"golang.org/x/arch/arm64/arm64asm"
)

var maxInstructionLength uint64 = 4

type archInst arm64asm.Inst

func asmDecode(mem []byte, pc uint64) (*archInst, error) {
	inst, err := arm64asm.Decode(mem)
	if err != nil {
		return nil, err
	}

	r := archInst(inst)
	return &r, nil
}

func (inst *archInst) Size() int {
	return 4
}

// Text will return the assembly instructions in human readable format according to
// the flavour specified.
func (inst *AsmInstruction) Text(flavour AssemblyFlavour, bi *BinaryInfo) string {
	if inst.Inst == nil {
		return "?"
	}

	var text string

	switch flavour {
	case GNUFlavour:
		text = arm64asm.GNUSyntax(arm64asm.Inst(*inst.Inst))
	default:
		text = arm64asm.GoSyntax(arm64asm.Inst(*inst.Inst), inst.Loc.PC, bi.symLookup, nil)
	}

	return text
}

// IsCall returns true if the instruction is a BL or BLR instruction.
func (inst *AsmInstruction) IsCall() bool {
	if inst.Inst == nil {
		return false
	}
	return inst.Inst.Op == arm64asm.BL || inst.Inst.Op == arm64asm.BLR
}

// IsRet returns true if the instruction is a RET or ERET instruction.
func (inst *AsmInstruction) IsRet() bool {
	if inst.Inst == nil {
		return false
	}
	return inst.Inst.Op == arm64asm.RET || inst.Inst.Op == arm64asm.ERET
}

func resolveCallArg(inst *archInst, instAddr uint64, currentGoroutine bool, regs Registers, mem MemoryReadWriter, bininfo *BinaryInfo) *Location {
	if inst.Op != arm64asm.BL && inst.Op != arm64asm.BLR {
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
		pc, err = regs.Get(int(arg))
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

type instrseq []arm64asm.Op

// Possible stacksplit prologues are inserted by stacksplit in
// $GOROOT/src/cmd/internal/obj/arm64/obj7.go.
// The stacksplit prologue will always begin with loading curg in CX, this
// instruction is added by load_g_cx in the same file and is either 1 or 2
// MOVs.
var prologues []instrseq

func init() {
	var tinyStacksplit = instrseq{arm64asm.MOV, arm64asm.CMP, arm64asm.B}
	var smallStacksplit = instrseq{arm64asm.SUB, arm64asm.CMP, arm64asm.B}
	var bigStacksplit = instrseq{arm64asm.CMP, arm64asm.B, arm64asm.ADD, arm64asm.SUB, arm64asm.MOV, arm64asm.CMP, arm64asm.B}
	var unixGetG = instrseq{arm64asm.MOV}

	prologues = make([]instrseq, 0, 3)
	for _, getG := range []instrseq{unixGetG} {
		for _, stacksplit := range []instrseq{tinyStacksplit, smallStacksplit, bigStacksplit} {
			prologue := make(instrseq, 0, len(getG)+len(stacksplit))
			prologue = append(prologue, getG...)
			prologue = append(prologue, stacksplit...)
			prologues = append(prologues, prologue)
		}
	}
}

// firstPCAfterPrologueDisassembly returns the address of the first
// instruction after the prologue for function fn by disassembling fn and
// matching the instructions against known split-stack prologue patterns.
// If sameline is set firstPCAfterPrologueDisassembly will always return an
// address associated with the same line as fn.Entry
func firstPCAfterPrologueDisassembly(p Process, fn *Function, sameline bool) (uint64, error) {
	var mem MemoryReadWriter = p.CurrentThread()
	breakpoints := p.Breakpoints()
	bi := p.BinInfo()
	text, err := disassemble(mem, nil, breakpoints, bi, fn.Entry, fn.End, false)
	if err != nil {
		return fn.Entry, err
	}

	if len(text) <= 0 {
		return fn.Entry, nil
	}

	for _, prologue := range prologues {
		if len(prologue) >= len(text) {
			continue
		}
		if checkPrologue(text, prologue) {
			r := &text[len(prologue)]
			if sameline {
				if r.Loc.Line != text[0].Loc.Line {
					return fn.Entry, nil
				}
			}
			return r.Loc.PC, nil
		}
	}

	return fn.Entry, nil
}

func checkPrologue(s []AsmInstruction, prologuePattern instrseq) bool {
	line := s[0].Loc.Line
	for i, op := range prologuePattern {
		if s[i].Inst.Op != op || s[i].Loc.Line != line {
			return false
		}
	}
	return true
}
