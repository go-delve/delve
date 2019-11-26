// TODO: disassembler support should be compiled in unconditionally,
// instead of being decided by the build-target architecture, and be
// part of the Arch object instead.

package proc

import (
	"encoding/binary"

	"golang.org/x/arch/x86/x86asm"
)

var maxInstructionLength uint64 = 15

type archInst x86asm.Inst

func asmDecode(mem []byte, pc uint64) (*archInst, error) {
	inst, err := x86asm.Decode(mem, 64)
	if err != nil {
		return nil, err
	}
	patchPCRel(pc, &inst)
	r := archInst(inst)
	return &r, nil
}

func (inst *archInst) Size() int {
	return inst.Len
}

// converts PC relative arguments to absolute addresses
func patchPCRel(pc uint64, inst *x86asm.Inst) {
	for i := range inst.Args {
		rel, isrel := inst.Args[i].(x86asm.Rel)
		if isrel {
			inst.Args[i] = x86asm.Imm(int64(pc) + int64(rel) + int64(inst.Len))
		}
	}
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
		text = x86asm.GNUSyntax(x86asm.Inst(*inst.Inst), inst.Loc.PC, bi.symLookup)
	case GoFlavour:
		text = x86asm.GoSyntax(x86asm.Inst(*inst.Inst), inst.Loc.PC, bi.symLookup)
	case IntelFlavour:
		fallthrough
	default:
		text = x86asm.IntelSyntax(x86asm.Inst(*inst.Inst), inst.Loc.PC, bi.symLookup)
	}

	return text
}

// IsCall returns true if the instruction is a CALL or LCALL instruction.
func (inst *AsmInstruction) IsCall() bool {
	if inst.Inst == nil {
		return false
	}
	return inst.Inst.Op == x86asm.CALL || inst.Inst.Op == x86asm.LCALL
}

// IsRet returns true if the instruction is a RET or LRET instruction.
func (inst *AsmInstruction) IsRet() bool {
	if inst.Inst == nil {
		return false
	}
	return inst.Inst.Op == x86asm.RET || inst.Inst.Op == x86asm.LRET
}

func resolveCallArg(inst *archInst, instAddr uint64, currentGoroutine bool, regs Registers, mem MemoryReadWriter, bininfo *BinaryInfo) *Location {
	if inst.Op != x86asm.CALL && inst.Op != x86asm.LCALL {
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
		pc, err = regs.Get(int(arg))
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
		base, err1 := regs.Get(int(arg.Base))
		index, err2 := regs.Get(int(arg.Index))
		if err1 != nil || err2 != nil {
			return nil
		}
		addr := uintptr(int64(base) + int64(index*uint64(arg.Scale)) + arg.Disp)
		//TODO: should this always be 64 bits instead of inst.MemBytes?
		pcbytes := make([]byte, inst.MemBytes)
		_, err := mem.ReadMemory(pcbytes, addr)
		if err != nil {
			return nil
		}
		pc = binary.LittleEndian.Uint64(pcbytes)
	default:
		return nil
	}

	file, line, fn := bininfo.PCToLine(pc)
	if fn == nil {
		return &Location{PC: pc}
	}
	return &Location{PC: pc, File: file, Line: line, Fn: fn}
}

type instrseq []x86asm.Op

// Possible stacksplit prologues are inserted by stacksplit in
// $GOROOT/src/cmd/internal/obj/x86/obj6.go.
// The stacksplit prologue will always begin with loading curg in CX, this
// instruction is added by load_g_cx in the same file and is either 1 or 2
// MOVs.
var prologues []instrseq

func init() {
	var tinyStacksplit = instrseq{x86asm.CMP, x86asm.JBE}
	var smallStacksplit = instrseq{x86asm.LEA, x86asm.CMP, x86asm.JBE}
	var bigStacksplit = instrseq{x86asm.MOV, x86asm.CMP, x86asm.JE, x86asm.LEA, x86asm.SUB, x86asm.CMP, x86asm.JBE}
	var unixGetG = instrseq{x86asm.MOV}
	var windowsGetG = instrseq{x86asm.MOV, x86asm.MOV}

	prologues = make([]instrseq, 0, 2*3)
	for _, getG := range []instrseq{unixGetG, windowsGetG} {
		for _, stacksplit := range []instrseq{tinyStacksplit, smallStacksplit, bigStacksplit} {
			prologue := make(instrseq, 0, len(getG)+len(stacksplit))
			prologue = append(prologue, getG...)
			prologue = append(prologue, stacksplit...)
			prologues = append(prologues, prologue)
		}
	}
}
