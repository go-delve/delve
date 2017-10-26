package proc

import "sort"

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
	GoFlavour
)

// Disassemble disassembles target memory between startPC and endPC, marking
// the current instruction being executed in goroutine g.
// If currentGoroutine is set and thread is stopped at a CALL instruction Disassemble will evaluate the argument of the CALL instruction using the thread's registers
// Be aware that the Bytes field of each returned instruction is a slice of a larger array of size endPC - startPC
func Disassemble(dbp Process, g *G, startPC, endPC uint64) ([]AsmInstruction, error) {
	if dbp.Exited() {
		return nil, &ProcessExitedError{Pid: dbp.Pid()}
	}
	if g == nil {
		ct := dbp.CurrentThread()
		regs, _ := ct.Registers(false)
		return disassemble(ct, regs, dbp.Breakpoints(), dbp.BinInfo(), startPC, endPC, false)
	}

	var regs Registers
	var mem MemoryReadWriter = dbp.CurrentThread()
	if g.Thread != nil {
		mem = g.Thread
		regs, _ = g.Thread.Registers(false)
	}

	return disassemble(mem, regs, dbp.Breakpoints(), dbp.BinInfo(), startPC, endPC, false)
}

func disassemble(memrw MemoryReadWriter, regs Registers, breakpoints *BreakpointMap, bi *BinaryInfo, startPC, endPC uint64, singleInstr bool) ([]AsmInstruction, error) {
	mem := make([]byte, int(endPC-startPC))
	_, err := memrw.ReadMemory(mem, uintptr(startPC))
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

// Looks up symbol (either functions or global variables) at address addr.
// Used by disassembly formatter.
func (bi *BinaryInfo) symLookup(addr uint64) (string, uint64) {
	fn := bi.PCToFunc(addr)
	if fn != nil {
		if fn.Entry == addr {
			// only report the function name if it's the exact address because it's
			// easier to read the absolute address than function_name+offset.
			return fn.Name, fn.Entry
		}
		return "", 0
	}
	i := sort.Search(len(bi.packageVars), func(i int) bool {
		return bi.packageVars[i].addr >= addr
	})
	if i >= len(bi.packageVars) {
		return "", 0
	}
	if bi.packageVars[i].addr > addr {
		// report previous variable + offset if i-th variable starts after addr
		i--
	}
	if i > 0 {
		return bi.packageVars[i].name, bi.packageVars[i].addr
	}
	return "", 0
}
