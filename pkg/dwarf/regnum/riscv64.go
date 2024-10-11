package regnum

import "fmt"

// The mapping between hardware registers and DWARF registers, See
// https://github.com/riscv-non-isa/riscv-elf-psabi-doc/blob/master/riscv-dwarf.adoc

const (
	// Integer Registers
	RISCV64_X0 = 0
	// Link Register
	RISCV64_LR = 1
	// Stack Pointer
	RISCV64_SP = 2
	RISCV64_GP = 3
	RISCV64_TP = 4
	RISCV64_T0 = 5
	RISCV64_T1 = 6
	RISCV64_T2 = 7
	RISCV64_S0 = 8
	// Frame Pointer
	RISCV64_FP  = RISCV64_S0
	RISCV64_S1  = 9
	RISCV64_A0  = 10
	RISCV64_A1  = 11
	RISCV64_A2  = 12
	RISCV64_A3  = 13
	RISCV64_A4  = 14
	RISCV64_A5  = 15
	RISCV64_A6  = 16
	RISCV64_A7  = 17
	RISCV64_S2  = 18
	RISCV64_S3  = 19
	RISCV64_S4  = 20
	RISCV64_S5  = 21
	RISCV64_S6  = 22
	RISCV64_S7  = 23
	RISCV64_S8  = 24
	RISCV64_S9  = 25
	RISCV64_S10 = 26
	// G Register
	RISCV64_S11 = 27
	RISCV64_T3  = 28
	RISCV64_T4  = 29
	RISCV64_T5  = 30
	RISCV64_T6  = 31

	RISCV64_X31 = RISCV64_T6

	// Floating-point Registers
	RISCV64_F0  = 32
	RISCV64_F31 = 63

	// Not defined in DWARF specification
	RISCV64_PC = 65

	_RISC64_MaxRegNum = RISCV64_PC
)

func RISCV64ToName(num uint64) string {
	switch {
	case num <= RISCV64_X31:
		return fmt.Sprintf("X%d", num)

	case num >= RISCV64_F0 && num <= RISCV64_F31:
		return fmt.Sprintf("F%d", num)

	case num == RISCV64_PC:
		return "PC"

	default:
		return fmt.Sprintf("Unknown%d", num)
	}
}

func RISCV64MaxRegNum() uint64 {
	return _RISC64_MaxRegNum
}

var RISCV64NameToDwarf = func() map[string]int {
	r := make(map[string]int)
	for i := 0; i <= 31; i++ {
		r[fmt.Sprintf("x%d", i)] = RISCV64_X0 + i
	}
	r["pc"] = RISCV64_PC

	for i := 0; i <= 31; i++ {
		r[fmt.Sprintf("f%d", i)] = RISCV64_F0 + i
	}

	return r
}()
