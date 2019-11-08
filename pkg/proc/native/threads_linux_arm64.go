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

func (thread *Thread) fpRegisters() (fpregs []proc.Register, fpregset []byte, err error) {
	thread.dbp.execPtraceFunc(func() { fpregset, err = PtraceGetFpRegset(thread.ID) })
	fpregs = linutil.Decode(fpregset)
	if err != nil {
		err = fmt.Errorf("could not get floating point registers: %v", err.Error())
	}
	return
}

func (t *Thread) restoreRegisters(savedRegs proc.Registers) error {
	sr := savedRegs.(*linutil.ARM64Registers)

	var restoreRegistersErr error
	t.dbp.execPtraceFunc(func() {
		restoreRegistersErr = ptraceSetGRegs(t.ID, sr.Regs)
		if restoreRegistersErr != syscall.Errno(0) {
			return
		}
		if sr.Fpregset != nil {
			iov := sys.Iovec{Base: &sr.Fpregset[0], Len: uint64(len(sr.Fpregset))}
			_, _, restoreRegistersErr = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET, uintptr(t.ID), uintptr(elf.NT_FPREGSET), uintptr(unsafe.Pointer(&iov)), 0, 0)
		}
		//return
	})
	if restoreRegistersErr == syscall.Errno(0) {
		restoreRegistersErr = nil
	}
	return restoreRegistersErr
}
