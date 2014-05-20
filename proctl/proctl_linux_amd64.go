package proctl

import (
	"fmt"
	"os"
	"syscall"
)

type DebuggedProcess struct {
	Pid     int
	Regs    *syscall.PtraceRegs
	Process *os.Process
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

	debuggedProc := DebuggedProcess{
		Pid:     pid,
		Regs:    &syscall.PtraceRegs{},
		Process: proc,
	}

	_, err = proc.Wait()
	if err != nil {
		return nil, err
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
	err := syscall.PtraceSingleStep(dbp.Pid)
	if err != nil {
		return err
	}

	_, err = dbp.Process.Wait()
	if err != nil {
		return err
	}

	return nil
}
