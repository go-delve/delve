// TODO: disassembler support should be compiled in unconditionally,
// instead of being decided by the build-target architecture, and be
// part of the Arch object instead.

package proc

import (
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"

	"golang.org/x/arch/x86/x86asm"
)

func amd64AsmDecode(asmInst *AsmInstruction, mem []byte, regs *op.DwarfRegisters, memrw MemoryReadWriter, bi *BinaryInfo) error {
	return x86AsmDecode(asmInst, mem, regs, memrw, bi, 64)
}

// Possible stacksplit prologues are inserted by stacksplit in
// $GOROOT/src/cmd/internal/obj/x86/obj6.go.
// The stacksplit prologue will always begin with loading curg in CX, this
// instruction is added by load_g_cx in the same file and is either 1 or 2
// MOVs.
var prologuesAMD64 []opcodeSeq

func init() {
	var tinyStacksplit = opcodeSeq{uint64(x86asm.CMP), uint64(x86asm.JBE)}
	var smallStacksplit = opcodeSeq{uint64(x86asm.LEA), uint64(x86asm.CMP), uint64(x86asm.JBE)}
	var bigStacksplit = opcodeSeq{uint64(x86asm.MOV), uint64(x86asm.CMP), uint64(x86asm.JE), uint64(x86asm.LEA), uint64(x86asm.SUB), uint64(x86asm.CMP), uint64(x86asm.JBE)}
	var unixGetG = opcodeSeq{uint64(x86asm.MOV)}
	var windowsGetG = opcodeSeq{uint64(x86asm.MOV), uint64(x86asm.MOV)}

	prologuesAMD64 = make([]opcodeSeq, 0, 2*3)
	for _, getG := range []opcodeSeq{unixGetG, windowsGetG} {
		for _, stacksplit := range []opcodeSeq{tinyStacksplit, smallStacksplit, bigStacksplit} {
			prologue := make(opcodeSeq, 0, len(getG)+len(stacksplit))
			prologue = append(prologue, getG...)
			prologue = append(prologue, stacksplit...)
			prologuesAMD64 = append(prologuesAMD64, prologue)
		}
	}
}

var amd64AsmRegisters = map[int]asmRegister{
	// 8-bit
	int(x86asm.AL):   asmRegister{regnum.AMD64_Rax, 0, mask8},
	int(x86asm.CL):   asmRegister{regnum.AMD64_Rcx, 0, mask8},
	int(x86asm.DL):   asmRegister{regnum.AMD64_Rdx, 0, mask8},
	int(x86asm.BL):   asmRegister{regnum.AMD64_Rbx, 0, mask8},
	int(x86asm.AH):   asmRegister{regnum.AMD64_Rax, 8, mask8},
	int(x86asm.CH):   asmRegister{regnum.AMD64_Rcx, 8, mask8},
	int(x86asm.DH):   asmRegister{regnum.AMD64_Rdx, 8, mask8},
	int(x86asm.BH):   asmRegister{regnum.AMD64_Rbx, 8, mask8},
	int(x86asm.SPB):  asmRegister{regnum.AMD64_Rsp, 0, mask8},
	int(x86asm.BPB):  asmRegister{regnum.AMD64_Rbp, 0, mask8},
	int(x86asm.SIB):  asmRegister{regnum.AMD64_Rsi, 0, mask8},
	int(x86asm.DIB):  asmRegister{regnum.AMD64_Rdi, 0, mask8},
	int(x86asm.R8B):  asmRegister{regnum.AMD64_R8, 0, mask8},
	int(x86asm.R9B):  asmRegister{regnum.AMD64_R9, 0, mask8},
	int(x86asm.R10B): asmRegister{regnum.AMD64_R10, 0, mask8},
	int(x86asm.R11B): asmRegister{regnum.AMD64_R11, 0, mask8},
	int(x86asm.R12B): asmRegister{regnum.AMD64_R12, 0, mask8},
	int(x86asm.R13B): asmRegister{regnum.AMD64_R13, 0, mask8},
	int(x86asm.R14B): asmRegister{regnum.AMD64_R14, 0, mask8},
	int(x86asm.R15B): asmRegister{regnum.AMD64_R15, 0, mask8},

	// 16-bit
	int(x86asm.AX):   asmRegister{regnum.AMD64_Rax, 0, mask16},
	int(x86asm.CX):   asmRegister{regnum.AMD64_Rcx, 0, mask16},
	int(x86asm.DX):   asmRegister{regnum.AMD64_Rdx, 0, mask16},
	int(x86asm.BX):   asmRegister{regnum.AMD64_Rbx, 0, mask16},
	int(x86asm.SP):   asmRegister{regnum.AMD64_Rsp, 0, mask16},
	int(x86asm.BP):   asmRegister{regnum.AMD64_Rbp, 0, mask16},
	int(x86asm.SI):   asmRegister{regnum.AMD64_Rsi, 0, mask16},
	int(x86asm.DI):   asmRegister{regnum.AMD64_Rdi, 0, mask16},
	int(x86asm.R8W):  asmRegister{regnum.AMD64_R8, 0, mask16},
	int(x86asm.R9W):  asmRegister{regnum.AMD64_R9, 0, mask16},
	int(x86asm.R10W): asmRegister{regnum.AMD64_R10, 0, mask16},
	int(x86asm.R11W): asmRegister{regnum.AMD64_R11, 0, mask16},
	int(x86asm.R12W): asmRegister{regnum.AMD64_R12, 0, mask16},
	int(x86asm.R13W): asmRegister{regnum.AMD64_R13, 0, mask16},
	int(x86asm.R14W): asmRegister{regnum.AMD64_R14, 0, mask16},
	int(x86asm.R15W): asmRegister{regnum.AMD64_R15, 0, mask16},

	// 32-bit
	int(x86asm.EAX):  asmRegister{regnum.AMD64_Rax, 0, mask32},
	int(x86asm.ECX):  asmRegister{regnum.AMD64_Rcx, 0, mask32},
	int(x86asm.EDX):  asmRegister{regnum.AMD64_Rdx, 0, mask32},
	int(x86asm.EBX):  asmRegister{regnum.AMD64_Rbx, 0, mask32},
	int(x86asm.ESP):  asmRegister{regnum.AMD64_Rsp, 0, mask32},
	int(x86asm.EBP):  asmRegister{regnum.AMD64_Rbp, 0, mask32},
	int(x86asm.ESI):  asmRegister{regnum.AMD64_Rsi, 0, mask32},
	int(x86asm.EDI):  asmRegister{regnum.AMD64_Rdi, 0, mask32},
	int(x86asm.R8L):  asmRegister{regnum.AMD64_R8, 0, mask32},
	int(x86asm.R9L):  asmRegister{regnum.AMD64_R9, 0, mask32},
	int(x86asm.R10L): asmRegister{regnum.AMD64_R10, 0, mask32},
	int(x86asm.R11L): asmRegister{regnum.AMD64_R11, 0, mask32},
	int(x86asm.R12L): asmRegister{regnum.AMD64_R12, 0, mask32},
	int(x86asm.R13L): asmRegister{regnum.AMD64_R13, 0, mask32},
	int(x86asm.R14L): asmRegister{regnum.AMD64_R14, 0, mask32},
	int(x86asm.R15L): asmRegister{regnum.AMD64_R15, 0, mask32},

	// 64-bit
	int(x86asm.RAX): asmRegister{regnum.AMD64_Rax, 0, 0},
	int(x86asm.RCX): asmRegister{regnum.AMD64_Rcx, 0, 0},
	int(x86asm.RDX): asmRegister{regnum.AMD64_Rdx, 0, 0},
	int(x86asm.RBX): asmRegister{regnum.AMD64_Rbx, 0, 0},
	int(x86asm.RSP): asmRegister{regnum.AMD64_Rsp, 0, 0},
	int(x86asm.RBP): asmRegister{regnum.AMD64_Rbp, 0, 0},
	int(x86asm.RSI): asmRegister{regnum.AMD64_Rsi, 0, 0},
	int(x86asm.RDI): asmRegister{regnum.AMD64_Rdi, 0, 0},
	int(x86asm.R8):  asmRegister{regnum.AMD64_R8, 0, 0},
	int(x86asm.R9):  asmRegister{regnum.AMD64_R9, 0, 0},
	int(x86asm.R10): asmRegister{regnum.AMD64_R10, 0, 0},
	int(x86asm.R11): asmRegister{regnum.AMD64_R11, 0, 0},
	int(x86asm.R12): asmRegister{regnum.AMD64_R12, 0, 0},
	int(x86asm.R13): asmRegister{regnum.AMD64_R13, 0, 0},
	int(x86asm.R14): asmRegister{regnum.AMD64_R14, 0, 0},
	int(x86asm.R15): asmRegister{regnum.AMD64_R15, 0, 0},
}
