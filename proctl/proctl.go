// Package proctl provides functions for attaching to and manipulating
// a process during the debug session.
package proctl

import (
	"debug/dwarf"
	"debug/gosym"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/dwarf/frame"
	"github.com/derekparker/delve/dwarf/line"
	"github.com/derekparker/delve/dwarf/reader"
	"github.com/derekparker/delve/source"
)

// Struct representing a debugged process. Holds onto pid, register values,
// process struct and process state.
type DebuggedProcess struct {
	Pid                 int
	Process             *os.Process
	Dwarf               *dwarf.Data
	GoSymTable          *gosym.Table
	FrameEntries        frame.FrameDescriptionEntries
	LineInfo            *line.DebugLineInfo
	HWBreakPoints       [4]*BreakPoint
	BreakPoints         map[uint64]*BreakPoint
	Threads             map[int]*ThreadContext
	CurrentThread       *ThreadContext
	os                  *OSProcessDetails
	ast                 *source.Searcher
	breakpointIDCounter int
	running             bool
	halt                bool
	exited              bool
}

// A ManualStopError happens when the user triggers a
// manual stop via SIGERM.
type ManualStopError struct{}

func (mse ManualStopError) Error() string {
	return "Manual stop requested"
}

// ProcessExitedError indicates that the process has exited and contains both
// process id and exit status.
type ProcessExitedError struct {
	Pid    int
	Status int
}

func (pe ProcessExitedError) Error() string {
	return fmt.Sprintf("process %d has exited with status %d", pe.Pid, pe.Status)
}

// Attach to an existing process with the given PID.
func Attach(pid int) (*DebuggedProcess, error) {
	dbp, err := newDebugProcess(pid, true)
	if err != nil {
		return nil, err
	}
	return dbp, nil
}

// Create and begin debugging a new process. First entry in
// `cmd` is the program to run, and then rest are the arguments
// to be supplied to that process.
func Launch(cmd []string) (*DebuggedProcess, error) {
	proc := exec.Command(cmd[0])
	proc.Args = cmd
	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr
	proc.SysProcAttr = &syscall.SysProcAttr{Ptrace: true}

	if err := proc.Start(); err != nil {
		return nil, err
	}

	_, _, err := wait(proc.Process.Pid, 0)
	if err != nil {
		return nil, fmt.Errorf("waiting for target execve failed: %s", err)
	}

	return newDebugProcess(proc.Process.Pid, false)
}

// Returns whether or not Delve thinks the debugged
// process has exited.
func (dbp *DebuggedProcess) Exited() bool {
	return dbp.exited
}

// Returns whether or not Delve thinks the debugged
// process is currently executing.
func (dbp *DebuggedProcess) Running() bool {
	return dbp.running
}

// Finds the executable and then uses it
// to parse the following information:
// * Dwarf .debug_frame section
// * Dwarf .debug_line section
// * Go symbol table.
func (dbp *DebuggedProcess) LoadInformation() error {
	var wg sync.WaitGroup

	exe, err := dbp.findExecutable()
	if err != nil {
		return err
	}

	wg.Add(3)
	go dbp.parseDebugFrame(exe, &wg)
	go dbp.obtainGoSymbols(exe, &wg)
	go dbp.parseDebugLineInfo(exe, &wg)
	wg.Wait()

	return nil
}

// Find a location by string (file+line, function, breakpoint id, addr)
func (dbp *DebuggedProcess) FindLocation(str string) (uint64, error) {
	// File + Line
	if strings.ContainsRune(str, ':') {
		fl := strings.Split(str, ":")

		fileName, err := filepath.Abs(fl[0])
		if err != nil {
			return 0, err
		}

		line, err := strconv.Atoi(fl[1])
		if err != nil {
			return 0, err
		}

		pc, _, err := dbp.GoSymTable.LineToPC(fileName, line)
		if err != nil {
			return 0, err
		}
		return pc, nil
	}

	// Try to lookup by function name
	fn := dbp.GoSymTable.LookupFunc(str)
	if fn != nil {
		return fn.Entry, nil
	}

	// Attempt to parse as number for breakpoint id or raw address
	id, err := strconv.ParseUint(str, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("unable to find location for %s", str)
	}

	// Use as breakpoint id
	for _, bp := range dbp.HWBreakPoints {
		if bp == nil {
			continue
		}
		if uint64(bp.ID) == id {
			return bp.Addr, nil
		}
	}
	for _, bp := range dbp.BreakPoints {
		if uint64(bp.ID) == id {
			return bp.Addr, nil
		}
	}

	// Last resort, use as raw address
	return id, nil
}

// Sends out a request that the debugged process halt
// execution. Sends SIGSTOP to all threads.
func (dbp *DebuggedProcess) RequestManualStop() {
	dbp.halt = true
	for _, th := range dbp.Threads {
		th.Halt()
	}
	dbp.running = false
}

// Sets a breakpoint at addr, and stores it in the process wide
// break point table. Setting a break point must be thread specific due to
// ptrace actions needing the thread to be in a signal-delivery-stop.
//
// Depending on hardware support, Delve will choose to either
// set a hardware or software breakpoint. Essentially, if the
// hardware supports it, and there are free debug registers, Delve
// will set a hardware breakpoint. Otherwise we fall back to software
// breakpoints, which are a bit more work for us.
func (dbp *DebuggedProcess) Break(addr uint64) (*BreakPoint, error) {
	return dbp.setBreakpoint(dbp.CurrentThread.Id, addr)
}

// Sets a breakpoint by location string (function, file+line, address)
func (dbp *DebuggedProcess) BreakByLocation(loc string) (*BreakPoint, error) {
	addr, err := dbp.FindLocation(loc)
	if err != nil {
		return nil, err
	}
	return dbp.Break(addr)
}

// Clears a breakpoint in the current thread.
func (dbp *DebuggedProcess) Clear(addr uint64) (*BreakPoint, error) {
	tid := dbp.CurrentThread.Id
	// Check for hardware breakpoint
	for i, bp := range dbp.HWBreakPoints {
		if bp == nil {
			continue
		}
		if bp.Addr == addr {
			dbp.HWBreakPoints[i] = nil
			if err := clearHardwareBreakpoint(i, tid); err != nil {
				return nil, err
			}
			return bp, nil
		}
	}
	// Check for software breakpoint
	if bp, ok := dbp.BreakPoints[addr]; ok {
		thread := dbp.Threads[tid]
		if _, err := writeMemory(thread, uintptr(bp.Addr), bp.OriginalData); err != nil {
			return nil, fmt.Errorf("could not clear breakpoint %s", err)
		}
		delete(dbp.BreakPoints, addr)
		return bp, nil
	}
	return nil, fmt.Errorf("no breakpoint at %#v", addr)
}

// Clears a breakpoint by location (function, file+line, address, breakpoint id)
func (dbp *DebuggedProcess) ClearByLocation(loc string) (*BreakPoint, error) {
	addr, err := dbp.FindLocation(loc)
	if err != nil {
		return nil, err
	}
	return dbp.Clear(addr)
}

// Returns the status of the current main thread context.
func (dbp *DebuggedProcess) Status() *sys.WaitStatus {
	return dbp.CurrentThread.Status
}

// Step over function calls.
func (dbp *DebuggedProcess) Next() error {
	return dbp.run(dbp.next)
}

func (dbp *DebuggedProcess) next() error {
	curg, err := dbp.CurrentThread.curG()
	if err != nil {
		return err
	}
	defer dbp.clearTempBreakpoints()
	for _, th := range dbp.Threads {
		if th.blocked() { // Continue threads that aren't running go code.
			if err := th.Continue(); err != nil {
				return err
			}
			continue
		}
		if err := th.Next(); err != nil {
			return err
		}
	}

	for {
		tid, err := trapWait(dbp, -1)
		if err != nil {
			return err
		}
		th, ok := dbp.Threads[tid]
		if !ok {
			return fmt.Errorf("unknown thread %d", tid)
		}
		pc, err := th.CurrentPC()
		if err != nil {
			return err
		}
		// Check if we've hit a software breakpoint. If so, reset PC.
		if err = th.clearTempBreakpoint(pc - 1); err != nil {
			return err
		}
		// Grab the current goroutine for this thread.
		tg, err := th.curG()
		if err != nil {
			return err
		}
		// Make sure we're on the same goroutine.
		// TODO(dp) take into account goroutine exit.
		if tg.id == curg.id {
			if dbp.CurrentThread.Id != tid {
				dbp.SwitchThread(tid)
			}
			break
		}
	}
	return dbp.Halt()
}

// Resume process.
func (dbp *DebuggedProcess) Continue() error {
	for _, thread := range dbp.Threads {
		err := thread.Continue()
		if err != nil {
			return err
		}
	}

	fn := func() error {
		wpid, err := trapWait(dbp, -1)
		if err != nil {
			return err
		}

		thread, ok := dbp.Threads[wpid]
		if !ok {
			return fmt.Errorf("could not find thread for %d", wpid)
		}

		if wpid != dbp.CurrentThread.Id {
			dbp.SwitchThread(wpid)
		}

		pc, err := thread.CurrentPC()
		if err != nil {
			return err
		}

		// Check to see if we hit a runtime.breakpoint
		fn := dbp.GoSymTable.PCToFunc(pc)
		if fn != nil && fn.Name == "runtime.breakpoint" {
			// step twice to get back to user code
			for i := 0; i < 2; i++ {
				err = thread.Step()
				if err != nil {
					return err
				}
			}
			dbp.Halt()
			return nil
		}

		// Check for hardware breakpoint
		for _, bp := range dbp.HWBreakPoints {
			if bp != nil && bp.Addr == pc {
				if !bp.Temp {
					return dbp.Halt()
				}
				return nil
			}
		}
		// Check to see if we have hit a software breakpoint.
		if bp, ok := dbp.BreakPoints[pc-1]; ok {
			if !bp.Temp {
				return dbp.Halt()
			}
			return nil
		}

		return fmt.Errorf("unrecognized breakpoint %#v", pc)
	}
	return dbp.run(fn)
}

// Steps through process.
func (dbp *DebuggedProcess) Step() (err error) {
	fn := func() error {
		for _, th := range dbp.Threads {
			if th.blocked() {
				continue
			}
			err := th.Step()
			if err != nil {
				return err
			}
		}
		return nil
	}

	return dbp.run(fn)
}

// Change from current thread to the thread specified by `tid`.
func (dbp *DebuggedProcess) SwitchThread(tid int) error {
	if th, ok := dbp.Threads[tid]; ok {
		dbp.CurrentThread = th
		fmt.Printf("thread context changed from %d to %d\n", dbp.CurrentThread.Id, tid)
		return nil
	}
	return fmt.Errorf("thread %d does not exist", tid)
}

// Obtains register values from what Delve considers to be the current
// thread of the traced process.
func (dbp *DebuggedProcess) Registers() (Registers, error) {
	return dbp.CurrentThread.Registers()
}

// Returns the PC of the current thread.
func (dbp *DebuggedProcess) CurrentPC() (uint64, error) {
	return dbp.CurrentThread.CurrentPC()
}

// Returns the value of the named symbol.
func (dbp *DebuggedProcess) EvalSymbol(name string) (*Variable, error) {
	return dbp.CurrentThread.EvalSymbol(name)
}

func (dbp *DebuggedProcess) CallFn(name string, fn func(*ThreadContext) error) error {
	return dbp.CurrentThread.CallFn(name, fn)
}

// Returns a reader for the dwarf data
func (dbp *DebuggedProcess) DwarfReader() *reader.Reader {
	return reader.New(dbp.Dwarf)
}

// Finds the breakpoint for the given pc.
func (dbp *DebuggedProcess) FindBreakpoint(pc uint64) (*BreakPoint, bool) {
	for _, bp := range dbp.HWBreakPoints {
		if bp != nil && bp.Addr == pc {
			return bp, true
		}
	}
	if bp, ok := dbp.BreakPoints[pc]; ok {
		return bp, true
	}
	return nil, false
}

// Returns a new DebuggedProcess struct.
func newDebugProcess(pid int, attach bool) (*DebuggedProcess, error) {
	dbp := DebuggedProcess{
		Pid:         pid,
		Threads:     make(map[int]*ThreadContext),
		BreakPoints: make(map[uint64]*BreakPoint),
		os:          new(OSProcessDetails),
		ast:         source.New(),
	}

	if attach {
		err := sys.PtraceAttach(pid)
		if err != nil {
			return nil, err
		}
		_, _, err = wait(pid, 0)
		if err != nil {
			return nil, err
		}
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, err
	}

	dbp.Process = proc
	err = dbp.LoadInformation()
	if err != nil {
		return nil, err
	}

	if err := dbp.updateThreadList(); err != nil {
		return nil, err
	}

	return &dbp, nil
}
func (dbp *DebuggedProcess) clearTempBreakpoints() error {
	for _, bp := range dbp.HWBreakPoints {
		if bp != nil && bp.Temp {
			if _, err := dbp.Clear(bp.Addr); err != nil {
				return err
			}
		}
	}
	for _, bp := range dbp.BreakPoints {
		if !bp.Temp {
			continue
		}
		if _, err := dbp.Clear(bp.Addr); err != nil {
			return err
		}
	}
	return nil
}

func (dbp *DebuggedProcess) run(fn func() error) error {
	if dbp.exited {
		return fmt.Errorf("process has already exited")
	}
	dbp.running = true
	dbp.halt = false
	defer func() { dbp.running = false }()
	if err := fn(); err != nil {
		if _, ok := err.(ManualStopError); !ok {
			return err
		}
	}
	return nil
}
