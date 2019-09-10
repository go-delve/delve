package proc

import (
	"bytes"
	"encoding/binary"
	"golang.org/x/arch/arm64/arm64asm"
//	"golang.org/x/arch/x86/x86asm"
)

var dwarfToAsm = map[int]arm64asm.Reg{
	0:  arm64asm.X0,
	1:  arm64asm.X1,
	2:  arm64asm.X2,
	3:  arm64asm.X3,
	4:  arm64asm.X4,
	5:  arm64asm.X5,
	6:  arm64asm.X6,
	7:  arm64asm.X7,
	8:  arm64asm.X8,
	9:  arm64asm.X9,
	10: arm64asm.X10,
	11: arm64asm.X11,
	12: arm64asm.X12,
	13: arm64asm.X13,
	14: arm64asm.X14,
	15: arm64asm.X15,
	16: arm64asm.X16,
	17: arm64asm.X17,
	18: arm64asm.X18,
	19: arm64asm.X19,
	20: arm64asm.X20,
	21: arm64asm.X21,
	22: arm64asm.X22,
	23: arm64asm.X23,
	24: arm64asm.X24,
	25: arm64asm.X25,
	26: arm64asm.X26,
	27: arm64asm.X27,
	28: arm64asm.X28,
	29: arm64asm.X29,
	30: arm64asm.X30,
	31: arm64asm.SP,
	64:  arm64asm.V0,
	65:  arm64asm.V1,
	66:  arm64asm.V2,
	67:  arm64asm.V3,
	68:  arm64asm.V4,
	69:  arm64asm.V5,
	70:  arm64asm.V6,
	71:  arm64asm.V7,
	72:  arm64asm.V8,
	73:  arm64asm.V9,
	74: arm64asm.V10,
	75: arm64asm.V11,
	76: arm64asm.V12,
	77: arm64asm.V13,
	78: arm64asm.V14,
	79: arm64asm.V15,
	80: arm64asm.V16,
	81: arm64asm.V17,
	82: arm64asm.V18,
	83: arm64asm.V19,
	84: arm64asm.V20,
	85: arm64asm.V21,
	86: arm64asm.V22,
	87: arm64asm.V23,
	88: arm64asm.V24,
	89: arm64asm.V25,
	90: arm64asm.V26,
	91: arm64asm.V27,
	92: arm64asm.V28,
	93: arm64asm.V29,
	94: arm64asm.V30,
	95: arm64asm.V31,
}
/*
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
*/
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
	return []byte{}
}
