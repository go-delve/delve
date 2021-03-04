package regnum

import (
	"fmt"
	"strings"
)

// The mapping between hardware registers and DWARF registers is specified
// in the System V ABI Intel386 Architecture Processor Supplement page 25,
// table 2.14
// https://www.uclibc.org/docs/psABI-i386.pdf

const (
	I386_Eax    = 0
	I386_Ecx    = 1
	I386_Edx    = 2
	I386_Ebx    = 3
	I386_Esp    = 4
	I386_Ebp    = 5
	I386_Esi    = 6
	I386_Edi    = 7
	I386_Eip    = 8
	I386_Eflags = 9
	I386_ST0    = 11 // ST(1) through ST(7) follow
	I386_XMM0   = 21 // XMM1 through XMM7 follow
	I386_Es     = 40
	I386_Cs     = 41
	I386_Ss     = 42
	I386_Ds     = 43
	I386_Fs     = 44
	I386_Gs     = 45
)

var i386DwarfToName = map[int]string{
	I386_Eax:      "Eax",
	I386_Ecx:      "Ecx",
	I386_Edx:      "Edx",
	I386_Ebx:      "Ebx",
	I386_Esp:      "Esp",
	I386_Ebp:      "Ebp",
	I386_Esi:      "Esi",
	I386_Edi:      "Edi",
	I386_Eip:      "Eip",
	I386_Eflags:   "Eflags",
	I386_ST0:      "ST(0)",
	I386_ST0 + 1:  "ST(1)",
	I386_ST0 + 2:  "ST(2)",
	I386_ST0 + 3:  "ST(3)",
	I386_ST0 + 4:  "ST(4)",
	I386_ST0 + 5:  "ST(5)",
	I386_ST0 + 6:  "ST(6)",
	I386_ST0 + 7:  "ST(7)",
	I386_XMM0:     "XMM0",
	I386_XMM0 + 1: "XMM1",
	I386_XMM0 + 2: "XMM2",
	I386_XMM0 + 3: "XMM3",
	I386_XMM0 + 4: "XMM4",
	I386_XMM0 + 5: "XMM5",
	I386_XMM0 + 6: "XMM6",
	I386_XMM0 + 7: "XMM7",
	I386_Es:       "Es",
	I386_Cs:       "Cs",
	I386_Ss:       "Ss",
	I386_Ds:       "Ds",
	I386_Fs:       "Fs",
	I386_Gs:       "Gs",
}

var I386NameToDwarf = func() map[string]int {
	r := make(map[string]int)
	for regNum, regName := range i386DwarfToName {
		r[strings.ToLower(regName)] = regNum
	}
	r["eflags"] = 9
	r["st0"] = 11
	r["st1"] = 12
	r["st2"] = 13
	r["st3"] = 14
	r["st4"] = 15
	r["st5"] = 16
	r["st6"] = 17
	r["st7"] = 18
	return r
}()

func I386MaxRegNum() int {
	max := int(I386_Eip)
	for i := range i386DwarfToName {
		if i > max {
			max = i
		}
	}
	return max
}

func I386ToName(num int) string {
	name, ok := i386DwarfToName[num]
	if ok {
		return name
	}
	return fmt.Sprintf("unknown%d", num)
}
