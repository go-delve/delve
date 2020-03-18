package proc

import "fmt"

// FindFileLocation returns the PC for a given file:line.
// Assumes that `file` is normalized to lower case and '/' on Windows.
func FindFileLocation(p Process, fileName string, lineno int) ([]uint64, error) {
	pcs, err := p.BinInfo().LineToPC(fileName, lineno)
	if err != nil {
		return nil, err
	}
	var fn *Function
	for i := range pcs {
		if fn == nil || pcs[i] < fn.Entry || pcs[i] >= fn.End {
			fn = p.BinInfo().PCToFunc(pcs[i])
		}
		if fn != nil && fn.Entry == pcs[i] {
			pcs[i], _ = FirstPCAfterPrologue(p, fn, true)
		}
	}
	return pcs, nil
}

// ErrFunctionNotFound is returned when failing to find the
// function named 'FuncName' within the binary.
type ErrFunctionNotFound struct {
	FuncName string
}

func (err *ErrFunctionNotFound) Error() string {
	return fmt.Sprintf("Could not find function %s\n", err.FuncName)
}

// FindFunctionLocation finds address of a function's line
// If lineOffset is passed FindFunctionLocation will return the address of that line
func FindFunctionLocation(p Process, funcName string, lineOffset int) ([]uint64, error) {
	bi := p.BinInfo()
	origfn := bi.LookupFunc[funcName]
	if origfn == nil {
		return nil, &ErrFunctionNotFound{funcName}
	}

	if lineOffset <= 0 {
		r := make([]uint64, 0, len(origfn.InlinedCalls)+1)
		if origfn.Entry > 0 {
			// add concrete implementation of the function
			pc, err := FirstPCAfterPrologue(p, origfn, false)
			if err != nil {
				return nil, err
			}
			r = append(r, pc)
		}
		// add inlined calls to the function
		for _, call := range origfn.InlinedCalls {
			r = append(r, call.LowPC)
		}
		if len(r) == 0 {
			return nil, &ErrFunctionNotFound{funcName}
		}
		return r, nil
	}
	filename, lineno := origfn.cu.lineInfo.PCToLine(origfn.Entry, origfn.Entry)
	return bi.LineToPC(filename, lineno+lineOffset)
}

// FindDeferReturnCalls will find all runtime.deferreturn locations in the given buffer of
// assembly instructions.
// See documentation of Breakpoint.DeferCond for why this is necessary
func FindDeferReturnCalls(text []AsmInstruction) []uint64 {
	const deferreturn = "runtime.deferreturn"
	deferreturns := []uint64{}

	for _, instr := range text {
		if instr.IsCall() && instr.DestLoc != nil && instr.DestLoc.Fn != nil && instr.DestLoc.Fn.Name == deferreturn {
			deferreturns = append(deferreturns, instr.Loc.PC)
		}
	}
	return deferreturns
}
