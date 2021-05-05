// TODO: disassembler support should be compiled in unconditionally,
// instead of being decided by the build-target architecture, and be
// part of the Arch object instead.

package proc

import (
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"

	"golang.org/x/arch/x86/x86asm"
)

func i386AsmDecode(asmInst *AsmInstruction, mem []byte, regs *op.DwarfRegisters, memrw MemoryReadWriter, bi *BinaryInfo) error {
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

var i386AsmRegisters = map[int]asmRegister{
	// 8-bit
	int(x86asm.AL):  asmRegister{regnum.I386_Eax, 0, mask8},
	int(x86asm.CL):  asmRegister{regnum.I386_Ecx, 0, mask8},
	int(x86asm.DL):  asmRegister{regnum.I386_Edx, 0, mask8},
	int(x86asm.BL):  asmRegister{regnum.I386_Ebx, 0, mask8},
	int(x86asm.AH):  asmRegister{regnum.I386_Eax, 8, mask8},
	int(x86asm.CH):  asmRegister{regnum.I386_Ecx, 8, mask8},
	int(x86asm.DH):  asmRegister{regnum.I386_Edx, 8, mask8},
	int(x86asm.BH):  asmRegister{regnum.I386_Ebx, 8, mask8},
	int(x86asm.SPB): asmRegister{regnum.I386_Esp, 0, mask8},
	int(x86asm.BPB): asmRegister{regnum.I386_Ebp, 0, mask8},
	int(x86asm.SIB): asmRegister{regnum.I386_Esi, 0, mask8},
	int(x86asm.DIB): asmRegister{regnum.I386_Edi, 0, mask8},

	// 16-bit
	int(x86asm.AX): asmRegister{regnum.I386_Eax, 0, mask16},
	int(x86asm.CX): asmRegister{regnum.I386_Ecx, 0, mask16},
	int(x86asm.DX): asmRegister{regnum.I386_Edx, 0, mask16},
	int(x86asm.BX): asmRegister{regnum.I386_Ebx, 0, mask16},
	int(x86asm.SP): asmRegister{regnum.I386_Esp, 0, mask16},
	int(x86asm.BP): asmRegister{regnum.I386_Ebp, 0, mask16},
	int(x86asm.SI): asmRegister{regnum.I386_Esi, 0, mask16},
	int(x86asm.DI): asmRegister{regnum.I386_Edi, 0, mask16},

	// 32-bit
	int(x86asm.EAX): asmRegister{regnum.I386_Eax, 0, mask32},
	int(x86asm.ECX): asmRegister{regnum.I386_Ecx, 0, mask32},
	int(x86asm.EDX): asmRegister{regnum.I386_Edx, 0, mask32},
	int(x86asm.EBX): asmRegister{regnum.I386_Ebx, 0, mask32},
	int(x86asm.ESP): asmRegister{regnum.I386_Esp, 0, mask32},
	int(x86asm.EBP): asmRegister{regnum.I386_Ebp, 0, mask32},
	int(x86asm.ESI): asmRegister{regnum.I386_Esi, 0, mask32},
	int(x86asm.EDI): asmRegister{regnum.I386_Edi, 0, mask32},
}
