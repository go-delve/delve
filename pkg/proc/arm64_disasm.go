package proc

import (
	"encoding/binary"

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

// arm64LinkerTrampolineTarget recognizes the instruction sequences emitted by
// cmd/link/internal/arm64.gentramp and gentrampgot.
func arm64LinkerTrampolineTarget(name string, pc uint64, instructions []AsmInstruction) (addr uint64, indirect, ok bool) {
	if !linkerTrampolineName.MatchString(name) {
		return 0, false, false
	}

	if len(instructions) != 3 {
		return 0, false, false
	}
	adrp, adrpok := instructions[0].Inst.(*arm64ArchInst)
	load, loadok := instructions[1].Inst.(*arm64ArchInst)
	br, brok := instructions[2].Inst.(*arm64ArchInst)
	if !adrpok || !loadok || !brok || adrp == nil || load == nil || br == nil {
		return 0, false, false
	}

	adrpdst, adrpDstOK := adrp.Args[0].(arm64asm.Reg)
	adrpoff, adrpOffOK := adrp.Args[1].(arm64asm.PCRel)
	brreg, brRegOK := br.Args[0].(arm64asm.Reg)
	if adrp.Op != arm64asm.ADRP || !adrpDstOK || adrpdst != arm64asm.X16 || !adrpOffOK ||
		br.Op != arm64asm.BR || !brRegOK || brreg != arm64asm.X16 {
		return 0, false, false
	}

	// ADRP forms its result by clearing the low 12 bits of the instruction
	// address (aligning it to a 4 KiB page) and adding its signed immediate.
	page := pc &^ 0xfff
	if adrpoff < 0 {
		offset := uint64(-adrpoff)
		if page < offset {
			return 0, false, false
		}
		page -= offset
	} else {
		offset := uint64(adrpoff)
		if page > ^uint64(0)-offset {
			return 0, false, false
		}
		page += offset
	}

	var offset uint64
	switch load.Op {
	case arm64asm.ADD:
		dst, dstok := load.Args[0].(arm64asm.RegSP)
		src, srcok := load.Args[1].(arm64asm.RegSP)
		_, immok := load.Args[2].(arm64asm.ImmShift)
		if !dstok || arm64asm.Reg(dst) != arm64asm.X16 ||
			!srcok || arm64asm.Reg(src) != arm64asm.X16 || !immok {
			return 0, false, false
		}
		// ADD (immediate) stores imm12 in bits 10 through 21. Bit 22 selects
		// whether imm12 is used directly or shifted left by 12 bits.
		imm12 := uint64(load.Enc>>10) & 0xfff
		shift := uint(load.Enc>>22) & 0x1
		offset = imm12 << (12 * shift)
	case arm64asm.LDR:
		dst, dstok := load.Args[0].(arm64asm.Reg)
		mem, memok := load.Args[1].(arm64asm.MemImmediate)
		if !dstok || dst != arm64asm.X16 || !memok ||
			arm64asm.Reg(mem.Base) != arm64asm.X16 || mem.Mode != arm64asm.AddrOffset {
			return 0, false, false
		}
		// LDR (unsigned immediate) also stores imm12 in bits 10 through 21.
		// The 64-bit form scales that value by the eight-byte operand size.
		imm12 := uint64(load.Enc>>10) & 0xfff
		offset = imm12 << 3
		indirect = true
	default:
		return 0, false, false
	}
	if page > ^uint64(0)-offset {
		return 0, false, false
	}
	return page + offset, indirect, true
}

func resolveARM64LinkerTrampoline(p Process, fn *Function, pc uint64) (uint64, bool) {
	name := ""
	if fn != nil {
		name = fn.Name
	} else if sym := p.BinInfo().SymNames[pc]; sym != nil {
		name = sym.Name
	}
	if fn != nil && fn.End-fn.Entry < 12 {
		return 0, false
	}
	text, err := Disassemble(p.Memory(), nil, p.Breakpoints(), p.BinInfo(), pc, pc+12)
	if err != nil || len(text) != 3 {
		return 0, false
	}
	addr, indirect, ok := arm64LinkerTrampolineTarget(name, pc, text)
	if !ok || !indirect {
		return addr, ok
	}

	target := make([]byte, p.BinInfo().Arch.PtrSize())
	if _, err := p.Memory().ReadMemory(target, addr); err != nil {
		return 0, false
	}
	return binary.LittleEndian.Uint64(target), true
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
