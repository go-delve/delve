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
func GoroutinesInfo(dbp Process, start, count int) ([]*G, int, error) {
	if _, err := dbp.Valid(); err != nil {
		return nil, -1, err
	}
	if dbp.Common().allGCache != nil {
		// We can't use the cached array to fulfill a subrange request
		if start == 0 && (count == 0 || count >= len(dbp.Common().allGCache)) {
			return dbp.Common().allGCache, -1, nil
		}
	}

	exeimage := dbp.BinInfo().Images[0] // Image corresponding to the executable file

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
		g, _ := GetG(th)
		if g != nil {
			threadg[g.ID] = g
		}
	}

	addr, err := rdr.AddrFor("runtime.allglen", exeimage.StaticBase)
	if err != nil {
		return nil, -1, err
	}
	allglenBytes := make([]byte, 8)
	_, err = dbp.CurrentThread().ReadMemory(allglenBytes, uintptr(addr))
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
	_, err = dbp.CurrentThread().ReadMemory(faddr, uintptr(allgentryaddr))
	if err != nil {
		return nil, -1, err
	}
	allgptr := binary.LittleEndian.Uint64(faddr)

	for i := uint64(start); i < allglen; i++ {
		if count != 0 && len(allg) >= count {
			return allg, int(i), nil
		}
		gvar, err := newGVariable(dbp.CurrentThread(), uintptr(allgptr+(i*uint64(dbp.BinInfo().Arch.PtrSize()))), true)
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

// FindGoroutine returns a G struct representing the goroutine
// specified by `gid`.
func FindGoroutine(dbp Process, gid int) (*G, error) {
	if selg := dbp.SelectedGoroutine(); (gid == -1) || (selg != nil && selg.ID == gid) || (selg == nil && gid == 0) {
		// Return the currently selected goroutine in the following circumstances:
		//
		// 1. if the caller asks for gid == -1 (because that's what a goroutine ID of -1 means in our API).
		// 2. if gid == selg.ID.
		//    this serves two purposes: (a) it's an optimizations that allows us
		//    to avoid reading any other goroutine and, more importantly, (b) we
		//    could be reading an incorrect value for the goroutine ID of a thread.
		//    This condition usually happens when a goroutine calls runtime.clone
		//    and for a short period of time two threads will appear to be running
		//    the same goroutine.
		// 3. if the caller asks for gid == 0 and the selected goroutine is
		//    either 0 or nil.
		//    Goroutine 0 is special, it either means we have no current goroutine
		//    (for example, running C code), or that we are running on a speical
		//    stack (system stack, signal handling stack) and we didn't properly
		//    detect it.
		//    Since there could be multiple goroutines '0' running simultaneously
		//    if the user requests it return the one that's already selected or
		//    nil if there isn't a selected goroutine.
		return selg, nil
	}

	if gid == 0 {
		return nil, fmt.Errorf("Unknown goroutine %d", gid)
	}

	// Calling GoroutinesInfo could be slow if there are many goroutines
	// running, check if a running goroutine has been requested first.
	for _, thread := range dbp.ThreadList() {
		g, _ := GetG(thread)
		if g != nil && g.ID == gid {
			return g, nil
		}
	}

	const goroutinesInfoLimit = 10
	nextg := 0
	for nextg >= 0 {
		var gs []*G
		var err error
		gs, nextg, err = GoroutinesInfo(dbp, nextg, goroutinesInfoLimit)
		if err != nil {
			return nil, err
		}
		for i := range gs {
			if gs[i].ID == gid {
				if gs[i].Unreadable != nil {
					return nil, gs[i].Unreadable
				}
				return gs[i], nil
			}
		}
	}

	return nil, fmt.Errorf("Unknown goroutine %d", gid)
}

// FirstPCAfterPrologue returns the address of the first
// instruction after the prologue for function fn.
// If sameline is set FirstPCAfterPrologue will always return an
// address associated with the same line as fn.Entry.
func FirstPCAfterPrologue(p Process, fn *Function, sameline bool) (uint64, error) {
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

	pc, err := firstPCAfterPrologueDisassembly(p, fn, sameline)
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
