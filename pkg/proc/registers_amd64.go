package proc

import (
	"bytes"
	"encoding/binary"

	"golang.org/x/arch/x86/x86asm"
)

var dwarfToAsm = map[int]x86asm.Reg{
	0:  x86asm.RAX,
	1:  x86asm.RDX,
	2:  x86asm.RCX,
	3:  x86asm.RBX,
	4:  x86asm.RSI,
	5:  x86asm.RDI,
	6:  x86asm.RBP,
	7:  x86asm.RSP,
	8:  x86asm.R8,
	9:  x86asm.R9,
	10: x86asm.R10,
	11: x86asm.R11,
	12: x86asm.R12,
	13: x86asm.R13,
	14: x86asm.R14,
	15: x86asm.R15,
	16: x86asm.RIP,
}

var dwarfToName = map[int]string{
	17: "XMM0",
	18: "XMM1",
	19: "XMM2",
	20: "XMM3",
	21: "XMM4",
	22: "XMM5",
	23: "XMM6",
	24: "XMM7",
	25: "XMM8",
	26: "XMM9",
	27: "XMM10",
	28: "XMM11",
	29: "XMM12",
	30: "XMM13",
	31: "XMM14",
	32: "XMM15",
	33: "ST(0)",
	34: "ST(1)",
	35: "ST(2)",
	36: "ST(3)",
	37: "ST(4)",
	38: "ST(5)",
	39: "ST(6)",
	40: "ST(7)",
	49: "Eflags",
	50: "Es",
	51: "Cs",
	52: "Ss",
	53: "Ds",
	54: "Fs",
	55: "Gs",
	58: "Fs_base",
	59: "Gs_base",
	64: "MXCSR",
	65: "CW",
	66: "SW",
}

// GetDwarfRegister maps between DWARF register numbers and architecture
// registers.
// The mapping is specified in the System V ABI AMD64 Architecture Processor
// Supplement page 57, figure 3.36
// https://www.uclibc.org/docs/psABI-x86_64.pdf
func GetDwarfRegister(regs Registers, i int) []byte {
	if asmreg, ok := dwarfToAsm[i]; ok {
		x, _ := regs.Get(int(asmreg))
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, x)
		return buf.Bytes()
	}
	if regname, ok := dwarfToName[i]; ok {
		regslice := regs.Slice(true)
		for _, reg := range regslice {
			if reg.Name == regname {
				return reg.Bytes
			}
		}
	}
	return []byte{}
}
