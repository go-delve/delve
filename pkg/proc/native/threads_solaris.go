package native

// #include <libproc.h>
import "C"
import (
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/solutil"
)

type waitStatus sys.WaitStatus

// osSpecificDetails hold Solaris specific process details.
type osSpecificDetails struct {
	lwp *C.struct_ps_lwphandle
}

func (t *nativeThread) stop() (err error) {
	t.dbp.execPtraceFunc(func() {
		if rc, err1 := C.Ldstop(t.os.lwp); rc == -1 {
			err = err1
		}
	})
	return err
}

func (t *nativeThread) resume() (err error) {
	t.dbp.execPtraceFunc(func() {
		if rc, err1 := C.Lsetrun(t.os.lwp, 0, C.PRCSIG|C.PRCFAULT); rc == -1 {
			err = err1
		}
	})
	return err
}

func (t *nativeThread) singleStep() (err error) {
	t.dbp.execPtraceFunc(func() {
		// We call Lwait on the thread even though it should already
		// be stopped because it causes the status which is cached
		// in the handle to get updated. If we don't do this Lsetrun
		// can return EBUSY because it thinks it's already running.
		if rc, err1 := C.Lwait(t.os.lwp, 0); rc == -1 {
			err = err1
		} else if rc, err1 = C.Lsetrun(t.os.lwp, 0, C.PRSTEP|C.PRCFAULT); rc == -1 {
			err = err1
		}
	})
	if err != nil {
		return err
	}
	for {
		th, err := t.dbp.trapWait(-1)
		if err != nil {
			return err
		}
		if th.ID == t.ID {
			break
		}
		if err = th.resume(); err != nil {
			return err
		}
	}
	return nil
}

func (t *nativeThread) restoreRegisters(savedRegs proc.Registers) error {
	currentRegs, err := registers(t)
	if err != nil {
		return err
	}
	cr := currentRegs.(*solutil.AMD64Registers)
	sr := savedRegs.(*solutil.AMD64Registers)
	sr.Regs.FsBase = cr.Regs.FsBase
	sr.Regs.GsBase = cr.Regs.GsBase
	t.dbp.execPtraceFunc(func() {
		if rc, err1 := C.Plwp_setregs(t.dbp.os.pr, C.uint(t.ID), (*C.long)(unsafe.Pointer(&sr.Regs))); rc == -1 {
			err = err1
		} else if rc, err1 = C.Plwp_setfpregs(t.dbp.os.pr, C.uint(t.ID), (*C.prfpregset_t)(unsafe.Pointer(sr.Fpregset))); rc == -1 {
			err = err1
		}
	})
	return err
}

func (t *nativeThread) WriteMemory(addr uint64, data []byte) (n int, err error) {
	if t.dbp.exited {
		return 0, proc.ErrProcessExited{Pid: t.dbp.pid}
	}
	if len(data) == 0 {
		return 0, nil
	}
	t.dbp.execPtraceFunc(func() {
		if rc, err1 := C.Pwrite(t.dbp.os.pr, unsafe.Pointer(&data[0]), C.ulong(len(data)), C.uintptr_t(addr)); rc == -1 {
			err = err1
		} else {
			n = int(rc)
		}
	})
	return n, err
}

func (t *nativeThread) ReadMemory(data []byte, addr uint64) (n int, err error) {
	if t.dbp.exited {
		return 0, proc.ErrProcessExited{Pid: t.dbp.pid}
	}
	if len(data) == 0 {
		return 0, nil
	}
	t.dbp.execPtraceFunc(func() {
		if rc, err1 := C.Pread(t.dbp.os.pr, unsafe.Pointer(&data[0]), C.ulong(len(data)), C.uintptr_t(addr)); rc == -1 {
			err = err1
		} else {
			n = int(rc)
		}
	})
	return n, err
}

func (t *nativeThread) writeHardwareBreakpoint(addr uint64, wtype proc.WatchType, idx uint8) error {
	return proc.ErrHWBreakUnsupported
}

func (t *nativeThread) clearHardwareBreakpoint(addr uint64, wtype proc.WatchType, idx uint8) error {
	return proc.ErrHWBreakUnsupported
}

func (t *nativeThread) findHardwareBreakpoint() (*proc.Breakpoint, error) {
	return nil, nil
}
