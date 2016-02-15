package proc

import (
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
	err = sys.Tgkill(t.dbp.Pid, t.ID, sys.SIGSTOP)
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
	state := status(t.ID, t.dbp.os.comm)
	return state == StatusTraceStop
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
		wpid, status, err := t.dbp.wait(t.ID, 0)
		if err != nil {
			return err
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
	var err error
	t.dbp.execPtraceFunc(func() { err = sys.PtraceGetRegs(t.ID, &t.os.registers) })
	if err != nil {
		return nil, fmt.Errorf("could not save register contents")
	}
	return &Regs{&t.os.registers}, nil
}

func (t *Thread) restoreRegisters() (err error) {
	t.dbp.execPtraceFunc(func() { err = sys.PtraceSetRegs(t.ID, &t.os.registers) })
	return
}

func (t *Thread) writeMemory(addr uintptr, data []byte) (written int, err error) {
	if len(data) == 0 {
		return
	}
	t.dbp.execPtraceFunc(func() { written, err = sys.PtracePokeData(t.ID, addr, data) })
	return
}

func (t *Thread) readMemory(addr uintptr, size int) (data []byte, err error) {
	if size == 0 {
		return
	}
	data = make([]byte, size)
	t.dbp.execPtraceFunc(func() { _, err = sys.PtracePeekData(t.ID, addr, data) })
	return
}
