package regnum

import (
	"fmt"
)

// The mapping between hardware registers and DWARF registers, See
// https://loongson.github.io/LoongArch-Documentation/LoongArch-Vol1-EN.html
// https://loongson.github.io/LoongArch-Documentation/LoongArch-ELF-ABI-EN.html

const (
	// General-purpose Register
	LOONG64_R0  = 0
	LOONG64_LR  = 1 // ra: address for subroutine
	LOONG64_SP  = 3 // sp: stack pointer
	LOONG64_R22 = 22
	LOONG64_FP  = LOONG64_R22 // fp: frame pointer
	LOONG64_R31 = 31

	// Floating-point Register
	LOONG64_F0  = 32
	LOONG64_F31 = 63

	// Floating condition flag register
	LOONG64_FCC0 = 64
	LOONG64_FCC7 = 71

	LOONG64_FCSR = 72

	// Extra, not defined in ELF-ABI specification
	LOONG64_ERA  = 73
	LOONG64_BADV = 74

	// See golang src/cmd/link/internal/loong64/l.go
	LOONG64_PC = LOONG64_ERA // era : exception program counter

	_LOONG64_MaxRegNum = LOONG64_BADV
)

func LOONG64ToName(num uint64) string {
	switch {
	case num <= LOONG64_R31:
		return fmt.Sprintf("R%d", num)

	case num >= LOONG64_F0 && num <= LOONG64_F31:
		return fmt.Sprintf("F%d", num-32)

	case num >= LOONG64_FCC0 && num <= LOONG64_FCC7:
		return fmt.Sprintf("FCC%d", num-64)

	case num == LOONG64_FCSR:
		return "FCSR"

	case num == LOONG64_ERA:
		return "ERA"

	case num == LOONG64_BADV:
		return "BADV"

	default:
		return fmt.Sprintf("Unknown%d", num)
	}
}

func LOONG64MaxRegNum() uint64 {
	return _LOONG64_MaxRegNum
}

var LOONG64NameToDwarf = func() map[string]int {
	r := make(map[string]int)
	for i := 0; i <= 31; i++ {
		r[fmt.Sprintf("r%d", i)] = LOONG64_R0 + i
	}
	r["era"] = LOONG64_ERA
	r["badv"] = LOONG64_BADV

	for i := 0; i <= 31; i++ {
		r[fmt.Sprintf("f%d", i)] = LOONG64_F0 + i
	}

	for i := 0; i <= 7; i++ {
		r[fmt.Sprintf("fcc%d", i)] = LOONG64_FCC0 + i
	}
	r["fcsr"] = LOONG64_FCSR

	return r
}()
