package proc

// AsmInstruction represents one assembly instruction.
type AsmInstruction struct {
	Loc        Location
	DestLoc    *Location
	Bytes      []byte
	Breakpoint bool
	AtPC       bool
	Inst       *archInst
}

// AssemblyFlavour is the assembly syntax to display.
type AssemblyFlavour int

const (
	// GNUFlavour will display GNU assembly syntax.
	GNUFlavour = AssemblyFlavour(iota)
	// IntelFlavour will display Intel assembly syntax.
	IntelFlavour
	// GoFlavour will display Go assembly syntax.
	GoFlavour
)

// Disassemble disassembles target memory between startAddr and endAddr, marking
// the current instruction being executed in goroutine g.
// If currentGoroutine is set and thread is stopped at a CALL instruction Disassemble
// will evaluate the argument of the CALL instruction using the thread's registers.
// Be aware that the Bytes field of each returned instruction is a slice of a larger array of size startAddr - endAddr.
func Disassemble(mem MemoryReadWriter, regs Registers, breakpoints *BreakpointMap, bi *BinaryInfo, startAddr, endAddr uint64) ([]AsmInstruction, error) {
	return disassemble(mem, regs, breakpoints, bi, startAddr, endAddr, false)
}

func disassemble(memrw MemoryReadWriter, regs Registers, breakpoints *BreakpointMap, bi *BinaryInfo, startAddr, endAddr uint64, singleInstr bool) ([]AsmInstruction, error) {
	mem := make([]byte, int(endAddr-startAddr))
	_, err := memrw.ReadMemory(mem, uintptr(startAddr))
	if err != nil {
		return nil, err
	}

	r := make([]AsmInstruction, 0, len(mem)/15)
	pc := startAddr

	var curpc uint64
	if regs != nil {
		curpc = regs.PC()
	}

	for len(mem) > 0 {
		bp, atbp := breakpoints.M[pc]
		if atbp {
			for i := range bp.OriginalData {
				mem[i] = bp.OriginalData[i]
			}
		}
		file, line, fn := bi.PCToLine(pc)
		loc := Location{PC: pc, File: file, Line: line, Fn: fn}
		inst, err := asmDecode(mem, pc)
		if err == nil {
			atpc := (regs != nil) && (curpc == pc)
			destloc := resolveCallArg(inst, atpc, regs, memrw, bi)
			r = append(r, AsmInstruction{Loc: loc, DestLoc: destloc, Bytes: mem[:inst.Len], Breakpoint: atbp, AtPC: atpc, Inst: inst})

			pc += uint64(inst.Size())
			mem = mem[inst.Size():]
		} else {
			r = append(r, AsmInstruction{Loc: loc, Bytes: mem[:1], Breakpoint: atbp, Inst: nil})
			pc++
			mem = mem[1:]
		}
		if singleInstr {
			break
		}
	}
	return r, nil
}
