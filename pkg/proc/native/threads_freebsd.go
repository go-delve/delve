package native

// #include <sys/thr.h>
import "C"
import (
	"fmt"
	"github.com/go-delve/delve/pkg/proc/fbsdutil"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
)

type WaitStatus sys.WaitStatus

// OSSpecificDetails hold FreeBSD specific process details.
type OSSpecificDetails struct {
	registers sys.Reg
}

func (t *Thread) stop() (err error) {
	_, err = C.thr_kill2(C.pid_t(t.dbp.pid), C.long(t.ID), C.int(sys.SIGSTOP))
	if err != nil {
		err = fmt.Errorf("stop err %s on thread %d", err, t.ID)
		return
	}
	// If the process is stopped, we must continue it so it can receive the
	// signal
	PtraceCont(t.dbp.pid, 0)
	_, _, err = t.dbp.waitFast(t.dbp.pid)
	if err != nil {
		err = fmt.Errorf("wait err %s on thread %d", err, t.ID)
		return
	}
	return
}

func (t *Thread) Stopped() bool {
	state := status(t.dbp.pid)
	return state == StatusStopped
}

func (t *Thread) resume() error {
	return t.resumeWithSig(0)
}

func (t *Thread) resumeWithSig(sig int) (err error) {
	t.dbp.execPtraceFunc(func() { err = PtraceCont(t.ID, sig) })
	//t.os.running = true
	return
}

func (t *Thread) singleStep() (err error) {
	t.dbp.execPtraceFunc(func() { err = PtraceSingleStep(t.ID) })
	if err != nil {
		return err
	}
	for {
		th, err := t.dbp.trapWait(t.dbp.pid)
		if err != nil {
			return err
		}
		if th.ID == t.ID {
			break
		}
		PtraceCont(th.ID, 0)
	}
	return nil
}

func (t *Thread) Blocked() bool {
	loc, err := t.Location()
	if err != nil {
		return false
	}
	if loc.Fn != nil && ((loc.Fn.Name == "runtime.futex") || (loc.Fn.Name == "runtime.usleep") || (loc.Fn.Name == "runtime.clone")) {
		return true
	}
	return false
}

func (t *Thread) restoreRegisters(savedRegs proc.Registers) error {
	sr := savedRegs.(*fbsdutil.AMD64Registers)

	var restoreRegistersErr error
	t.dbp.execPtraceFunc(func() {
		restoreRegistersErr = sys.PtraceSetRegs(t.ID, (*sys.Reg)(sr.Regs))
		if restoreRegistersErr != nil {
			return
		}
		if sr.Fpregset.Xsave != nil {
			iov := sys.Iovec{Base: &sr.Fpregset.Xsave[0], Len: uint64(len(sr.Fpregset.Xsave))}
			_, _, restoreRegistersErr = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETREGS, uintptr(t.ID), uintptr(unsafe.Pointer(&iov)), 0, 0, 0)
			return
		}

		_, _, restoreRegistersErr = syscall.Syscall6(syscall.SYS_PTRACE, sys.PTRACE_SETFPREGS, uintptr(t.ID), uintptr(unsafe.Pointer(&sr.Fpregset.AMD64PtraceFpRegs)), 0, 0, 0)
		return
	})
	if restoreRegistersErr == syscall.Errno(0) {
		restoreRegistersErr = nil
	}
	return restoreRegistersErr
}

func (t *Thread) WriteMemory(addr uintptr, data []byte) (written int, err error) {
	if t.dbp.exited {
		return 0, proc.ErrProcessExited{Pid: t.dbp.pid}
	}
	if len(data) == 0 {
		return 0, nil
	}
	t.dbp.execPtraceFunc(func() { written, err = ptraceWriteData(t.ID, addr, data) })
	return written, err
}

func (t *Thread) ReadMemory(data []byte, addr uintptr) (n int, err error) {
	if t.dbp.exited {
		return 0, proc.ErrProcessExited{Pid: t.dbp.pid}
	}
	if len(data) == 0 {
		return 0, nil
	}
	t.dbp.execPtraceFunc(func() { n, err = ptraceReadData(t.ID, addr, data) })
	return n, err
}
