package proc

import (
	"errors"
	"fmt"
)

// TODO(refactor) REMOVE BEFORE MERGE - this should be removed when execution logic is moved to target
const UnrecoveredPanic = "unrecovered-panic"

// ErrNotExecutable is returned after attempting to execute a non-executable file
// to begin a debug session.
var ErrNotExecutable = errors.New("not an executable file")

// ErrNotRecorded is returned when an action is requested that is
// only possible on recorded (traced) programs.
var ErrNotRecorded = errors.New("not a recording")

// ErrProcessExited indicates that the process has exited and contains both
// process id and exit status.
type ErrProcessExited struct {
	Pid    int
	Status int
}

func (pe ErrProcessExited) Error() string {
	return fmt.Sprintf("Process %d has exited with status %d", pe.Pid, pe.Status)
}

// ProcessDetachedError indicates that we detached from the target process.
type ProcessDetachedError struct {
}

func (pe ProcessDetachedError) Error() string {
	return "detached from the process"
}

// FirstPCAfterPrologue returns the address of the first
// instruction after the prologue for function fn.
// If sameline is set FirstPCAfterPrologue will always return an
// address associated with the same line as fn.Entry.
func FirstPCAfterPrologue(bi *BinaryInfo, mem MemoryReadWriter, breakpoints *BreakpointMap, fn *Function, sameline bool) (uint64, error) {
	pc, _, line, ok := fn.CompileUnit.LineInfo.PrologueEndPC(fn.Entry, fn.End)
	if ok {
		if !sameline {
			return pc, nil
		}
		_, entryLine := fn.CompileUnit.LineInfo.PCToLine(fn.Entry, fn.Entry)
		if entryLine == line {
			return pc, nil
		}
	}

	pc, err := firstPCAfterPrologueDisassembly(mem, bi, breakpoints, fn, sameline)
	if err != nil {
		return fn.Entry, err
	}

	if pc == fn.Entry {
		// Look for the first instruction with the stmt flag set, so that setting a
		// breakpoint with file:line and with the function name always result on
		// the same instruction being selected.
		if pc2, _, _, ok := fn.CompileUnit.LineInfo.FirstStmtForLine(fn.Entry, fn.End); ok {
			return pc2, nil
		}
	}

	return pc, nil
}
