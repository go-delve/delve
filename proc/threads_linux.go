package proc

import (
	"fmt"

	sys "golang.org/x/sys/unix"
)

// OSSpecificDetails hold Linux specific
// process details.
type OSSpecificDetails struct {
	registers sys.PtraceRegs
}

func (t *Thread) halt() error {
	err := sys.Tgkill(t.p.Pid, t.ID, sys.SIGSTOP)
	if err != nil {
		return fmt.Errorf("halt err %s on thread %d for process %d", err, t.ID, t.p.Pid)
	}
	_, _, err = wait(t.p, t.ID)
	if err != nil && err != sys.ECHILD {
		return err
	}
	return nil
}

func (t *Thread) stopped() bool {
	state := status(t.ID, t.p.os.comm)
	return state == StatusTraceStop || state == StatusTraceStopT
}

func (t *Thread) resume() error {
	return t.resumeWithSig(0)
}

func (t *Thread) resumeWithSig(sig int) error {
	t.running = true
	return PtraceCont(t.ID, sig)
}

func (t *Thread) singleStep() error {
	for {
		err := PtraceSingleStep(t.ID)
		if err != nil {
			return err
		}
		// TODO(derekparker) consolidate all wait calls into threadResume
		status, trapthread, err := wait(t.p, t.ID)
		if err != nil {
			return err
		}
		if status.Exited() {
			_, err := Mourn(t.p)
			if err != nil {
				return err
			}
			rs := 0
			if status != nil {
				rs = status.ExitStatus()
			}
			return ProcessExitedError{Pid: t.p.Pid, Status: rs}
		}
		if trapthread.ID == t.ID && status.Trap() {
			return nil
		}
	}
}

func (t *Thread) blocked() bool {
	pc, _ := t.PC()
	fn := t.p.Dwarf.PCToFunc(pc)
	if fn != nil && ((fn.Name == "runtime.futex") || (fn.Name == "runtime.usleep") || (fn.Name == "runtime.clone")) {
		return true
	}
	return false
}

func (t *Thread) saveRegisters() (Registers, error) {
	var err error
	execOnPtraceThread(func() { err = sys.PtraceGetRegs(t.ID, &t.os.registers) })
	if err != nil {
		return nil, fmt.Errorf("could not save register contents")
	}
	return &Regs{&t.os.registers}, nil
}

func (t *Thread) restoreRegisters() (err error) {
	execOnPtraceThread(func() { err = sys.PtraceSetRegs(t.ID, &t.os.registers) })
	return
}

func (t *Thread) writeMemory(addr uintptr, data []byte) (written int, err error) {
	if len(data) == 0 {
		return
	}
	execOnPtraceThread(func() { written, err = sys.PtracePokeData(t.p.Pid, addr, data) })
	return
}

func (t *Thread) readMemory(addr uintptr, size int) (data []byte, err error) {
	if size == 0 {
		return
	}
	data = make([]byte, size)
	execOnPtraceThread(func() { _, err = sys.PtracePeekData(t.p.Pid, addr, data) })
	return
}
