// TODO: disassembler support should be compiled in unconditionally,
// instead of being decided by the build-target architecture, and be
// part of the Arch object instead.

package proc

import (
	"golang.org/x/arch/x86/x86asm"
)

func i386AsmDecode(asmInst *AsmInstruction, mem []byte, regs Registers, memrw MemoryReadWriter, bi *BinaryInfo) error {
	return x86AsmDecode(asmInst, mem, regs, memrw, bi, 32)
}

// Possible stacksplit prologues are inserted by stacksplit in
// $GOROOT/src/cmd/internal/obj/x86/obj6.go.
// If 386 on linux when pie, the stacksplit prologue beigin with `call __x86.get_pc_thunk.` sometime.
var prologuesI386 []opcodeSeq

func init() {
	var i386GetPcIns = opcodeSeq{uint64(x86asm.CALL)}
	var tinyStacksplit = opcodeSeq{uint64(x86asm.CMP), uint64(x86asm.JBE)}
	var smallStacksplit = opcodeSeq{uint64(x86asm.LEA), uint64(x86asm.CMP), uint64(x86asm.JBE)}
	var bigStacksplit = opcodeSeq{uint64(x86asm.MOV), uint64(x86asm.CMP), uint64(x86asm.JE), uint64(x86asm.LEA), uint64(x86asm.SUB), uint64(x86asm.CMP), uint64(x86asm.JBE)}
	var unixGetG = opcodeSeq{uint64(x86asm.MOV), uint64(x86asm.MOV)}

	prologuesI386 = make([]opcodeSeq, 0, 2*3)
	for _, getPcIns := range []opcodeSeq{{}, i386GetPcIns} {
		for _, getG := range []opcodeSeq{unixGetG} { // TODO(chainhelen), need to support other OSs.
			for _, stacksplit := range []opcodeSeq{tinyStacksplit, smallStacksplit, bigStacksplit} {
				prologue := make(opcodeSeq, 0, len(getPcIns)+len(getG)+len(stacksplit))
				prologue = append(prologue, getPcIns...)
				prologue = append(prologue, getG...)
				prologue = append(prologue, stacksplit...)
				prologuesI386 = append(prologuesI386, prologue)
			}
		}
	}
}
