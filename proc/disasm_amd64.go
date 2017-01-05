package proc

import (
	"debug/gosym"
	"encoding/binary"
	"rsc.io/x86/x86asm"
)

var maxInstructionLength uint64 = 15

type ArchInst x86asm.Inst

func asmDecode(mem []byte, pc uint64) (*ArchInst, error) {
	inst, err := x86asm.Decode(mem, 64)
	if err != nil {
		return nil, err
	}
	patchPCRel(pc, &inst)
	r := ArchInst(inst)
	return &r, nil
}

func (inst *ArchInst) Size() int {
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
	return
}

func (inst *AsmInstruction) Text(flavour AssemblyFlavour) string {
	if inst.Inst == nil {
		return "?"
	}

	var text string

	switch flavour {
	case GNUFlavour:
		text = x86asm.GNUSyntax(x86asm.Inst(*inst.Inst))
	case IntelFlavour:
		fallthrough
	default:
		text = x86asm.IntelSyntax(x86asm.Inst(*inst.Inst))
	}

	if inst.IsCall() && inst.DestLoc != nil && inst.DestLoc.Fn != nil {
		text += " " + inst.DestLoc.Fn.Name
	}

	return text
}

func (inst *AsmInstruction) IsCall() bool {
	return inst.Inst.Op == x86asm.CALL || inst.Inst.Op == x86asm.LCALL
}

func (thread *Thread) resolveCallArg(inst *ArchInst, currentGoroutine bool, regs Registers) *Location {
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
		regs, err := thread.Registers(false)
		if err != nil {
			return nil
		}
		base, err1 := regs.Get(int(arg.Base))
		index, err2 := regs.Get(int(arg.Index))
		if err1 != nil || err2 != nil {
			return nil
		}
		addr := uintptr(int64(base) + int64(index*uint64(arg.Scale)) + arg.Disp)
		//TODO: should this always be 64 bits instead of inst.MemBytes?
		pcbytes, err := thread.readMemory(addr, inst.MemBytes)
		if err != nil {
			return nil
		}
		pc = binary.LittleEndian.Uint64(pcbytes)
	default:
		return nil
	}

	file, line, fn := thread.dbp.PCToLine(pc)
	if fn == nil {
		return nil
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

// FirstPCAfterPrologue returns the address of the first instruction after the prologue for function fn
// If sameline is set FirstPCAfterPrologue will always return an address associated with the same line as fn.Entry
func (dbp *Process) FirstPCAfterPrologue(fn *gosym.Func, sameline bool) (uint64, error) {
	text, err := dbp.CurrentThread.Disassemble(fn.Entry, fn.End, false)
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
