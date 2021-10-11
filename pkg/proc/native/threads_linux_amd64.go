package native

import (
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/amd64util"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

func (t *nativeThread) restoreRegisters(savedRegs proc.Registers) error {
	sr := savedRegs.(*linutil.AMD64Registers)

	var restoreRegistersErr error
	t.dbp.execPtraceFunc(func() {
		oldRegs := (*sys.PtraceRegs)(sr.Regs)

		var currentRegs sys.PtraceRegs
		restoreRegistersErr = sys.PtraceGetRegs(t.ID, &currentRegs)
		if restoreRegistersErr != nil {
			return
		}
		// restoreRegisters is only supposed to restore CPU registers, not FS_BASE and GS_BASE
		oldRegs.Fs_base = currentRegs.Fs_base
		oldRegs.Gs_base = currentRegs.Gs_base

		restoreRegistersErr = sys.PtraceSetRegs(t.ID, oldRegs)

		if restoreRegistersErr != nil {
			return
		}
		if sr.Fpregset.Xsave != nil {
			iov := sys.Iovec{Base: &sr.Fpregset.Xsave[0], Len: uint64(len(sr.Fpregset.Xsave))}
			_, _, restoreRegistersErr = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGSET, uintptr(t.ID), _NT_X86_XSTATE, uintptr(unsafe.Pointer(&iov)), 0, 0)
			return
		}

		_, _, restoreRegistersErr = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETFPREGS, uintptr(t.ID), uintptr(0), uintptr(unsafe.Pointer(&sr.Fpregset.AMD64PtraceFpRegs)), 0, 0)
	})
	if restoreRegistersErr == syscall.Errno(0) {
		restoreRegistersErr = nil
	}
	return restoreRegistersErr
}

const debugRegUserOffset = 848 // offset of debug registers in the user struct, see source/arch/x86/kernel/ptrace.c

func (t *nativeThread) withDebugRegisters(f func(*amd64util.DebugRegisters) error) error {
	var err error
	t.dbp.execPtraceFunc(func() {
		debugregs := make([]uint64, 8)

		for i := range debugregs {
			if i == 4 || i == 5 {
				continue
			}
			_, _, err = sys.Syscall6(sys.SYS_PTRACE, sys.PTRACE_PEEKUSR, uintptr(t.ID), uintptr(debugRegUserOffset+uintptr(i)*unsafe.Sizeof(debugregs[0])), uintptr(unsafe.Pointer(&debugregs[i])), 0, 0)
			if err != nil && err != syscall.Errno(0) {
				return
			}
		}

		drs := amd64util.NewDebugRegisters(&debugregs[0], &debugregs[1], &debugregs[2], &debugregs[3], &debugregs[6], &debugregs[7])

		err = f(drs)

		if drs.Dirty {
			for i := range debugregs {
				if i == 4 || i == 5 {
					// Linux will return EIO for DR4 and DR5
					continue
				}
				_, _, err = sys.Syscall6(sys.SYS_PTRACE, sys.PTRACE_POKEUSR, uintptr(t.ID), uintptr(debugRegUserOffset+uintptr(i)*unsafe.Sizeof(debugregs[0])), uintptr(debugregs[i]), 0, 0)
				if err != nil && err != syscall.Errno(0) {
					return
				}
			}
		}
	})
	if err == syscall.Errno(0) || err == sys.ESRCH {
		err = nil
	}
	return err
}
