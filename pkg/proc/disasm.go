package proc

// AsmInstruction represents one assembly instruction.
type AsmInstruction struct {
	Loc        Location
	DestLoc    *Location
	Bytes      []byte
	Breakpoint bool
	AtPC       bool

	Size int
	Kind AsmInstructionKind

	Inst archInst
}

type AsmInstructionKind uint8

const (
	OtherInstruction AsmInstructionKind = iota
	CallInstruction
	RetInstruction
)

func (instr *AsmInstruction) IsCall() bool {
	return instr.Kind == CallInstruction
}

func (instr *AsmInstruction) IsRet() bool {
	return instr.Kind == RetInstruction
}

type archInst interface {
	Text(flavour AssemblyFlavour, pc uint64, symLookup func(uint64) (string, uint64)) string
	OpcodeEquals(op uint64) bool
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

type opcodeSeq []uint64

// firstPCAfterPrologueDisassembly returns the address of the first
// instruction after the prologue for function fn by disassembling fn and
// matching the instructions against known split-stack prologue patterns.
// If sameline is set firstPCAfterPrologueDisassembly will always return an
// address associated with the same line as fn.Entry
func firstPCAfterPrologueDisassembly(p Process, breakpoints *BreakpointMap, fn *Function, sameline bool) (uint64, error) {
	var mem MemoryReadWriter = p.CurrentThread()
	bi := p.BinInfo()
	text, err := disassemble(mem, nil, breakpoints, bi, fn.Entry, fn.End, false)
	if err != nil {
		return fn.Entry, err
	}

	if len(text) <= 0 {
		return fn.Entry, nil
	}

	for _, prologue := range p.BinInfo().Arch.Prologues() {
		if len(prologue) >= len(text) {
			continue
		}
		if checkPrologue(text, prologue) {
			r := &text[len(prologue)]
			if sameline {
				if r.Loc.Line != text[0].Loc.Line {
					return fn.Entry, nil
				}
			}
			return r.Loc.PC, nil
		}
	}

	return fn.Entry, nil
}

func checkPrologue(s []AsmInstruction, prologuePattern opcodeSeq) bool {
	line := s[0].Loc.Line
	for i, op := range prologuePattern {
		if !s[i].Inst.OpcodeEquals(op) || s[i].Loc.Line != line {
			return false
		}
	}
	return true
}

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

	r := make([]AsmInstruction, 0, len(mem)/int(bi.Arch.MaxInstructionLength()))
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

		var inst AsmInstruction
		inst.Loc = Location{PC: pc, File: file, Line: line, Fn: fn}
		inst.Breakpoint = atbp
		inst.AtPC = (regs != nil) && (curpc == pc)

		bi.Arch.AsmDecode(&inst, mem, regs, memrw, bi)

		r = append(r, inst)

		pc += uint64(inst.Size)
		mem = mem[inst.Size:]

		if singleInstr {
			break
		}
	}
	return r, nil
}

// Text will return the assembly instructions in human readable format according to
// the flavour specified.
func (inst *AsmInstruction) Text(flavour AssemblyFlavour, bi *BinaryInfo) string {
	return inst.Inst.Text(flavour, inst.Loc.PC, bi.symLookup)
}
