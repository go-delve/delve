package regnum

import (
	"fmt"
)

// The mapping between hardware registers and DWARF registers is specified
// in the DWARF for the ARM® Architecture page 7,
// Table 1
// http://infocenter.arm.com/help/topic/com.arm.doc.ihi0040b/IHI0040B_aadwarf.pdf

const (
	ARM64_X0         = 0  // X1 through X30 follow
	ARM64_BP         = 29 // also X29
	ARM64_LR         = 30 // also X30
	ARM64_SP         = 31
	ARM64_PC         = 32
	ARM64_V0         = 64 // V1 through V31 follow
	_ARM64_MaxRegNum = ARM64_V0 + 31
)

func ARM64ToName(num uint64) string {
	switch {
	case num <= 30:
		return fmt.Sprintf("X%d", num)
	case num == ARM64_SP:
		return "SP"
	case num == ARM64_PC:
		return "PC"
	case num >= ARM64_V0 && num <= 95:
		return fmt.Sprintf("V%d", num-64)
	default:
		return fmt.Sprintf("unknown%d", num)
	}
}

func ARM64MaxRegNum() uint64 {
	return _ARM64_MaxRegNum
}

var ARM64NameToDwarf = func() map[string]int {
	r := make(map[string]int)
	for i := 0; i <= 32; i++ {
		r[fmt.Sprintf("x%d", i)] = ARM64_X0 + i
	}
	r["fp"] = 29
	r["lr"] = 30
	r["sp"] = 31
	r["pc"] = 32

	for i := 0; i <= 31; i++ {
		r[fmt.Sprintf("v%d", i)] = ARM64_V0 + i
	}

	return r
}()
