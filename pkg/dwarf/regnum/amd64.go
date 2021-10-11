package regnum

import (
	"fmt"
	"strings"
)

// The mapping between hardware registers and DWARF registers is specified
// in the System V ABI AMD64 Architecture Processor Supplement v. 1.0 page 61,
// figure 3.36
// https://gitlab.com/x86-psABIs/x86-64-ABI/-/tree/master

const (
	AMD64_Rax     = 0
	AMD64_Rdx     = 1
	AMD64_Rcx     = 2
	AMD64_Rbx     = 3
	AMD64_Rsi     = 4
	AMD64_Rdi     = 5
	AMD64_Rbp     = 6
	AMD64_Rsp     = 7
	AMD64_R8      = 8
	AMD64_R9      = 9
	AMD64_R10     = 10
	AMD64_R11     = 11
	AMD64_R12     = 12
	AMD64_R13     = 13
	AMD64_R14     = 14
	AMD64_R15     = 15
	AMD64_Rip     = 16
	AMD64_XMM0    = 17 // XMM1 through XMM15 follow
	AMD64_ST0     = 33 // ST(1) through ST(7) follow
	AMD64_Rflags  = 49
	AMD64_Es      = 50
	AMD64_Cs      = 51
	AMD64_Ss      = 52
	AMD64_Ds      = 53
	AMD64_Fs      = 54
	AMD64_Gs      = 55
	AMD64_Fs_base = 58
	AMD64_Gs_base = 59
	AMD64_MXCSR   = 64
	AMD64_CW      = 65
	AMD64_SW      = 66
	AMD64_XMM16   = 67  // XMM17 through XMM31 follow
	AMD64_K0      = 118 // k1 through k7 follow
)

var amd64DwarfToName = map[uint64]string{
	AMD64_Rax:        "Rax",
	AMD64_Rdx:        "Rdx",
	AMD64_Rcx:        "Rcx",
	AMD64_Rbx:        "Rbx",
	AMD64_Rsi:        "Rsi",
	AMD64_Rdi:        "Rdi",
	AMD64_Rbp:        "Rbp",
	AMD64_Rsp:        "Rsp",
	AMD64_R8:         "R8",
	AMD64_R9:         "R9",
	AMD64_R10:        "R10",
	AMD64_R11:        "R11",
	AMD64_R12:        "R12",
	AMD64_R13:        "R13",
	AMD64_R14:        "R14",
	AMD64_R15:        "R15",
	AMD64_Rip:        "Rip",
	AMD64_XMM0:       "XMM0",
	AMD64_XMM0 + 1:   "XMM1",
	AMD64_XMM0 + 2:   "XMM2",
	AMD64_XMM0 + 3:   "XMM3",
	AMD64_XMM0 + 4:   "XMM4",
	AMD64_XMM0 + 5:   "XMM5",
	AMD64_XMM0 + 6:   "XMM6",
	AMD64_XMM0 + 7:   "XMM7",
	AMD64_XMM0 + 8:   "XMM8",
	AMD64_XMM0 + 9:   "XMM9",
	AMD64_XMM0 + 10:  "XMM10",
	AMD64_XMM0 + 11:  "XMM11",
	AMD64_XMM0 + 12:  "XMM12",
	AMD64_XMM0 + 13:  "XMM13",
	AMD64_XMM0 + 14:  "XMM14",
	AMD64_XMM0 + 15:  "XMM15",
	AMD64_ST0:        "ST(0)",
	AMD64_ST0 + 1:    "ST(1)",
	AMD64_ST0 + 2:    "ST(2)",
	AMD64_ST0 + 3:    "ST(3)",
	AMD64_ST0 + 4:    "ST(4)",
	AMD64_ST0 + 5:    "ST(5)",
	AMD64_ST0 + 6:    "ST(6)",
	AMD64_ST0 + 7:    "ST(7)",
	AMD64_Rflags:     "Rflags",
	AMD64_Es:         "Es",
	AMD64_Cs:         "Cs",
	AMD64_Ss:         "Ss",
	AMD64_Ds:         "Ds",
	AMD64_Fs:         "Fs",
	AMD64_Gs:         "Gs",
	AMD64_Fs_base:    "Fs_base",
	AMD64_Gs_base:    "Gs_base",
	AMD64_MXCSR:      "MXCSR",
	AMD64_CW:         "CW",
	AMD64_SW:         "SW",
	AMD64_XMM16:      "XMM16",
	AMD64_XMM16 + 1:  "XMM17",
	AMD64_XMM16 + 2:  "XMM18",
	AMD64_XMM16 + 3:  "XMM19",
	AMD64_XMM16 + 4:  "XMM20",
	AMD64_XMM16 + 5:  "XMM21",
	AMD64_XMM16 + 6:  "XMM22",
	AMD64_XMM16 + 7:  "XMM23",
	AMD64_XMM16 + 8:  "XMM24",
	AMD64_XMM16 + 9:  "XMM25",
	AMD64_XMM16 + 10: "XMM26",
	AMD64_XMM16 + 11: "XMM27",
	AMD64_XMM16 + 12: "XMM28",
	AMD64_XMM16 + 13: "XMM29",
	AMD64_XMM16 + 14: "XMM30",
	AMD64_XMM16 + 15: "XMM31",
	AMD64_K0:         "K0",
	AMD64_K0 + 1:     "K1",
	AMD64_K0 + 2:     "K2",
	AMD64_K0 + 3:     "K3",
	AMD64_K0 + 4:     "K4",
	AMD64_K0 + 5:     "K5",
	AMD64_K0 + 6:     "K6",
	AMD64_K0 + 7:     "K7",
}

var AMD64NameToDwarf = func() map[string]int {
	r := make(map[string]int)
	for regNum, regName := range amd64DwarfToName {
		r[strings.ToLower(regName)] = int(regNum)
	}
	r["eflags"] = 49
	r["st0"] = 33
	r["st1"] = 34
	r["st2"] = 35
	r["st3"] = 36
	r["st4"] = 37
	r["st5"] = 38
	r["st6"] = 39
	r["st7"] = 40
	return r
}()

func AMD64MaxRegNum() uint64 {
	max := uint64(AMD64_Rip)
	for i := range amd64DwarfToName {
		if i > max {
			max = i
		}
	}
	return max
}

func AMD64ToName(num uint64) string {
	name, ok := amd64DwarfToName[num]
	if ok {
		return name
	}
	return fmt.Sprintf("unknown%d", num)
}
