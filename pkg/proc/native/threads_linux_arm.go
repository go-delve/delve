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
