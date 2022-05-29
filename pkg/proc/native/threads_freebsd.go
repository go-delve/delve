package native

// #include <sys/thr.h>
import "C"

import (
	"fmt"
	"github.com/go-delve/delve/pkg/proc/fbsdutil"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/amd64util"
)

type waitStatus sys.WaitStatus

// osSpecificDetails hold FreeBSD specific process details.
type osSpecificDetails struct {
	registers sys.Reg
}

func (t *nativeThread) stop() (err error) {
	_, err = C.thr_kill2(C.pid_t(t.dbp.pid), C.long(t.ID), C.int(sys.SIGSTOP))
	if err != nil {
		err = fmt.Errorf("stop err %s on thread %d", err, t.ID)
		return
	}
	// If the process is stopped, we must continue it so it can receive the
	// signal
	t.dbp.execPtraceFunc(func() { err = ptraceCont(t.dbp.pid, 0) })
	if err != nil {
		return err
	}
	_, _, err = t.dbp.waitFast(t.dbp.pid)
	if err != nil {
		err = fmt.Errorf("wait err %s on thread %d", err, t.ID)
		return
	}
	return
}

func (t *nativeThread) Stopped() bool {
	state := status(t.dbp.pid)
	return state == statusStopped
}

func (t *nativeThread) resume() error {
	return t.resumeWithSig(0)
}

func (t *nativeThread) resumeWithSig(sig int) (err error) {
	t.dbp.execPtraceFunc(func() { err = ptraceCont(t.ID, sig) })
	return
}

func (t *nativeThread) singleStep() (err error) {
	t.dbp.execPtraceFunc(func() { err = ptraceSingleStep(t.ID) })
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
		t.dbp.execPtraceFunc(func() { err = ptraceCont(th.ID, 0) })
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *nativeThread) restoreRegisters(savedRegs proc.Registers) error {
	sr := savedRegs.(*fbsdutil.AMD64Registers)
	return setRegisters(t, sr, true)
}

func (t *nativeThread) WriteMemory(addr uint64, data []byte) (written int, err error) {
	if t.dbp.exited {
		return 0, proc.ErrProcessExited{Pid: t.dbp.pid}
	}
	if len(data) == 0 {
		return 0, nil
	}
	t.dbp.execPtraceFunc(func() { written, err = ptraceWriteData(t.ID, uintptr(addr), data) })
	return written, err
}

func (t *nativeThread) ReadMemory(data []byte, addr uint64) (n int, err error) {
	if t.dbp.exited {
		return 0, proc.ErrProcessExited{Pid: t.dbp.pid}
	}
	if len(data) == 0 {
		return 0, nil
	}
	t.dbp.execPtraceFunc(func() { n, err = ptraceReadData(t.ID, uintptr(addr), data) })
	return n, err
}

func (t *nativeThread) withDebugRegisters(f func(*amd64util.DebugRegisters) error) error {
	return proc.ErrHWBreakUnsupported
}

// SoftExc returns true if this thread received a software exception during the last resume.
func (t *nativeThread) SoftExc() bool {
	return false
}
