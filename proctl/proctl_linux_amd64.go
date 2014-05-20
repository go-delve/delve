package proctl

import (
	"fmt"
	"os"
	"syscall"
)

type DebuggedProcess struct {
	Pid          int
	Regs         *syscall.PtraceRegs
	Process      *os.Process
	ProcessState *os.ProcessState
}

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

func (dbp *DebuggedProcess) Registers() (*syscall.PtraceRegs, error) {
	err := syscall.PtraceGetRegs(dbp.Pid, dbp.Regs)
	if err != nil {
		return nil, fmt.Errorf("Registers():", err)
	}

	return dbp.Regs, nil
}

func (dbp *DebuggedProcess) Step() error {
	return dbp.Exec(syscall.PtraceSingleStep(dbp.Pid))
}

func (dbp *DebuggedProcess) Continue() error {
	return dbp.Exec(syscall.PtraceCont(dbp.Pid, 0))
}

func (dbp *DebuggedProcess) Exec(err error) error {
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
