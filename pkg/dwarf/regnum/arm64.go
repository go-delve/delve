package regnum

import (
	"fmt"
)

// The mapping between hardware registers and DWARF registers is specified
// in the DWARF for the ARM® Architecture page 7,
// Table 1
// http://infocenter.arm.com/help/topic/com.arm.doc.ihi0040b/IHI0040B_aadwarf.pdf

const (
	ARM64_X0 = 0  // X1 through X30 follow
	ARM64_BP = 29 // also X29
	ARM64_LR = 30 // also X30
	ARM64_SP = 31
	ARM64_PC = 32
	ARM64_V0 = 64 // V1 through V31 follow

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
