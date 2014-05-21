// Package proctl provides functions for attaching to and manipulating
// a process during the debug session.
package proctl

import (
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
}

// Returns a new DebuggedProcess struct with sensible defaults.
func NewDebugProcess(pid int) (*DebuggedProcess, error) {
	err := syscall.PtraceAttach(pid)
	if err != nil {
		return nil, err
	}

	proc, err := os.FindProcess(pid)
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
	}

	return &debuggedProc, nil
}

// Obtains register values from the debugged process.
func (dbp *DebuggedProcess) Registers() (*syscall.PtraceRegs, error) {
	err := syscall.PtraceGetRegs(dbp.Pid, dbp.Regs)
	if err != nil {
		return nil, fmt.Errorf("Registers():", err)
	}

	return dbp.Regs, nil
}

// Steps through process.
func (dbp *DebuggedProcess) Step() error {
	return dbp.handleResult(syscall.PtraceSingleStep(dbp.Pid))
}

// Continue process until next breakpoint.
func (dbp *DebuggedProcess) Continue() error {
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
