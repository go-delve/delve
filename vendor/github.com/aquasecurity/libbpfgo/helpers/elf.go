package helpers

import (
	"debug/elf"
	"errors"
	"fmt"
)

// SymbolToOffset attempts to resolve a 'symbol' name in the binary found at
// 'path' to an offset. The offset can be used for attaching a u(ret)probe
func SymbolToOffset(path, symbol string) (uint32, error) {

	f, err := elf.Open(path)
	if err != nil {
		return 0, fmt.Errorf("could not open elf file to resolve symbol offset: %v", err)
	}

	syms, err := f.Symbols()
	if err != nil {
		return 0, fmt.Errorf("could not open symbol section to resolve symbol offset: %v", err)
	}

	sectionsToSearchForSymbol := []*elf.Section{}

	for i := range f.Sections {
		if f.Sections[i].Flags == elf.SHF_ALLOC+elf.SHF_EXECINSTR {
			sectionsToSearchForSymbol = append(sectionsToSearchForSymbol, f.Sections[i])
		}
	}

	var executableSection *elf.Section
	for i := range syms {
		if syms[i].Name == symbol {

			// Find what section the symbol is in by checking the executable section's
			// addr space.
			for m := range sectionsToSearchForSymbol {
				if syms[i].Value > sectionsToSearchForSymbol[m].Addr &&
					syms[i].Value < sectionsToSearchForSymbol[m].Addr+sectionsToSearchForSymbol[m].Size {
					executableSection = sectionsToSearchForSymbol[m]
				}
			}

			if executableSection == nil {
				return 0, errors.New("could not find symbol in executable sections of binary")
			}

			return uint32(syms[i].Value - executableSection.Addr + executableSection.Offset), nil
		}
	}
	return 0, fmt.Errorf("symbol not found")
}
