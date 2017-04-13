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

func (dbp *Process) Disassemble(g *G, startPC, endPC uint64) ([]AsmInstruction, error) {
	if g == nil {
		regs, _ := dbp.currentThread.Registers(false)
		return Disassemble(dbp.currentThread, regs, dbp.breakpoints, &dbp.bi, startPC, endPC)
	}

	var regs Registers
	thread := dbp.currentThread
	if g.thread != nil {
		thread = g.thread
		regs, _ = g.thread.Registers(false)
	}

	return Disassemble(thread, regs, dbp.breakpoints, &dbp.bi, startPC, endPC)
}

// Disassemble disassembles target memory between startPC and endPC
// If currentGoroutine is set and thread is stopped at a CALL instruction Disassemble will evaluate the argument of the CALL instruction using the thread's registers
// Be aware that the Bytes field of each returned instruction is a slice of a larger array of size endPC - startPC
func Disassemble(memrw memoryReadWriter, regs Registers, breakpoints map[uint64]*Breakpoint, bi *BinaryInfo, startPC, endPC uint64) ([]AsmInstruction, error) {
	mem, err := memrw.readMemory(uintptr(startPC), int(endPC-startPC))
	if err != nil {
		return nil, err
	}

	r := make([]AsmInstruction, 0, len(mem)/15)
	pc := startPC

	var curpc uint64
	if regs != nil {
		curpc = regs.PC()
	}

	for len(mem) > 0 {
		bp, atbp := breakpoints[pc]
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
	}
	return r, nil
}
