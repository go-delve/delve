package proc

import (
	"encoding/binary"
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

// GoroutinesInfo searches for goroutines starting at index 'start', and
// returns an array of up to 'count' (or all found elements, if 'count' is 0)
// G structures representing the information Delve care about from the internal
// runtime G structure.
// GoroutinesInfo also returns the next index to be used as 'start' argument
// while scanning for all available goroutines, or -1 if there was an error
// or if the index already reached the last possible value.
func GoroutinesInfo(dbp Process, bi *BinaryInfo, mem MemoryReadWriter, start, count int) ([]*G, int, error) {
	if _, err := dbp.Valid(); err != nil {
		return nil, -1, err
	}
	if dbp.Common().allGCache != nil {
		// We can't use the cached array to fulfill a subrange request
		if start == 0 && (count == 0 || count >= len(dbp.Common().allGCache)) {
			return dbp.Common().allGCache, -1, nil
		}
	}

	exeimage := bi.Images[0] // Image corresponding to the executable file

	var (
		threadg = map[int]*G{}
		allg    []*G
		rdr     = exeimage.DwarfReader()
	)

	threads := dbp.ThreadList()
	for _, th := range threads {
		if th.Blocked() {
			continue
		}
		g, _ := GetG(th, bi)
		if g != nil {
			threadg[g.ID] = g
		}
	}

	addr, err := rdr.AddrFor("runtime.allglen", exeimage.StaticBase)
	if err != nil {
		return nil, -1, err
	}
	allglenBytes := make([]byte, 8)
	_, err = mem.ReadMemory(allglenBytes, uintptr(addr))
	if err != nil {
		return nil, -1, err
	}
	allglen := binary.LittleEndian.Uint64(allglenBytes)

	rdr.Seek(0)
	allgentryaddr, err := rdr.AddrFor("runtime.allgs", exeimage.StaticBase)
	if err != nil {
		// try old name (pre Go 1.6)
		allgentryaddr, err = rdr.AddrFor("runtime.allg", exeimage.StaticBase)
		if err != nil {
			return nil, -1, err
		}
	}
	faddr := make([]byte, dbp.BinInfo().Arch.PtrSize())
	_, err = mem.ReadMemory(faddr, uintptr(allgentryaddr))
	if err != nil {
		return nil, -1, err
	}
	allgptr := binary.LittleEndian.Uint64(faddr)

	for i := uint64(start); i < allglen; i++ {
		if count != 0 && len(allg) >= count {
			return allg, int(i), nil
		}
		gvar, err := newGVariable(mem, dbp.BinInfo(), uintptr(allgptr+(i*uint64(dbp.BinInfo().Arch.PtrSize()))), true)
		if err != nil {
			allg = append(allg, &G{Unreadable: err})
			continue
		}
		g, err := gvar.parseG()
		if err != nil {
			allg = append(allg, &G{Unreadable: err})
			continue
		}
		if thg, allocated := threadg[g.ID]; allocated {
			loc, err := thg.Thread.Location()
			if err != nil {
				return nil, -1, err
			}
			g.Thread = thg.Thread
			// Prefer actual thread location information.
			g.CurrentLoc = *loc
			g.SystemStack = thg.SystemStack
		}
		if g.Status != Gdead {
			allg = append(allg, g)
		}
	}
	if start == 0 {
		dbp.Common().allGCache = allg
	}

	return allg, -1, nil
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
