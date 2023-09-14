package proc

import (
	"encoding/binary"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"golang.org/x/arch/ppc64/ppc64asm"
)

// Possible stacksplit prologues are inserted by stacksplit in
// $GOROOT/src/cmd/internal/obj/ppc64/obj9.go.
var prologuesPPC64LE []opcodeSeq

func init() {
	// Note: these will be the gnu opcodes and not the Go opcodes. Verify the sequences are as expected.
	var tinyStacksplit = opcodeSeq{uint64(ppc64asm.ADDI), uint64(ppc64asm.CMPLD), uint64(ppc64asm.BC)}
	var smallStacksplit = opcodeSeq{uint64(ppc64asm.ADDI), uint64(ppc64asm.CMPLD), uint64(ppc64asm.BC)}
	var bigStacksplit = opcodeSeq{uint64(ppc64asm.ADDI), uint64(ppc64asm.CMPLD), uint64(ppc64asm.BC), uint64(ppc64asm.STD), uint64(ppc64asm.STD), uint64(ppc64asm.MFSPR)}
	var adjustTOCPrologueOnPIE = opcodeSeq{uint64(ppc64asm.ADDIS), uint64(ppc64asm.ADDI)}

	var unixGetG = opcodeSeq{uint64(ppc64asm.LD)}
	var prologue opcodeSeq
	prologuesPPC64LE = make([]opcodeSeq, 0, 3)
	prologue = make(opcodeSeq, 0, 1)
	for _, getG := range []opcodeSeq{unixGetG} {
		for _, stacksplit := range []opcodeSeq{tinyStacksplit, smallStacksplit, bigStacksplit} {
			prologue = append(prologue, getG...)
			prologue = append(prologue, stacksplit...)
			prologuesPPC64LE = append(prologuesPPC64LE, prologue)
		}
	}
	// On PIE mode special prologue is generated two instructions before the function entry point that correlates to call target
	// address, Teach delve to recognize this sequence to appropriately adjust PC address so that the breakpoint is actually hit
	// while doing step
	TOCprologue := make(opcodeSeq, 0, len(adjustTOCPrologueOnPIE))
	TOCprologue = append(TOCprologue, adjustTOCPrologueOnPIE...)
	prologuesPPC64LE = append(prologuesPPC64LE, TOCprologue)
}

func ppc64leAsmDecode(asmInst *AsmInstruction, mem []byte, regs *op.DwarfRegisters, memrw MemoryReadWriter, bi *BinaryInfo) error {
	asmInst.Size = 4
	asmInst.Bytes = mem[:asmInst.Size]

	inst, err := ppc64asm.Decode(mem, binary.LittleEndian)
	if err != nil {
		asmInst.Inst = (*ppc64ArchInst)(nil)
		return err
	}
	asmInst.Inst = (*ppc64ArchInst)(&inst)
	asmInst.Kind = OtherInstruction

	switch inst.Op {
	case ppc64asm.BL, ppc64asm.BLA, ppc64asm.BCL, ppc64asm.BCLA, ppc64asm.BCLRL, ppc64asm.BCCTRL, ppc64asm.BCTARL:
		// Pages 38-40 Book I v3.0
		asmInst.Kind = CallInstruction
	case ppc64asm.RFEBB, ppc64asm.RFID, ppc64asm.HRFID, ppc64asm.RFI, ppc64asm.RFCI, ppc64asm.RFDI, ppc64asm.RFMCI, ppc64asm.RFGI, ppc64asm.BCLR:
		asmInst.Kind = RetInstruction
	case ppc64asm.B, ppc64asm.BA, ppc64asm.BC, ppc64asm.BCA, ppc64asm.BCCTR, ppc64asm.BCTAR:
		// Pages 38-40 Book I v3.0
		asmInst.Kind = JmpInstruction
	case ppc64asm.TD, ppc64asm.TDI, ppc64asm.TW, ppc64asm.TWI:
		asmInst.Kind = HardBreakInstruction
	}

	asmInst.DestLoc = resolveCallArgPPC64LE(&inst, asmInst.Loc.PC, asmInst.AtPC, regs, memrw, bi)
	return nil
}

func resolveCallArgPPC64LE(inst *ppc64asm.Inst, instAddr uint64, currentGoroutine bool, regs *op.DwarfRegisters, mem MemoryReadWriter, bininfo *BinaryInfo) *Location {
	switch inst.Op {
	case ppc64asm.BCLRL, ppc64asm.BCLR:
		if regs != nil && regs.PC() == instAddr {
			pc := regs.Reg(bininfo.Arch.LRRegNum).Uint64Val
			file, line, fn := bininfo.PCToLine(pc)
			if fn == nil {
				return &Location{PC: pc}
			}
			return &Location{PC: pc, File: file, Line: line, Fn: fn}
		}
		return nil
	case ppc64asm.B, ppc64asm.BL, ppc64asm.BLA, ppc64asm.BCL, ppc64asm.BCLA, ppc64asm.BCCTRL, ppc64asm.BCTARL:
		// ok
	default:
		return nil
	}

	var pc uint64
	var err error

	switch arg := inst.Args[0].(type) {
	case ppc64asm.Imm:
		pc = uint64(arg)
	case ppc64asm.Reg:
		if !currentGoroutine || regs == nil {
			return nil
		}
		pc, err = bininfo.Arch.getAsmRegister(regs, int(arg))
		if err != nil {
			return nil
		}
	case ppc64asm.PCRel:
		pc = instAddr + uint64(arg)
	default:
		return nil
	}

	file, line, fn := bininfo.PCToLine(pc)
	if fn == nil {
		return &Location{PC: pc}
	}
	return &Location{PC: pc, File: file, Line: line, Fn: fn}
}

type ppc64ArchInst ppc64asm.Inst

func (inst *ppc64ArchInst) Text(flavour AssemblyFlavour, pc uint64, symLookup func(uint64) (string, uint64)) string {
	if inst == nil {
		return "?"
	}

	var text string

	switch flavour {
	case GNUFlavour:
		text = ppc64asm.GNUSyntax(ppc64asm.Inst(*inst), pc)
	default:
		text = ppc64asm.GoSyntax(ppc64asm.Inst(*inst), pc, symLookup)
	}

	return text
}

func (inst *ppc64ArchInst) OpcodeEquals(op uint64) bool {
	if inst == nil {
		return false
	}
	return uint64(inst.Op) == op
}

var ppc64leAsmRegisters = func() map[int]asmRegister {
	r := make(map[int]asmRegister)

	// General Purpose Registers: from R0 to R31
	for i := ppc64asm.R0; i <= ppc64asm.R31; i++ {
		r[int(i)] = asmRegister{regnum.PPC64LE_R0 + uint64(i-ppc64asm.R0), 0, 0}
	}

	// Floating point registers: from F0 to F31
	for i := ppc64asm.F0; i <= ppc64asm.F31; i++ {
		r[int(i)] = asmRegister{regnum.PPC64LE_F0 + uint64(i-ppc64asm.F0), 0, 0}
	}

	// Vector (Altivec/VMX) registers: from V0 to V31
	for i := ppc64asm.V0; i <= ppc64asm.V31; i++ {
		r[int(i)] = asmRegister{regnum.PPC64LE_V0 + uint64(i-ppc64asm.V0), 0, 0}
	}

	// Vector Scalar (VSX) registers: from VS0 to VS63
	for i := ppc64asm.VS0; i <= ppc64asm.VS63; i++ {
		r[int(i)] = asmRegister{regnum.PPC64LE_VS0 + uint64(i-ppc64asm.VS0), 0, 0}
	}

	// Condition Registers: from CR0 to CR7
	for i := ppc64asm.CR0; i <= ppc64asm.CR7; i++ {
		r[int(i)] = asmRegister{regnum.PPC64LE_CR0 + uint64(i-ppc64asm.CR0), 0, 0}
	}
	return r
}()
