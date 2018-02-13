package native

import (
	"fmt"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/pkg/proc"
)

type WaitStatus sys.WaitStatus

// OSSpecificDetails hold Linux specific
// process details.
type OSSpecificDetails struct {
	registers sys.PtraceRegs
}

func (t *Thread) halt() (err error) {
	err = sys.Tgkill(t.dbp.pid, t.ID, sys.SIGSTOP)
	if err != nil {
		err = fmt.Errorf("halt err %s on thread %d", err, t.ID)
		return
	}
	return
}

// Stopped returns whether the thread is stopped at
// the operating system level.
func (t *Thread) Stopped() bool {
	state := status(t.ID, t.dbp.os.comm)
	return state == StatusTraceStop || state == StatusTraceStopT
}

func (t *Thread) resume() error {
	return t.resumeWithSig(0)
}

func (t *Thread) resumeWithSig(sig int) (err error) {
	t.running = true
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
			return proc.ProcessExitedError{Pid: t.dbp.pid, Status: rs}
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

func (t *Thread) saveRegisters() (proc.Registers, error) {
	var err error
	t.dbp.execPtraceFunc(func() { err = sys.PtraceGetRegs(t.ID, &t.os.registers) })
	if err != nil {
		return nil, fmt.Errorf("could not save register contents")
	}
	return &Regs{&t.os.registers, nil}, nil
}

func (t *Thread) restoreRegisters() (err error) {
	t.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(t.ID, &t.os.registers) })
	return
}

func (t *Thread) WriteMemory(addr uintptr, data []byte) (written int, err error) {
	if t.dbp.exited {
		return 0, proc.ProcessExitedError{Pid: t.dbp.pid}
	}
	if len(data) == 0 {
		return
	}
	t.dbp.execPtraceFunc(func() { written, err = sys.PtracePokeData(t.ID, addr, data) })
	return
}

func (t *Thread) ReadMemory(data []byte, addr uintptr) (n int, err error) {
	if t.dbp.exited {
		return 0, proc.ProcessExitedError{Pid: t.dbp.pid}
	}
	if len(data) == 0 {
		return
	}
	t.dbp.execPtraceFunc(func() { _, err = sys.PtracePeekData(t.ID, addr, data) })
	return
}
