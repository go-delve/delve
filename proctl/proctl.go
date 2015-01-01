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
	"syscall"

	"github.com/derekparker/delve/dwarf/frame"
	"github.com/derekparker/delve/dwarf/reader"
)

// Struct representing a debugged process. Holds onto pid, register values,
// process struct and process state.
type DebuggedProcess struct {
	Pid           int
	Process       *os.Process
	Dwarf         *dwarf.Data
	GoSymTable    *gosym.Table
	FrameEntries  *frame.FrameDescriptionEntries
	BreakPoints   map[uint64]*BreakPoint
	Threads       map[int]*ThreadContext
	CurrentThread *ThreadContext
}

// Represents a single breakpoint. Stores information on the break
// point including the byte of data that originally was stored at that
// address.
type BreakPoint struct {
	FunctionName string
	File         string
	Line         int
	Addr         uint64
	OriginalData []byte
	ID           int
	temp         bool
}

type BreakPointExistsError struct {
	file string
	line int
	addr uint64
}

// ProcessStatus is the result of parsing the data from
// the /proc/<pid>/stats psuedo file.
type ProcessStatus struct {
	pid   int
	comm  string
	state rune
	ppid  int
}

const (
	STATUS_SLEEPING   = 'S'
	STATUS_RUNNING    = 'R'
	STATUS_TRACE_STOP = 't'
)

var (
	breakpointIDCounter = 0
)

func (bpe BreakPointExistsError) Error() string {
	return fmt.Sprintf("Breakpoint exists at %s:%d at %x", bpe.file, bpe.line, bpe.addr)
}

func Attach(pid int) (*DebuggedProcess, error) {
	dbp, err := newDebugProcess(pid, true)
	if err != nil {
		return nil, err
	}
	// Attach to all currently active threads.
	allm, err := dbp.CurrentThread.AllM()
	if err != nil {
		return nil, err
	}
	for _, m := range allm {
		if m.procid == 0 {
			continue
		}
		_, err := dbp.AttachThread(m.procid)
		if err != nil {
			return nil, err
		}
	}
	return dbp, nil
}

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

// Returns a new DebuggedProcess struct with sensible defaults.
func newDebugProcess(pid int, attach bool) (*DebuggedProcess, error) {
	dbp := DebuggedProcess{
		Pid:         pid,
		Threads:     make(map[int]*ThreadContext),
		BreakPoints: make(map[uint64]*BreakPoint),
	}

	if attach {
		thread, err := dbp.AttachThread(pid)
		if err != nil {
			return nil, err
		}
		dbp.CurrentThread = thread
	} else {
		thread, err := dbp.addThread(pid)
		if err != nil {
			return nil, err
		}
		dbp.CurrentThread = thread
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

	return &dbp, nil
}

func (dbp *DebuggedProcess) AttachThread(tid int) (*ThreadContext, error) {
	if thread, ok := dbp.Threads[tid]; ok {
		return thread, nil
	}

	err := syscall.PtraceAttach(tid)
	if err != nil && err != syscall.EPERM {
		// Do not return err if err == EPERM,
		// we may already be tracing this thread due to
		// PTRACE_O_TRACECLONE. We will surely blow up later
		// if we truly don't have permissions.
		return nil, fmt.Errorf("could not attach to new thread %d %s", tid, err)
	}

	pid, status, err := wait(tid, 0)
	if err != nil {
		return nil, err
	}

	if status.Exited() {
		return nil, fmt.Errorf("thread already exited %d", pid)
	}

	return dbp.addThread(tid)
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
	} else {
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
		for _, bp := range dbp.BreakPoints {
			// ID
			if uint64(bp.ID) == id {
				return bp.Addr, nil
			}
		}

		// Last resort, use as raw address
		return id, nil
	}
}

// Sets a breakpoint in the current thread.
func (dbp *DebuggedProcess) Break(addr uint64) (*BreakPoint, error) {
	return dbp.CurrentThread.Break(addr)
}

// Sets a breakpoint by location string (function, file+line, address)
func (dbp *DebuggedProcess) BreakByLocation(loc string) (*BreakPoint, error) {
	addr, err := dbp.FindLocation(loc)
	if err != nil {
		return nil, err
	}
	return dbp.CurrentThread.Break(addr)
}

// Clears a breakpoint in the current thread.
func (dbp *DebuggedProcess) Clear(addr uint64) (*BreakPoint, error) {
	return dbp.CurrentThread.Clear(addr)
}

// Clears a breakpoint by location (function, file+line, address, breakpoint id)
func (dbp *DebuggedProcess) ClearByLocation(loc string) (*BreakPoint, error) {
	addr, err := dbp.FindLocation(loc)
	if err != nil {
		return nil, err
	}
	return dbp.CurrentThread.Clear(addr)
}

// Returns the status of the current main thread context.
func (dbp *DebuggedProcess) Status() *syscall.WaitStatus {
	return dbp.CurrentThread.Status
}

// Loop through all threads, printing their information
// to the console.
func (dbp *DebuggedProcess) PrintThreadInfo() error {
	for _, th := range dbp.Threads {
		if err := th.PrintInfo(); err != nil {
			return err
		}
	}
	return nil
}

// Steps through process.
func (dbp *DebuggedProcess) Step() (err error) {
	var (
		th *ThreadContext
		ok bool
	)

	allm, err := dbp.CurrentThread.AllM()
	if err != nil {
		return err
	}

	for _, m := range allm {
		th, ok = dbp.Threads[m.procid]
		if !ok {
			th = dbp.Threads[dbp.Pid]
		}

		if m.blocked == 0 {
			err := th.Step()
			if err != nil {
				return err
			}
		}

	}

	return nil
}

// Step over function calls.
func (dbp *DebuggedProcess) Next() error {
	var (
		th *ThreadContext
		ok bool
	)

	allm, err := dbp.CurrentThread.AllM()
	if err != nil {
		return err
	}

	for _, m := range allm {
		th, ok = dbp.Threads[m.procid]
		if !ok {
			th = dbp.Threads[dbp.Pid]
		}

		if m.blocked == 1 {
			// Continue any blocked M so that the
			// scheduler can continue to do its'
			// job correctly.
			err := th.Continue()
			if err != nil {
				return err
			}
			continue
		}

		err := th.Next()
		if err != nil && err != syscall.ESRCH {
			return err
		}
	}
	return stopTheWorld(dbp)
}

// Resume process.
func (dbp *DebuggedProcess) Continue() error {
	for _, thread := range dbp.Threads {
		err := thread.Continue()
		if err != nil {
			return err
		}
	}

	wpid, _, err := trapWait(dbp, -1, 0)
	if err != nil {
		return err
	}
	return handleBreakPoint(dbp, wpid)
}

// Obtains register values from what Delve considers to be the current
// thread of the traced process.
func (dbp *DebuggedProcess) Registers() (Registers, error) {
	return dbp.CurrentThread.Registers()
}

type InvalidAddressError struct {
	address uint64
}

func (iae InvalidAddressError) Error() string {
	return fmt.Sprintf("Invalid address %#v\n", iae.address)
}

func (dbp *DebuggedProcess) CurrentPC() (uint64, error) {
	return dbp.CurrentThread.CurrentPC()
}

// Returns the value of the named symbol.
func (dbp *DebuggedProcess) EvalSymbol(name string) (*Variable, error) {
	return dbp.CurrentThread.EvalSymbol(name)
}

// Returns a reader for the dwarf data
func (dbp *DebuggedProcess) DwarfReader() *reader.Reader {
	return reader.New(dbp.Dwarf)
}

type ProcessExitedError struct {
	pid int
}

func (pe ProcessExitedError) Error() string {
	return fmt.Sprintf("process %d has exited", pe.pid)
}

func trapWait(dbp *DebuggedProcess, pid int, options int) (int, *syscall.WaitStatus, error) {
	for {
		wpid, status, err := wait(pid, 0)
		if err != nil {
			return -1, nil, fmt.Errorf("wait err %s %d", err, pid)
		}
		if wpid == 0 {
			continue
		}
		if th, ok := dbp.Threads[wpid]; ok {
			th.Status = status
		}
		if status.Exited() && wpid == dbp.Pid {
			return -1, status, ProcessExitedError{wpid}
		}
		if status.StopSignal() == syscall.SIGTRAP && status.TrapCause() == syscall.PTRACE_EVENT_CLONE {
			err = addNewThread(dbp, wpid)
			if err != nil {
				return -1, nil, err
			}
			continue
		}
		if status.StopSignal() == syscall.SIGTRAP {
			return wpid, status, nil
		}
	}
}

func handleBreakPoint(dbp *DebuggedProcess, pid int) error {
	thread := dbp.Threads[pid]
	if pid != dbp.CurrentThread.Id {
		fmt.Printf("thread context changed from %d to %d\n", dbp.CurrentThread.Id, pid)
		dbp.CurrentThread = thread
	}

	pc, err := thread.CurrentPC()
	if err != nil {
		return fmt.Errorf("could not get current pc %s", err)
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
		stopTheWorld(dbp)
		return nil
	}

	// Check to see if we have hit a user set breakpoint.
	if bp, ok := dbp.BreakPoints[pc-1]; ok {
		if !bp.temp {
			stopTheWorld(dbp)
		}
		return nil
	}

	return fmt.Errorf("did not hit recognized breakpoint")
}

// Ensure execution of every traced thread is halted.
func stopTheWorld(dbp *DebuggedProcess) error {
	// Loop through all threads and ensure that we
	// stop the rest of them, so that by the time
	// we return control to the user, all threads
	// are inactive. We send SIGSTOP and ensure all
	// threads are in in signal-delivery-stop mode.
	for _, th := range dbp.Threads {
		ps, err := parseProcessStatus(th.Id)
		if err != nil {
			return err
		}

		if ps.state == STATUS_TRACE_STOP {
			continue
		}

		err = syscall.Tgkill(dbp.Pid, th.Id, syscall.SIGSTOP)
		if err != nil {
			return err
		}

		pid, _, err := wait(th.Id, 0)
		if err != nil {
			return fmt.Errorf("wait err %s %d", err, pid)
		}
	}

	return nil
}

func addNewThread(dbp *DebuggedProcess, pid int) error {
	// A traced thread has cloned a new thread, grab the pid and
	// add it to our list of traced threads.
	msg, err := syscall.PtraceGetEventMsg(pid)
	if err != nil {
		return fmt.Errorf("could not get event message: %s", err)
	}
	fmt.Println("new thread spawned", msg)

	_, err = dbp.addThread(int(msg))
	if err != nil {
		return err
	}

	err = syscall.PtraceCont(int(msg), 0)
	if err != nil {
		return fmt.Errorf("could not continue new thread %d %s", msg, err)
	}

	err = syscall.PtraceCont(pid, 0)
	if err != nil {
		return fmt.Errorf("could not continue stopped thread %d %s", pid, err)
	}

	return nil
}

func wait(pid, options int) (int, *syscall.WaitStatus, error) {
	var status syscall.WaitStatus
	wpid, err := syscall.Wait4(pid, &status, syscall.WALL|options, nil)
	return wpid, &status, err
}
