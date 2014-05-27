// Package proctl provides functions for attaching to and manipulating
// a process during the debug session.
package proctl

import (
	"debug/elf"
	"debug/gosym"
	"fmt"
	"os"
	"syscall"
)

// Struct representing a debugged process. Holds onto pid, register values,
// process struct and process state.
type DebuggedProcess struct {
	Pid          int
	Regs         *syscall.PtraceRegs
	Process      *os.Process
	ProcessState *os.ProcessState
	Executable   *elf.File
	Symbols      []elf.Symbol
	GoSymTable   *gosym.Table
	BreakPoints  map[string]*BreakPoint
}

type BreakPoint struct {
	FunctionName string
	File         string
	Line         int
	Addr         uint64
	OriginalData []byte
}

// Returns a new DebuggedProcess struct with sensible defaults.
func NewDebugProcess(pid int) (*DebuggedProcess, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, err
	}

	err = syscall.PtraceAttach(pid)
	if err != nil {
		return nil, err
	}

	ps, err := proc.Wait()
	if err != nil {
		return nil, err
	}

	debuggedProc := DebuggedProcess{
		Pid:          pid,
		Regs:         &syscall.PtraceRegs{},
		Process:      proc,
		ProcessState: ps,
		BreakPoints:  make(map[string]*BreakPoint),
	}

	err = debuggedProc.LoadInformation()
	if err != nil {
		return nil, err
	}

	return &debuggedProc, nil
}

func (dbp *DebuggedProcess) LoadInformation() error {
	err := dbp.findExecutable()
	if err != nil {
		return err
	}

	err = dbp.obtainGoSymbols()
	if err != nil {
		return err
	}

	return nil
}

// Obtains register values from the debugged process.
func (dbp *DebuggedProcess) Registers() (*syscall.PtraceRegs, error) {
	err := syscall.PtraceGetRegs(dbp.Pid, dbp.Regs)
	if err != nil {
		return nil, fmt.Errorf("Registers():", err)
	}

	return dbp.Regs, nil
}

// Sets a breakpoint in the running process.
func (dbp *DebuggedProcess) Break(fname string) (*BreakPoint, error) {
	var (
		int3 = []byte{'0', 'x', 'C', 'C'}
		fn   = dbp.GoSymTable.LookupFunc(fname)
	)

	if fn == nil {
		return nil, fmt.Errorf("No function named %s\n", fname)
	}

	f, l, _ := dbp.GoSymTable.PCToLine(fn.Entry)

	orginalData := make([]byte, 1)
	addr := uintptr(fn.Entry)
	_, err := syscall.PtracePeekData(dbp.Pid, addr, orginalData)
	if err != nil {
		return nil, err
	}

	_, err = syscall.PtracePokeData(dbp.Pid, addr, int3)
	if err != nil {
		return nil, err
	}

	breakpoint := &BreakPoint{
		FunctionName: fn.Name,
		File:         f,
		Line:         l,
		Addr:         fn.Entry,
		OriginalData: orginalData,
	}

	dbp.BreakPoints[fname] = breakpoint

	return breakpoint, nil
}

// Steps through process.
func (dbp *DebuggedProcess) Step() error {
	regs, err := dbp.Registers()
	if err != nil {
		return err
	}

	bp, ok := dbp.PCtoBP(regs.PC())
	if ok {
		err = dbp.restoreInstruction(regs.PC(), bp.OriginalData)
		if err != nil {
			return err
		}

	}

	err = dbp.handleResult(syscall.PtraceSingleStep(dbp.Pid))
	if err != nil {
		return fmt.Errorf("step failed: ", err.Error())
	}

	// Restore breakpoint
	if ok {
		_, err := dbp.Break(bp.FunctionName)
		if err != nil {
			return err
		}
	}

	return nil
}

// Continue process until next breakpoint.
func (dbp *DebuggedProcess) Continue() error {
	// Stepping first will ensure we are able to continue
	// past a breakpoint if that's currently where we are stopped.
	err := dbp.Step()
	if err != nil {
		return err
	}

	return dbp.handleResult(syscall.PtraceCont(dbp.Pid, 0))
}

func (dbp *DebuggedProcess) handleResult(err error) error {
	if err != nil {
		return err
	}

	ps, err := dbp.Process.Wait()
	if err != nil {
		return err
	}

	dbp.ProcessState = ps

	return nil
}

func (dbp *DebuggedProcess) findExecutable() error {
	procpath := fmt.Sprintf("/proc/%d/exe", dbp.Pid)

	f, err := os.Open(procpath)
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

func (dbp *DebuggedProcess) obtainGoSymbols() error {
	symdat, err := dbp.Executable.Section(".gosymtab").Data()
	if err != nil {
		return err
	}

	pclndat, err := dbp.Executable.Section(".gopclntab").Data()
	if err != nil {
		return err
	}

	pcln := gosym.NewLineTable(pclndat, dbp.Executable.Section(".text").Addr)
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		return err
	}

	dbp.GoSymTable = tab

	return nil
}

func (dbp *DebuggedProcess) PCtoBP(pc uint64) (*BreakPoint, bool) {
	_, _, fn := dbp.GoSymTable.PCToLine(pc)
	bp, ok := dbp.BreakPoints[fn.Name]
	return bp, ok
}

func (dbp *DebuggedProcess) restoreInstruction(pc uint64, data []byte) error {
	_, err := syscall.PtracePokeData(dbp.Pid, uintptr(pc), data)
	return err
}
