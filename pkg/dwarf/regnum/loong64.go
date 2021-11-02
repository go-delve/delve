package regnum

import (
	"fmt"
)

// The mapping between hardware registers and DWARF registers, See
// https://loongson.github.io/LoongArch-Documentation/LoongArch-Vol1-EN.html

const (
	// General CPU Registers
	LOONG64_R0  = 0
	LOONG64_R1  = 1
	LOONG64_R2  = 2
	LOONG64_R3  = 3
	LOONG64_R4  = 4
	LOONG64_R5  = 5
	LOONG64_R6  = 6
	LOONG64_R7  = 7
	LOONG64_R8  = 8
	LOONG64_R9  = 9
	LOONG64_R10 = 10
	LOONG64_R11 = 11
	LOONG64_R12 = 12
	LOONG64_R13 = 13
	LOONG64_R14 = 14
	LOONG64_R15 = 15
	LOONG64_R16 = 16
	LOONG64_R17 = 17
	LOONG64_R18 = 18
	LOONG64_R19 = 19
	LOONG64_R20 = 20
	LOONG64_R21 = 21
	LOONG64_R22 = 22
	LOONG64_R23 = 23
	LOONG64_R24 = 24
	LOONG64_R25 = 25
	LOONG64_R26 = 26
	LOONG64_R27 = 27
	LOONG64_R28 = 28
	LOONG64_R29 = 29
	LOONG64_R30 = 30
	LOONG64_R31 = 31

	// Control and Status Registers
	LOONG64_ERA  = 32
	LOONG64_BADV = 33

	// See golang src/cmd/link/internal/loong64/l.go
	LOONG64_SP = LOONG64_R3  // sp  : stack pointer
	LOONG64_FP = LOONG64_R22 // fp  : frame pointer
	LOONG64_LR = LOONG64_R1  // ra  : address fro subroutine
	LOONG64_PC = LOONG64_ERA // rea : exception program counter

	_LOONG64_MaxRegNum = LOONG64_BADV + 1
)

func LOONG64ToName(num uint64) string {
	switch {
	case num <= LOONG64_R31:
		return fmt.Sprintf("r%d", num)

	case num == LOONG64_ERA:
		return "era"

	case num == LOONG64_BADV:
		return "badv"

	default:
		return fmt.Sprintf("unknown%d", num)
	}
}

func LOONG64MaxRegNum() uint64 {
	return _LOONG64_MaxRegNum
}

var LOONG64NameToDwarf = func() map[string]int {
	r := make(map[string]int)
	for i := 0; i <= LOONG64_R31; i++ {
		r[fmt.Sprintf("r%d", i)] = LOONG64_R0 + i
	}

	r["era"] = 32
	r["badv"] = 33

	return r
}()
