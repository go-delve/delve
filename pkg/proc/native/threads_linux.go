package native

import (
	"fmt"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
)

type WaitStatus sys.WaitStatus

// OSSpecificDetails hold Linux specific
// process details.
type OSSpecificDetails struct {
	registers sys.PtraceRegs
	running   bool
}

func (t *Thread) stop() (err error) {
	err = sys.Tgkill(t.dbp.pid, t.ID, sys.SIGSTOP)
	if err != nil {
		err = fmt.Errorf("stop err %s on thread %d", err, t.ID)
		return
	}
	return
}

// Stopped returns whether the thread is stopped at
// the operating system level.
func (t *Thread) Stopped() bool {
	state := status(t.ID, t.dbp.os.comm)
	stopped := state == StatusTraceStop || state == StatusTraceStopT || state == StatusDisappeared
	if stopped {
		t.os.running = false
	}
	return stopped
}

func (t *Thread) resume() error {
	return t.resumeWithSig(0)
}

func (t *Thread) resumeWithSig(sig int) (err error) {
	t.os.running = true
	t.dbp.execPtraceFunc(func() { err = PtraceCont(t.ID, sig) })
	return
}

func (t *Thread) singleStep() (err error) {
	for {
		t.dbp.execPtraceFunc(func() { err = sys.PtraceSingleStep(t.ID) })
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

func (t *Thread) Blocked() bool {
	regs, err := t.Registers(false)
	if err != nil {
		return false
	}
	pc := regs.PC()
	fn := t.BinInfo().PCToFunc(pc)
	if fn != nil && ((fn.Name == "runtime.futex") || (fn.Name == "runtime.usleep") || (fn.Name == "runtime.clone")) {
		return true
	}
	return false
}

func (t *Thread) restoreRegisters(savedRegs proc.Registers) error {
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
		return
	}
	t.dbp.execPtraceFunc(func() { written, err = sys.PtracePokeData(t.ID, addr, data) })
	return
}

func (t *Thread) ReadMemory(data []byte, addr uintptr) (n int, err error) {
	if t.dbp.exited {
		return 0, proc.ErrProcessExited{Pid: t.dbp.pid}
	}
	if len(data) == 0 {
		return
	}
	t.dbp.execPtraceFunc(func() { _, err = sys.PtracePeekData(t.ID, addr, data) })
	if err == nil {
		n = len(data)
	}
	return
}
