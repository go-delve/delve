// Package proctl provides functions for attaching to and manipulating
// a process during the debug session.
package proctl

import (
	"bytes"
	"debug/gosym"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"

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
}

type BreakPointExistsError struct {
	file string
	line int
	addr uintptr
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
	debuggedProc := DebuggedProcess{
		Pid:         pid,
		Threads:     make(map[int]*ThreadContext),
		BreakPoints: make(map[uint64]*BreakPoint),
	}

	_, err := debuggedProc.AttachThread(pid)
	if err != nil {
		return nil, err
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, err
	}

	debuggedProc.Process = proc
	err = debuggedProc.LoadInformation()
	if err != nil {
		return nil, err
	}

	// TODO: for some reason this isn't grabbing all threads, and
	// neither is the subsequent ptrace clone op. Maybe when
	// we attach the process is right in the middle of a clone syscall?
	for _, tid := range threadIds(pid) {
		if _, ok := debuggedProc.Threads[tid]; !ok {
			_, err := debuggedProc.AttachThread(tid)
			if err != nil {
				return nil, err
			}
		}
	}

	return &debuggedProc, nil
}

func (dbp *DebuggedProcess) AttachThread(tid int) (*ThreadContext, error) {
	var status syscall.WaitStatus

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
	if err != nil {
		var status syscall.WaitStatus
		pid, e := syscall.Wait4(tid, &status, syscall.WALL, nil)
		if e != nil {
			if status.Exited() {
				return nil, ProcessExitedError{tid}
			}
			return nil, fmt.Errorf("error while waiting after adding thread: %d %s %v %d", tid, e, status.Exited(), status.TrapCause())
		}

		if pid != 0 {
			err := syscall.PtraceSetOptions(tid, syscall.PTRACE_O_TRACECLONE)
			if err != nil {
				return nil, fmt.Errorf("could not set options for new traced thread %d %s", tid, err)
			}
		}
	}

	tctxt := &ThreadContext{
		Id:      tid,
		Process: dbp,
		Regs:    new(syscall.PtraceRegs),
	}

	if tid == dbp.Pid {
		dbp.CurrentThread = tctxt
	}

	dbp.Threads[tid] = tctxt

	return tctxt, nil
}

// Sets a breakpoint in the running process.
func (dbp *DebuggedProcess) Break(addr uintptr) (*BreakPoint, error) {
	var (
		int3         = []byte{0xCC}
		f, l, fn     = dbp.GoSymTable.PCToLine(uint64(addr))
		originalData = make([]byte, 1)
	)

	if fn == nil {
		return nil, InvalidAddressError{address: addr}
	}

	_, err := syscall.PtracePeekData(dbp.CurrentThread.Id, addr, originalData)
	if err != nil {
		return nil, err
	}

	if bytes.Equal(originalData, int3) {
		return nil, BreakPointExistsError{f, l, addr}
	}

	_, err = syscall.PtracePokeData(dbp.CurrentThread.Id, addr, int3)
	if err != nil {
		return nil, err
	}

	breakpoint := &BreakPoint{
		FunctionName: fn.Name,
		File:         f,
		Line:         l,
		Addr:         uint64(addr),
		OriginalData: originalData,
	}

	dbp.BreakPoints[uint64(addr)] = breakpoint

	return breakpoint, nil
}

// Clears a breakpoint.
func (dbp *DebuggedProcess) Clear(pc uint64) (*BreakPoint, error) {
	bp, ok := dbp.BreakPoints[pc]
	if !ok {
		return nil, fmt.Errorf("No breakpoint currently set for %#v", pc)
	}

	_, err := syscall.PtracePokeData(dbp.CurrentThread.Id, uintptr(bp.Addr), bp.OriginalData)
	if err != nil {
		return nil, err
	}

	delete(dbp.BreakPoints, pc)

	return bp, nil
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
		if _, ok := err.(ProcessExitedError); !ok {
			return err
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

	_, _, err := wait(dbp, -1, 0)
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

// Takes an offset from RSP and returns the address of the
// instruction the currect function is going to return to.
func (dbp *DebuggedProcess) ReturnAddressFromOffset(offset int64) uint64 {
	regs, err := dbp.Registers()
	if err != nil {
		panic("Could not obtain register values")
	}

	retaddr := int64(regs.Rsp) + offset
	data := make([]byte, 8)
	syscall.PtracePeekText(dbp.Pid, uintptr(retaddr), data)
	return binary.LittleEndian.Uint64(data)
}

type ProcessExitedError struct {
	pid int
}

func (pe ProcessExitedError) Error() string {
	return fmt.Sprintf("process %d has exited", pe.pid)
}

func wait(dbp *DebuggedProcess, pid int, options int) (int, *syscall.WaitStatus, error) {
	var status syscall.WaitStatus

	for {
		pid, e := syscall.Wait4(-1, &status, syscall.WALL|options, nil)
		if e != nil {
			return -1, nil, fmt.Errorf("wait err %s %d", e, pid)
		}

		thread, threadtraced := dbp.Threads[pid]
		if threadtraced {
			thread.Status = &status
		}

		if status.Exited() {
			if pid == dbp.Pid {
				return 0, nil, ProcessExitedError{pid}
			}

			delete(dbp.Threads, pid)
		}

		if status.StopSignal() == syscall.SIGTRAP {
			if status.TrapCause() == syscall.PTRACE_EVENT_CLONE {
				// A traced thread has cloned a new thread, grab the pid and
				// add it to our list of traced threads.
				msg, err := syscall.PtraceGetEventMsg(pid)
				if err != nil {
					return 0, nil, fmt.Errorf("could not get event message: %s", err)
				}

				_, err = dbp.addThread(int(msg))
				if err != nil {
					if _, ok := err.(ProcessExitedError); ok {
						continue
					}
					return 0, nil, err
				}

				err = syscall.PtraceCont(int(msg), 0)
				if err != nil {
					return 0, nil, fmt.Errorf("could not continue new thread %d %s", msg, err)
				}

				err = syscall.PtraceCont(pid, 0)
				if err != nil {
					return 0, nil, fmt.Errorf("could not continue stopped thread %d %s", pid, err)
				}

				continue
			}

			if pid != dbp.CurrentThread.Id {
				fmt.Printf("changed thread context from %d to %d\n", dbp.CurrentThread.Id, pid)
				dbp.CurrentThread = thread
			}

			pc, _ := thread.CurrentPC()
			if _, ok := dbp.BreakPoints[pc-1]; ok {
				return pid, &status, nil
			}
		}

		if status.Stopped() {
			if pid == dbp.Pid {
				return pid, &status, nil
			}
		}
	}
}
