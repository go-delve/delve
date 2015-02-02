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
	"time"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/dwarf/frame"
	"github.com/derekparker/delve/dwarf/reader"
)

// Struct representing a debugged process. Holds onto pid, register values,
// process struct and process state.
type DebuggedProcess struct {
	Pid                 int
	Process             *os.Process
	Dwarf               *dwarf.Data
	GoSymTable          *gosym.Table
	FrameEntries        *frame.FrameDescriptionEntries
	HWBreakPoints       [4]*BreakPoint
	BreakPoints         map[uint64]*BreakPoint
	Threads             map[int]*ThreadContext
	CurrentThread       *ThreadContext
	breakpointIDCounter int
	running             bool
	halt                bool
}

// A ManualStopError happens when the user triggers a
// manual stop via SIGERM.
type ManualStopError struct{}

func (mse ManualStopError) Error() string {
	return "Manual stop requested"
}

// Attach to an existing process with the given PID.
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

// Attach to a newly created thread, and store that thread in our list of
// known threads.
func (dbp *DebuggedProcess) AttachThread(tid int) (*ThreadContext, error) {
	if thread, ok := dbp.Threads[tid]; ok {
		return thread, nil
	}

	err := sys.PtraceAttach(tid)
	if err != nil && err != sys.EPERM {
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

// Returns whether or not Delve thinks the debugged
// process is currently executing.
func (dbp *DebuggedProcess) Running() bool {
	return dbp.running
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
}

// Sends out a request that the debugged process halt
// execution. Sends SIGSTOP to all threads.
func (dbp *DebuggedProcess) RequestManualStop() {
	dbp.halt = true
	for _, th := range dbp.Threads {
		if stopped(th.Id) {
			continue
		}
		sys.Tgkill(dbp.Pid, th.Id, sys.SIGSTOP)
	}
	dbp.running = false
}

// Sets a breakpoint, adding it to our list of known breakpoints. Uses
// the "current thread" when setting the breakpoint.
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
func (dbp *DebuggedProcess) Status() *sys.WaitStatus {
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

	fn := func() error {
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

	return dbp.run(fn)
}

// Step over function calls.
func (dbp *DebuggedProcess) Next() error {
	var (
		th *ThreadContext
		ok bool
	)

	fn := func() error {
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
			if err != nil && err != sys.ESRCH {
				return err
			}
		}
		return stopTheWorld(dbp)
	}
	return dbp.run(fn)
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
		wpid, _, err := trapWait(dbp, -1)
		if err != nil {
			return err
		}
		return handleBreakPoint(dbp, wpid)
	}
	return dbp.run(fn)
}

// Obtains register values from what Delve considers to be the current
// thread of the traced process.
func (dbp *DebuggedProcess) Registers() (Registers, error) {
	return dbp.CurrentThread.Registers()
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

func (dbp *DebuggedProcess) run(fn func() error) error {
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

type ProcessExitedError struct {
	pid int
}

func (pe ProcessExitedError) Error() string {
	return fmt.Sprintf("process %d has exited", pe.pid)
}

func trapWait(dbp *DebuggedProcess, pid int) (int, *sys.WaitStatus, error) {
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
		if status.StopSignal() == sys.SIGTRAP && status.TrapCause() == sys.PTRACE_EVENT_CLONE {
			err = addNewThread(dbp, wpid)
			if err != nil {
				return -1, nil, err
			}
			continue
		}
		if status.StopSignal() == sys.SIGTRAP {
			return wpid, status, nil
		}
		if status.StopSignal() == sys.SIGSTOP && dbp.halt {
			return -1, nil, ManualStopError{}
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

	// Check for hardware breakpoint
	for _, bp := range dbp.HWBreakPoints {
		if bp.Addr == pc {
			if !bp.temp {
				stopTheWorld(dbp)
			}
			return nil
		}
	}
	// Check to see if we have hit a software breakpoint.
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
		if stopped(th.Id) {
			continue
		}
		err := sys.Tgkill(dbp.Pid, th.Id, sys.SIGSTOP)
		if err != nil {
			return err
		}
		pid, _, err := wait(th.Id, sys.WNOHANG)
		if err != nil {
			return fmt.Errorf("wait err %s %d", err, pid)
		}
	}

	return nil
}

func addNewThread(dbp *DebuggedProcess, pid int) error {
	// A traced thread has cloned a new thread, grab the pid and
	// add it to our list of traced threads.
	msg, err := sys.PtraceGetEventMsg(pid)
	if err != nil {
		return fmt.Errorf("could not get event message: %s", err)
	}
	fmt.Println("new thread spawned", msg)

	_, err = dbp.addThread(int(msg))
	if err != nil {
		return err
	}

	err = sys.PtraceCont(int(msg), 0)
	if err != nil {
		return fmt.Errorf("could not continue new thread %d %s", msg, err)
	}

	// Here we loop for a while to ensure that the once we continue
	// the newly created thread, we allow enough time for the runtime
	// to assign m->procid. This is important because we rely on
	// looping through runtime.allm in other parts of the code, so
	// we require that this is set before we do anything else.
	// TODO(dp): we might be able to eliminate this loop by telling
	// the CPU to emit a breakpoint exception on write to this location
	// in memory. That way we prevent having to loop, and can be
	// notified as soon as m->procid is set.
	th := dbp.Threads[pid]
	for {
		allm, _ := th.AllM()
		for _, m := range allm {
			if m.procid == int(msg) {
				// Continue the thread that cloned
				return sys.PtraceCont(pid, 0)
			}
		}
		time.Sleep(time.Millisecond)
	}
}

func wait(pid, options int) (int, *sys.WaitStatus, error) {
	var status sys.WaitStatus
	wpid, err := sys.Wait4(pid, &status, sys.WALL|options, nil)
	return wpid, &status, err
}
