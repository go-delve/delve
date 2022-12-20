package native

import (
	"bytes"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/amd64util"
	"github.com/go-delve/delve/pkg/proc/fbsdutil"
)

type waitStatus sys.WaitStatus

// osSpecificDetails hold FreeBSD specific process details.
type osSpecificDetails struct {
	registers sys.Reg
}

func (t *nativeThread) singleStep() (err error) {
	t.dbp.execPtraceFunc(func() { err = ptraceSetStep(t.ID) })
	if err != nil {
		return err
	}
	defer func() {
		t.dbp.execPtraceFunc(func() { ptraceClearStep(t.ID) })
	}()

	t.dbp.execPtraceFunc(func() { err = ptraceResume(t.ID) })
	if err != nil {
		return err
	}
	defer func() {
		t.dbp.execPtraceFunc(func() { ptraceSuspend(t.ID) })
	}()

	sig := 0
	for {
		err = t.dbp.ptraceCont(sig)
		sig = 0
		if err != nil {
			return err
		}

		trapthread, err := t.dbp.trapWaitInternal(-1, trapWaitStepping)
		if err != nil {
			return err
		}

		status := ((*sys.WaitStatus)(trapthread.Status))

		if trapthread.ID == t.ID {
			switch s := status.StopSignal(); s {
			case sys.SIGTRAP:
				return nil
			case sys.SIGSTOP:
				// delayed SIGSTOP, ignore it
			case sys.SIGILL, sys.SIGBUS, sys.SIGFPE, sys.SIGSEGV:
				// propagate signals that can be caused by current instruction
				sig = int(s)
			default:
				t.dbp.os.delayedSignal = s
			}
		} else {
			if status.StopSignal() == sys.SIGTRAP {
				t.dbp.os.trapThreads = append(t.dbp.os.trapThreads, trapthread.ID)
			} else {
				t.dbp.os.delayedSignal = status.StopSignal()
			}
		}
	}
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

func (t *nativeThread) atHardcodedBreakpoint(pc uint64) bool {
	for _, bpinstr := range [][]byte{
		t.dbp.BinInfo().Arch.BreakpointInstruction(),
		t.dbp.BinInfo().Arch.AltBreakpointInstruction()} {
		if bpinstr == nil {
			continue
		}
		buf := make([]byte, len(bpinstr))
		_, _ = t.ReadMemory(buf, pc-uint64(len(buf)))
		if bytes.Equal(buf, bpinstr) {
			return true
		}
	}
	return false
}
