package proc

type AsmInstruction struct {
	Loc        Location
	DestLoc    *Location
	Bytes      []byte
	Breakpoint bool
	AtPC       bool
	Inst       *ArchInst
}

type AssemblyFlavour int

const (
	GNUFlavour = AssemblyFlavour(iota)
	IntelFlavour
)

// Disassemble disassembles target memory between startPC and endPC
// If currentGoroutine is set and thread is stopped at a CALL instruction Disassemble will evaluate the argument of the CALL instruction using the thread's registers
// Be aware that the Bytes field of each returned instruction is a slice of a larger array of size endPC - startPC
func (t *Thread) Disassemble(startPC, endPC uint64, currentGoroutine bool) ([]AsmInstruction, error) {
	if t.p.exited {
		return nil, &ProcessExitedError{}
	}
	mem, err := t.readMemory(uintptr(startPC), int(endPC-startPC))
	if err != nil {
		return nil, err
	}

	r := make([]AsmInstruction, 0, len(mem)/15)
	pc := startPC

	var curpc uint64
	var regs Registers
	if currentGoroutine {
		regs, _ = t.Registers()
		if regs != nil {
			curpc = regs.PC()
		}
	}

	for len(mem) > 0 {
		bp, atbp := t.p.Breakpoints[pc]
		if atbp {
			for i := range bp.OriginalData {
				mem[i] = bp.OriginalData[i]
			}
		}
		file, line, fn := t.p.Dwarf.PCToLine(pc)
		loc := Location{PC: pc, File: file, Line: line, Fn: fn}
		inst, err := asmDecode(mem, pc)
		if err == nil {
			atpc := currentGoroutine && (curpc == pc)
			destloc := t.resolveCallArg(inst, atpc, regs)
			r = append(r, AsmInstruction{Loc: loc, DestLoc: destloc, Bytes: mem[:inst.Len], Breakpoint: atbp, AtPC: atpc, Inst: inst})

			pc += uint64(inst.Size())
			mem = mem[inst.Size():]
		} else {
			r = append(r, AsmInstruction{Loc: loc, Bytes: mem[:1], Breakpoint: atbp, Inst: nil})
			pc++
			mem = mem[1:]
		}
	}
	return r, nil
}
