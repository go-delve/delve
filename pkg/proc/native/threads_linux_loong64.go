//go:build linux && loong64
// +build linux,loong64

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
	var loong64_fpregs linutil.LOONG64PtraceFpRegs

	thread.dbp.execPtraceFunc(func() { loong64_fpregs.Vregs, err = ptraceGetFpRegset(thread.ID) })
	fpregs := loong64_fpregs.Decode()

	if err != nil {
		err = fmt.Errorf("could not get floating point registers: %v", err.Error())
	}

	return fpregs, loong64_fpregs.Vregs, err
}

func (t *nativeThread) restoreRegisters(savedRegs proc.Registers) error {
	var restoreRegistersErr error

	sr := savedRegs.(*linutil.LOONG64Registers)
	t.dbp.execPtraceFunc(func() {
		restoreRegistersErr = ptraceSetGRegs(t.ID, sr.Regs)
		if restoreRegistersErr != syscall.Errno(0) {
			return
		}

		if sr.Fpregset != nil {
			iov := sys.Iovec{Base: &sr.Fpregset[0], Len: uint64(len(sr.Fpregset))}
			_, _, restoreRegistersErr = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET,
				uintptr(t.ID), uintptr(elf.NT_FPREGSET), uintptr(unsafe.Pointer(&iov)), 0, 0)
		}
	})

	if restoreRegistersErr == syscall.Errno(0) {
		restoreRegistersErr = nil
	}

	return restoreRegistersErr
}
