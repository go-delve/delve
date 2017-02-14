package proc

import (
	"errors"
	"fmt"

	sys "golang.org/x/sys/unix"
)

type WaitStatus sys.WaitStatus

// OSSpecificDetails hold Linux specific
// process details.
type OSSpecificDetails struct {
	registers sys.PtraceRegs
}

func (t *Thread) halt() (err error) {
	if t.dbp.debugType == debugTypeCore {
		return errors.New("cannot halt a thread in a core file")
	}

	err = sys.Tgkill(t.dbp.pid, t.ID, sys.SIGSTOP)
	if err != nil {
		err = fmt.Errorf("halt err %s on thread %d", err, t.ID)
		return
	}
	_, _, err = t.dbp.wait(t.ID, 0)
	if err != nil {
		err = fmt.Errorf("wait err %s on thread %d", err, t.ID)
		return
	}
	return
}

func (t *Thread) stopped() bool {
	if t.dbp.debugType == debugTypeCore {
		return true
	}
	state := status(t.ID, t.dbp.os.comm)
	return state == StatusTraceStop || state == StatusTraceStopT
}

func (t *Thread) resume() error {
	return t.resumeWithSig(0)
}

func (t *Thread) resumeWithSig(sig int) (err error) {
	if t.dbp.debugType == debugTypeCore {
		return errors.New("cannot change resume a thread in a core file")
	}

	t.running = true
	t.dbp.execPtraceFunc(func() { err = PtraceCont(t.ID, sig) })
	return
}

func (t *Thread) singleStep() (err error) {
	if t.dbp.debugType == debugTypeCore {
		return errors.New("cannot step in a core file")
	}

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
			return ProcessExitedError{Pid: t.dbp.pid, Status: rs}
		}
		if wpid == t.ID && status.StopSignal() == sys.SIGTRAP {
			return nil
		}
	}
}

func (t *Thread) blocked() bool {
	pc, _ := t.PC()
	fn := t.dbp.goSymTable.PCToFunc(pc)
	if fn != nil && ((fn.Name == "runtime.futex") || (fn.Name == "runtime.usleep") || (fn.Name == "runtime.clone")) {
		return true
	}
	return false
}

func (t *Thread) saveRegisters() (Registers, error) {
	if t.dbp.debugType == debugTypeCore {
		return &Regs{&t.dbp.os.core.Threads[t.ID].Reg, nil}, nil
	}

	var err error
	t.dbp.execPtraceFunc(func() { err = sys.PtraceGetRegs(t.ID, &t.os.registers) })
	if err != nil {
		return nil, fmt.Errorf("could not save register contents")
	}
	return &Regs{&t.os.registers, nil}, nil
}

func (t *Thread) restoreRegisters() (err error) {
	if t.dbp.debugType == debugTypeCore {
		return errors.New("cannot write registers of a core file")
	}

	t.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(t.ID, &t.os.registers) })
	return
}

func (t *Thread) writeMemory(addr uintptr, data []byte) (written int, err error) {
	if len(data) == 0 {
		return
	}

	if t.dbp.debugType == debugTypeCore {
		return 0, errors.New("cannot write memory to a core file")
	}

	t.dbp.execPtraceFunc(func() { written, err = sys.PtracePokeData(t.ID, addr, data) })
	return
}

func (t *Thread) readMemory(addr uintptr, size int) (data []byte, err error) {
	if size == 0 {
		return
	}

	data = make([]byte, size)

	if t.dbp.debugType == debugTypeCore {
		_, err = t.dbp.os.core.ReadMemory(data, addr)
		return
	}
	t.dbp.execPtraceFunc(func() { _, err = sys.PtracePeekData(t.ID, addr, data) })
	return
}
