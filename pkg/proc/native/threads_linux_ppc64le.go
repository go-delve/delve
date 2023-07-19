package native

import (
	"fmt"
	"debug/elf"
        "syscall"
        "unsafe"

        sys "golang.org/x/sys/unix"


	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

func (t *nativeThread) fpRegisters() ([]proc.Register, []byte, error) {
	var regs []proc.Register
	var fpregs linutil.PPC64LEPtraceFpRegs
	var err error

	t.dbp.execPtraceFunc(func() { fpregs.Fp, err = ptraceGetFpRegset(t.ID) })
	regs = fpregs.Decode()
	if err != nil {
		err = fmt.Errorf("could not get floating point registers: %v", err.Error())
	}
	return regs, fpregs.Fp, err
}

func (t *nativeThread) restoreRegisters(savedRegs proc.Registers) error {
        sr := savedRegs.(*linutil.PPC64LERegisters)

        var restoreRegistersErr error
        t.dbp.execPtraceFunc(func() {
                restoreRegistersErr = ptraceSetGRegs(t.ID, sr.Regs)
                if restoreRegistersErr != syscall.Errno(0) && restoreRegistersErr != nil {
                        return
                }
                if sr.Fpregset != nil {
                        iov := sys.Iovec{Base: &sr.Fpregset[0], Len: _PPC64LE_FPREGS_SIZE}
                        _, _, restoreRegistersErr = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET, uintptr(t.ID), uintptr(elf.NT_FPREGSET), uintptr(unsafe.Pointer(&iov)), 0, 0)
                }
        })
        if restoreRegistersErr == syscall.Errno(0) {
                restoreRegistersErr = nil
        }
        return restoreRegistersErr
}

