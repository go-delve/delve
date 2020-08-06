package native

import (
	"debug/elf"
	"fmt"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

func (thread *nativeThread) fpRegisters() ([]proc.Register, []byte, error) {
	var err error
	var arm_fpregs linutil.ARM64PtraceFpRegs
	thread.dbp.execPtraceFunc(func() { arm_fpregs.Vregs, err = ptraceGetFpRegset(thread.ID) })
	fpregs := arm_fpregs.Decode()
	if err != nil {
		err = fmt.Errorf("could not get floating point registers: %v", err.Error())
	}
	return fpregs, arm_fpregs.Vregs, err
}

func (t *nativeThread) restoreRegisters(savedRegs proc.Registers) error {
	sr := savedRegs.(*linutil.ARMRegisters)

	var restoreRegistersErr error
	t.dbp.execPtraceFunc(func() {
		restoreRegistersErr = ptraceSetGRegs(t.ID, sr.Regs)
		if restoreRegistersErr != syscall.Errno(0) {
			return
		}
		if sr.Fpregset != nil {
			iov := sys.Iovec{Base: &sr.Fpregset[0], Len: uint32(len(sr.Fpregset))}
			_, _, restoreRegistersErr = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET, uintptr(t.ID), uintptr(elf.NT_FPREGSET), uintptr(unsafe.Pointer(&iov)), 0, 0)
		}
	})
	if restoreRegistersErr == syscall.Errno(0) {
		restoreRegistersErr = nil
	}
	return restoreRegistersErr
}

func (t *nativeThread) singleStep() (err error) {
	// Arm don't have ptrace singleStep implemented, so we use breakpoint to emulate it.
	for {
		pc, err := t.PC()
		if err != nil {
			return err
		}
		nextPc := pc + uint64(t.BinInfo().Arch.MaxInstructionLength())
		_, _, _, originalData, err := t.dbp.WriteBreakpoint(nextPc)
		if err != nil {
			return err
		}
		defer func() {
			_, _ = t.WriteMemory(uintptr(nextPc), originalData)
		}()
		t.dbp.execPtraceFunc(func() { err = ptraceCont(t.ID, 0) })
		if err != nil {
			return err
		}
		wpid, status, err := t.dbp.waitFast(t.ID)
		if err != nil {
			return err
		}
		if (status == nil || status.Exited()) && wpid == t.dbp.pid {
			t.dbp.postExit()
			rs := 0
			if status != nil {
				rs = status.ExitStatus()
			}
			return proc.ErrProcessExited{Pid: t.dbp.pid, Status: rs}
		}
		if wpid == t.ID && status.StopSignal() == sys.SIGTRAP {
			return nil
		}
	}
}
