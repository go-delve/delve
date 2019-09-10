package proc

import (
	"fmt"
	"io"

	"golang.org/x/arch/arm64/arm64asm"
)

var maxInstructionLength uint64 = 4

type archInst arm64asm.Inst

//type archInst x86asm.Inst

func asmDecode(mem []byte, pc uint64) (*archInst, error) {
	inst, err := arm64asm.Decode(mem)
	if err != nil {
		return nil, err
	}
	patchPCRel(pc, &inst)
	r := archInst(inst)
	return &r, nil
}

func (inst *archInst) Size() int {
	return 4
}

// converts PC relative arguments to absolute addresses
func patchPCRel(pc uint64, inst *arm64asm.Inst) {
	//var Imm64 arm64asm.Imm64

	for i := range inst.Args {
		rel, isrel := inst.Args[i].(arm64asm.PCRel)
		if isrel {

			inst.Args[i] = arm64asm.ArchArm64(uint64(pc) + uint64(rel) + uint64(inst.Len))

		}
	}
}

// Text will return the assembly instructions in human readable format according to
// the flavour specified.
var readerAt io.ReaderAt

func (inst *AsmInstruction) Text(flavour AssemblyFlavour, bi *BinaryInfo) string {
	if inst.Inst == nil {
		return "?"
	}
	var text string

	switch flavour {
	case GNUFlavour:
		text = arm64asm.GNUSyntax(arm64asm.Inst(*inst.Inst))
	case GoFlavour:
		text = arm64asm.GoSyntax(arm64asm.Inst(*inst.Inst), inst.Loc.PC, bi.symLookup, readerAt)
	case IntelFlavour:
		fallthrough
	default:
		text = arm64asm.GNUSyntax(arm64asm.Inst(*inst.Inst))
	}

	return text
}

// IsCall returns true if the instruction is a CALL or LCALL instruction.
func (inst *AsmInstruction) IsCall() bool {
	if inst.Inst == nil {
		return false
	}
	return inst.Inst.Op == arm64asm.BL || inst.Inst.Op == arm64asm.BLR
}

// IsRet returns true if the instruction is a RET or LRET instruction.
func (inst *AsmInstruction) IsRet() bool {
	if inst.Inst == nil {
		return false
	}
	return inst.Inst.Op == arm64asm.RET || inst.Inst.Op == arm64asm.ERET
}

func resolveCallArg(inst *archInst, currentGoroutine bool, regs Registers, mem MemoryReadWriter, bininfo *BinaryInfo) *Location {
	if inst.Op != arm64asm.BL /*&& inst.Op != arm64asm.B*/ {
	//if inst.Op != arm64asm.ADRP && inst.Op != arm64asm.LDUR {
		return nil
	}

	var pc uint64
	var err error
	switch arg := inst.Args[0].(type) {
	case arm64asm.ArchArm64:
		pc = uint64(arg)
	case arm64asm.Reg:
		if !currentGoroutine || regs == nil {
			return nil
		}
		pc, err = regs.Get(int(arg))
		if err != nil {
			return nil
		}

	case arm64asm.RegSP:
		fmt.Println("1")

	case arm64asm.ImmShift:
		fmt.Println("2")

	case arm64asm.PCRel:
		//pcbytes := make([]byte, inst.MemBytes)
		//pc = binary.LittleEndian.Uint64(pcbytes)
		fmt.Println("3")

	case arm64asm.MemImmediate:
		fmt.Println("4")

	case arm64asm.MemExtend:
		fmt.Println("5")

	case arm64asm.Imm:
		fmt.Println("6")

	case arm64asm.Imm_hint:
		fmt.Println("7")

	case arm64asm.Imm_clrex:
		fmt.Println("8")

	case arm64asm.Imm_dcps:
		fmt.Println("9")

	case arm64asm.Cond:
		fmt.Println("11")

		// Reg, RegSP, ImmShift, RegExtshiftAmount, PCRel, MemImmediate,
		// MemExtend, Imm, Imm64, Imm_hint, Imm_clrex, Imm_dcps, Cond,
		// Imm_c, Imm_option, Imm_prfop, Pstatefield, Systemreg, Imm_fp
		// RegisterWithArrangement, RegisterWithArrangementAndIndex.

	case arm64asm.Imm_c:
		fmt.Println("12")

	case arm64asm.Imm_option:
		fmt.Println("13")

	case arm64asm.Imm_prfop:
		fmt.Println("14")

	case arm64asm.Pstatefield:
		fmt.Println("15")

	case arm64asm.Systemreg:
		fmt.Println("16")

	case arm64asm.Imm_fp:
		fmt.Println("17")

	case arm64asm.RegisterWithArrangement:
		fmt.Println("18")

	case arm64asm.RegisterWithArrangementAndIndex:
		fmt.Println("19")

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
// $GOROOT/src/cmd/internal/obj/x86/obj6.go.
// The stacksplit prologue will always begin with loading curg in CX, thisi
// instruction is added by load_g_cx in the same file and is either 1 or 2
// MOVs.
var prologues []instrseq

func init() {
	var tinyStacksplit = instrseq{arm64asm.CMP, arm64asm.BSL} //Ok
	var smallStacksplit = instrseq{arm64asm.LDR, arm64asm.CMP, arm64asm.BSL}
	var bigStacksplit = instrseq{arm64asm.MOV, arm64asm.CMP, arm64asm.BL /*BNE*/, arm64asm.LDR, arm64asm.SUB, arm64asm.CMP, arm64asm.BSL}
	var unixGetG = instrseq{arm64asm.MOV}
	var windowsGetG = instrseq{arm64asm.MOV, arm64asm.MOV}

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
		if arm64asm.Op(s[i].Inst.Op) != op || s[i].Loc.Line != line {
			return false
		}
	}
	return true
}
