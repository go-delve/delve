// Package proctl provides functions for attaching to and manipulating
// a process during the debug session.
package proctl

import (
	"debug/gosym"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/derekparker/delve/dwarf/frame"
	"github.com/derekparker/delve/vendor/elf"
)

// Struct representing a debugged process. Holds onto pid, register values,
// process struct and process state.
type DebuggedProcess struct {
	Pid           int
	Process       *os.Process
	Executable    *elf.File
	Symbols       []elf.Symbol
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
	temp         bool
}

type BreakPointExistsError struct {
	file string
	line int
	addr uintptr
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

func parseProcessStatus(pid int) (*ProcessStatus, error) {
	var ps ProcessStatus

	f, err := os.Open(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fmt.Fscanf(f, "%d %s %c %d", &ps.pid, &ps.comm, &ps.state, &ps.ppid)

	return &ps, nil
}

func (bpe BreakPointExistsError) Error() string {
	return fmt.Sprintf("Breakpoint exists at %s:%d at %x", bpe.file, bpe.line, bpe.addr)
}

func AttachBinary(name string) (*DebuggedProcess, error) {
	proc := exec.Command(name)
	proc.Stdout = os.Stdout

	err := proc.Start()
	if err != nil {
		return nil, err
	}

	dbgproc, err := NewDebugProcess(proc.Process.Pid)
	if err != nil {
		return nil, err
	}

	return dbgproc, nil
}

// Returns a new DebuggedProcess struct with sensible defaults.
func NewDebugProcess(pid int) (*DebuggedProcess, error) {
	dbp := DebuggedProcess{
		Pid:         pid,
		Threads:     make(map[int]*ThreadContext),
		BreakPoints: make(map[uint64]*BreakPoint),
	}

	thread, err := dbp.AttachThread(pid)
	if err != nil {
		return nil, err
	}
	dbp.CurrentThread = thread

	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, err
	}

	dbp.Process = proc
	err = dbp.LoadInformation()
	if err != nil {
		return nil, err
	}

	// Attach to all currently active threads.
	for _, tid := range threadIds(pid) {
		_, err := dbp.AttachThread(tid)
		if err != nil {
			return nil, err
		}
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

	var status syscall.WaitStatus
	pid, e := syscall.Wait4(tid, &status, syscall.WALL, nil)
	if e != nil {
		return nil, err
	}

	if status.Exited() {
		return nil, fmt.Errorf("thread already exited %d", pid)
	}

	return dbp.addThread(tid)
}

func (dbp *DebuggedProcess) addThread(tid int) (*ThreadContext, error) {
	err := syscall.PtraceSetOptions(tid, syscall.PTRACE_O_TRACECLONE)
	if err == syscall.ESRCH {
		_, err = syscall.Wait4(tid, nil, syscall.WALL, nil)
		if err != nil {
			return nil, fmt.Errorf("error while waiting after adding thread: %d %s", tid, err)
		}

		err := syscall.PtraceSetOptions(tid, syscall.PTRACE_O_TRACECLONE)
		if err != nil {
			return nil, fmt.Errorf("could not set options for new traced thread %d %s", tid, err)
		}
	}

	dbp.Threads[tid] = &ThreadContext{
		Id:      tid,
		Process: dbp,
		Regs:    new(syscall.PtraceRegs),
	}

	return dbp.Threads[tid], nil
}

// Sets a breakpoint in the current thread.
func (dbp *DebuggedProcess) Break(addr uintptr) (*BreakPoint, error) {
	return dbp.CurrentThread.Break(addr)
}

// Clears a breakpoint in the current thread.
func (dbp *DebuggedProcess) Clear(pc uint64) (*BreakPoint, error) {
	return dbp.CurrentThread.Clear(pc)
}

// Returns the status of the current main thread context.
func (dbp *DebuggedProcess) Status() *syscall.WaitStatus {
	return dbp.CurrentThread.Status
}

// Finds the executable from /proc/<pid>/exe and then
// uses that to parse the following information:
// * Dwarf .debug_frame section
// * Dwarf .debug_line section
// * Go symbol table.
func (dbp *DebuggedProcess) LoadInformation() error {
	var (
		wg  sync.WaitGroup
		err error
	)

	err = dbp.findExecutable()
	if err != nil {
		return err
	}

	wg.Add(2)
	go dbp.parseDebugFrame(&wg)
	go dbp.obtainGoSymbols(&wg)

	wg.Wait()

	return nil
}

// Steps through process.
func (dbp *DebuggedProcess) Step() (err error) {
	for _, thread := range dbp.Threads {
		err := thread.Step()
		if err != nil {
			if _, ok := err.(ProcessExitedError); !ok {
				return err
			}
		}
	}

	return nil
}

// Step over function calls.
func (dbp *DebuggedProcess) Next() error {
	for _, thread := range dbp.Threads {
		err := thread.Next()
		if err != nil {
			if _, ok := err.(ProcessExitedError); !ok {
				return err
			}
		}
	}

	return nil
}

// Continue process until next breakpoint.
func (dbp *DebuggedProcess) Continue() error {
	for _, thread := range dbp.Threads {
		err := thread.Continue()
		if err != nil {
			return err
		}
	}

	_, _, err := trapWait(dbp, -1, 0)
	if err != nil {
		if _, ok := err.(ProcessExitedError); !ok {
			return err
		}
	}

	return nil
}

// Obtains register values from the debugged process.
func (dbp *DebuggedProcess) Registers() (*syscall.PtraceRegs, error) {
	return dbp.CurrentThread.Registers()
}

type InvalidAddressError struct {
	address uintptr
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

func (dbp *DebuggedProcess) findExecutable() error {
	procpath := fmt.Sprintf("/proc/%d/exe", dbp.Pid)

	f, err := os.OpenFile(procpath, 0, os.ModePerm)
	if err != nil {
		return err
	}

	elffile, err := elf.NewFile(f)
	if err != nil {
		return err
	}

	dbp.Executable = elffile

	return nil
}

func (dbp *DebuggedProcess) parseDebugFrame(wg *sync.WaitGroup) {
	defer wg.Done()

	debugFrame, err := dbp.Executable.Section(".debug_frame").Data()
	if err != nil {
		fmt.Println("could not get .debug_frame section", err)
		os.Exit(1)
	}

	dbp.FrameEntries = frame.Parse(debugFrame)
}

func (dbp *DebuggedProcess) obtainGoSymbols(wg *sync.WaitGroup) {
	defer wg.Done()

	var (
		symdat  []byte
		pclndat []byte
		err     error
	)

	if sec := dbp.Executable.Section(".gosymtab"); sec != nil {
		symdat, err = sec.Data()
		if err != nil {
			fmt.Println("could not get .gosymtab section", err)
			os.Exit(1)
		}
	}

	if sec := dbp.Executable.Section(".gopclntab"); sec != nil {
		pclndat, err = sec.Data()
		if err != nil {
			fmt.Println("could not get .gopclntab section", err)
			os.Exit(1)
		}
	}

	pcln := gosym.NewLineTable(pclndat, dbp.Executable.Section(".text").Addr)
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		fmt.Println("could not get initialize line table", err)
		os.Exit(1)
	}

	dbp.GoSymTable = tab
}

type ProcessExitedError struct {
	pid int
}

func (pe ProcessExitedError) Error() string {
	return fmt.Sprintf("process %d has exited", pe.pid)
}

func trapWait(dbp *DebuggedProcess, p int, options int) (int, *syscall.WaitStatus, error) {
	var status syscall.WaitStatus

	for {
		pid, err := syscall.Wait4(p, &status, syscall.WALL|options, nil)
		if err != nil {
			return -1, nil, fmt.Errorf("wait err %s %d", err, pid)
		}

		thread, threadtraced := dbp.Threads[pid]
		if !threadtraced {
			continue
		}
		thread.Status = &status

		if status.Exited() {
			if pid == dbp.Pid {
				return 0, nil, ProcessExitedError{pid}
			}
			delete(dbp.Threads, pid)
			continue
		}

		switch status.TrapCause() {
		case syscall.PTRACE_EVENT_CLONE:
			addNewThread(dbp, pid)
		default:
			pc, err := thread.CurrentPC()
			if err != nil {
				return -1, nil, fmt.Errorf("could not get current pc %s", err)
			}
			// Check to see if we have hit a breakpoint.
			if bp, ok := dbp.BreakPoints[pc-1]; ok {
				if !bp.temp {
					handleBreakPoint(dbp, thread, pid)
				}
				return pid, &status, nil
			}
		}

		if status.Stopped() {
			// The thread has stopped, but has not hit a breakpoint.
			// Continue the thread without returning control back
			// to the console.
			syscall.PtraceCont(pid, 0)
		}
	}
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

type waitstats struct {
	pid    int
	status *syscall.WaitStatus
}

type TimeoutError struct {
	pid int
}

func (err TimeoutError) Error() string {
	return fmt.Sprintf("timeout waiting for %d", err.pid)
}

// timeoutwait waits the specified duration before returning
// a TimeoutError.
func timeoutWait(pid int, options int) (int, *syscall.WaitStatus, error) {
	var (
		status   syscall.WaitStatus
		statchan = make(chan *waitstats)
		errchan  = make(chan error)
	)

	if pid > 0 {
		ps, err := parseProcessStatus(pid)
		if err != nil {
			return -1, nil, err
		}

		if ps.state == STATUS_SLEEPING {
			return 0, nil, nil
		}
	}

	go func(pid int) {
		pid, err := syscall.Wait4(pid, &status, syscall.WALL|options, nil)
		if err != nil {
			errchan <- fmt.Errorf("wait err %s %d", err, pid)
		}

		statchan <- &waitstats{pid: pid, status: &status}
	}(pid)

	select {
	case s := <-statchan:
		return s.pid, s.status, nil
	case <-time.After(2 * time.Second):
		if pid > 0 {
			ps, err := parseProcessStatus(pid)
			if err != nil {
				return -1, nil, err
			}
			syscall.Tgkill(ps.ppid, ps.pid, syscall.SIGSTOP)
		}

		return 0, nil, TimeoutError{pid}
	case err := <-errchan:
		return -1, nil, err
	}
}

func handleBreakPoint(dbp *DebuggedProcess, thread *ThreadContext, pid int) error {
	if pid != dbp.CurrentThread.Id {
		fmt.Printf("thread context changed from %d to %d\n", dbp.CurrentThread.Id, pid)
		dbp.CurrentThread = thread
	}

	// Loop through all threads and ensure that we
	// stop the rest of them, so that by the time
	// we return control to the user, all threads
	// are inactive. We send SIGSTOP and ensure all
	// threads are in in signal-delivery-stop mode.
	for _, th := range dbp.Threads {
		if th.Id == pid {
			// This thread is already stopped.
			continue
		}

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

		pid, err := syscall.Wait4(th.Id, nil, syscall.WALL, nil)
		if err != nil {
			return fmt.Errorf("wait err %s %d", err, pid)
		}
	}

	return nil
}
