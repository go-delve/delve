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
	int(x86asm.AL):   {regnum.AMD64_Rax, 0, mask8},
	int(x86asm.CL):   {regnum.AMD64_Rcx, 0, mask8},
	int(x86asm.DL):   {regnum.AMD64_Rdx, 0, mask8},
	int(x86asm.BL):   {regnum.AMD64_Rbx, 0, mask8},
	int(x86asm.AH):   {regnum.AMD64_Rax, 8, mask8},
	int(x86asm.CH):   {regnum.AMD64_Rcx, 8, mask8},
	int(x86asm.DH):   {regnum.AMD64_Rdx, 8, mask8},
	int(x86asm.BH):   {regnum.AMD64_Rbx, 8, mask8},
	int(x86asm.SPB):  {regnum.AMD64_Rsp, 0, mask8},
	int(x86asm.BPB):  {regnum.AMD64_Rbp, 0, mask8},
	int(x86asm.SIB):  {regnum.AMD64_Rsi, 0, mask8},
	int(x86asm.DIB):  {regnum.AMD64_Rdi, 0, mask8},
	int(x86asm.R8B):  {regnum.AMD64_R8, 0, mask8},
	int(x86asm.R9B):  {regnum.AMD64_R9, 0, mask8},
	int(x86asm.R10B): {regnum.AMD64_R10, 0, mask8},
	int(x86asm.R11B): {regnum.AMD64_R11, 0, mask8},
	int(x86asm.R12B): {regnum.AMD64_R12, 0, mask8},
	int(x86asm.R13B): {regnum.AMD64_R13, 0, mask8},
	int(x86asm.R14B): {regnum.AMD64_R14, 0, mask8},
	int(x86asm.R15B): {regnum.AMD64_R15, 0, mask8},

	// 16-bit
	int(x86asm.AX):   {regnum.AMD64_Rax, 0, mask16},
	int(x86asm.CX):   {regnum.AMD64_Rcx, 0, mask16},
	int(x86asm.DX):   {regnum.AMD64_Rdx, 0, mask16},
	int(x86asm.BX):   {regnum.AMD64_Rbx, 0, mask16},
	int(x86asm.SP):   {regnum.AMD64_Rsp, 0, mask16},
	int(x86asm.BP):   {regnum.AMD64_Rbp, 0, mask16},
	int(x86asm.SI):   {regnum.AMD64_Rsi, 0, mask16},
	int(x86asm.DI):   {regnum.AMD64_Rdi, 0, mask16},
	int(x86asm.R8W):  {regnum.AMD64_R8, 0, mask16},
	int(x86asm.R9W):  {regnum.AMD64_R9, 0, mask16},
	int(x86asm.R10W): {regnum.AMD64_R10, 0, mask16},
	int(x86asm.R11W): {regnum.AMD64_R11, 0, mask16},
	int(x86asm.R12W): {regnum.AMD64_R12, 0, mask16},
	int(x86asm.R13W): {regnum.AMD64_R13, 0, mask16},
	int(x86asm.R14W): {regnum.AMD64_R14, 0, mask16},
	int(x86asm.R15W): {regnum.AMD64_R15, 0, mask16},

	// 32-bit
	int(x86asm.EAX):  {regnum.AMD64_Rax, 0, mask32},
	int(x86asm.ECX):  {regnum.AMD64_Rcx, 0, mask32},
	int(x86asm.EDX):  {regnum.AMD64_Rdx, 0, mask32},
	int(x86asm.EBX):  {regnum.AMD64_Rbx, 0, mask32},
	int(x86asm.ESP):  {regnum.AMD64_Rsp, 0, mask32},
	int(x86asm.EBP):  {regnum.AMD64_Rbp, 0, mask32},
	int(x86asm.ESI):  {regnum.AMD64_Rsi, 0, mask32},
	int(x86asm.EDI):  {regnum.AMD64_Rdi, 0, mask32},
	int(x86asm.R8L):  {regnum.AMD64_R8, 0, mask32},
	int(x86asm.R9L):  {regnum.AMD64_R9, 0, mask32},
	int(x86asm.R10L): {regnum.AMD64_R10, 0, mask32},
	int(x86asm.R11L): {regnum.AMD64_R11, 0, mask32},
	int(x86asm.R12L): {regnum.AMD64_R12, 0, mask32},
	int(x86asm.R13L): {regnum.AMD64_R13, 0, mask32},
	int(x86asm.R14L): {regnum.AMD64_R14, 0, mask32},
	int(x86asm.R15L): {regnum.AMD64_R15, 0, mask32},

	// 64-bit
	int(x86asm.RAX): {regnum.AMD64_Rax, 0, 0},
	int(x86asm.RCX): {regnum.AMD64_Rcx, 0, 0},
	int(x86asm.RDX): {regnum.AMD64_Rdx, 0, 0},
	int(x86asm.RBX): {regnum.AMD64_Rbx, 0, 0},
	int(x86asm.RSP): {regnum.AMD64_Rsp, 0, 0},
	int(x86asm.RBP): {regnum.AMD64_Rbp, 0, 0},
	int(x86asm.RSI): {regnum.AMD64_Rsi, 0, 0},
	int(x86asm.RDI): {regnum.AMD64_Rdi, 0, 0},
	int(x86asm.R8):  {regnum.AMD64_R8, 0, 0},
	int(x86asm.R9):  {regnum.AMD64_R9, 0, 0},
	int(x86asm.R10): {regnum.AMD64_R10, 0, 0},
	int(x86asm.R11): {regnum.AMD64_R11, 0, 0},
	int(x86asm.R12): {regnum.AMD64_R12, 0, 0},
	int(x86asm.R13): {regnum.AMD64_R13, 0, 0},
	int(x86asm.R14): {regnum.AMD64_R14, 0, 0},
	int(x86asm.R15): {regnum.AMD64_R15, 0, 0},
}
