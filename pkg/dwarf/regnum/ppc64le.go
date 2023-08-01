package regnum

import "fmt"

// The mapping between hardware registers and DWARF registers is specified
// in the 64-Bit ELF V2 ABI Specification of the Power Architecture in section
// 2.4 DWARF Definition
// https://openpowerfoundation.org/specifications/64bitelfabi/

const (
	// General Purpose Registers: from R0 to R31
	PPC64LE_FIRST_GPR = 0
	PPC64LE_R0        = PPC64LE_FIRST_GPR
	PPC64LE_LAST_GPR  = 31
	// Floating point registers: from F0 to F31
	PPC64LE_FIRST_FPR = 32
	PPC64LE_F0        = PPC64LE_FIRST_FPR
	PPC64LE_LAST_FPR  = 63
	// Vector (Altivec/VMX) registers: from V0 to V31
	PPC64LE_FIRST_VMX = 77
	PPC64LE_V0        = PPC64LE_FIRST_VMX
	PPC64LE_LAST_VMX  = 108
	// Vector Scalar (VSX) registers: from VS32 to VS63
	// On ppc64le these are mapped to F0 to F31
	PPC64LE_FIRST_VSX = 32
	PPC64LE_VS0       = PPC64LE_FIRST_VSX
	PPC64LE_LAST_VSX  = 63
	// Condition Registers: from CR0 to CR7
	PPC64LE_CR0 = 0
	// Special registers
	PPC64LE_SP = 1  // Stack frame pointer: Gpr[1]
	PPC64LE_PC = 12 // The documentation refers to this as the CIA (Current Instruction Address)
	PPC64LE_LR = 65 // Link register
)

func PPC64LEToName(num uint64) string {
	switch {
	case num == PPC64LE_SP:
		return "SP"
	case num == PPC64LE_PC:
		return "PC"
	case num == PPC64LE_LR:
		return "LR"
	case isGPR(num):
		return fmt.Sprintf("r%d", int(num-PPC64LE_FIRST_GPR))
	case isFPR(num):
		return fmt.Sprintf("f%d", int(num-PPC64LE_FIRST_FPR))
	case isVMX(num):
		return fmt.Sprintf("v%d", int(num-PPC64LE_FIRST_VMX))
	case isVSX(num):
		return fmt.Sprintf("vs%d", int(num-PPC64LE_FIRST_VSX))
	default:
		return fmt.Sprintf("unknown%d", num)
	}
}

// PPC64LEMaxRegNum is 172 registers in total, across 4 categories:
// General Purpose Registers or GPR (32 GPR + 9 special registers)
// Floating Point Registers or FPR (32 FPR + 1 special register)
// Altivec/VMX Registers or VMX (32 VMX + 2 special registers)
// VSX Registers or VSX (64 VSX)
// Documentation: https://lldb.llvm.org/cpp_reference/RegisterContextPOSIX__ppc64le_8cpp_source.html
func PPC64LEMaxRegNum() uint64 {
	return 172
}

func isGPR(num uint64) bool {
	return num < PPC64LE_LAST_GPR
}

func isFPR(num uint64) bool {
	return num >= PPC64LE_FIRST_FPR && num <= PPC64LE_LAST_FPR
}

func isVMX(num uint64) bool {
	return num >= PPC64LE_FIRST_VMX && num <= PPC64LE_LAST_VMX
}

func isVSX(num uint64) bool {
	return num >= PPC64LE_FIRST_VSX && num <= PPC64LE_LAST_VSX
}

var PPC64LENameToDwarf = func() map[string]int {
	r := make(map[string]int)

	r["nip"] = PPC64LE_PC
	r["sp"] = PPC64LE_SP
	r["bp"] = PPC64LE_SP
	r["link"] = PPC64LE_LR

	// General Purpose Registers: from R0 to R31
	for i := 0; i <= 31; i++ {
		r[fmt.Sprintf("r%d", i)] = PPC64LE_R0 + i
	}

	// Floating point registers: from F0 to F31
	for i := 0; i <= 31; i++ {
		r[fmt.Sprintf("f%d", i)] = PPC64LE_F0 + i
	}

	// Vector (Altivec/VMX) registers: from V0 to V31
	for i := 0; i <= 31; i++ {
		r[fmt.Sprintf("v%d", i)] = PPC64LE_V0 + i
	}

	// Vector Scalar (VSX) registers: from VS0 to VS63
	for i := 0; i <= 63; i++ {
		r[fmt.Sprintf("vs%d", i)] = PPC64LE_VS0 + i
	}

	// Condition Registers: from CR0 to CR7
	for i := 0; i <= 7; i++ {
		r[fmt.Sprintf("cr%d", i)] = PPC64LE_CR0 + i
	}
	return r
}()
