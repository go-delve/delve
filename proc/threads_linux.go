package proc

import (
	"fmt"

	sys "golang.org/x/sys/unix"
)

// Not actually used, but necessary
// to be defined.
type OSSpecificDetails struct {
	registers sys.PtraceRegs
}

func (t *Thread) Halt() error {
	if stopped(t.Id) {
		return nil
	}
	err := sys.Tgkill(t.dbp.Pid, t.Id, sys.SIGSTOP)
	if err != nil {
		return fmt.Errorf("Halt err %s %d", err, t.Id)
	}
	_, _, err = wait(t.Id, 0)
	if err != nil {
		return fmt.Errorf("wait err %s %d", err, t.Id)
	}
	return nil
}

func (t *Thread) resume() error {
	return PtraceCont(t.Id, 0)
}

func (t *Thread) singleStep() error {
	err := sys.PtraceSingleStep(t.Id)
	if err != nil {
		return err
	}
	_, _, err = wait(t.Id, 0)
	return err
}

func (t *Thread) blocked() bool {
	// TODO(dp) cache the func pc to remove this lookup
	pc, _ := t.PC()
	fn := t.dbp.goSymTable.PCToFunc(pc)
	if fn != nil && ((fn.Name == "runtime.futex") || (fn.Name == "runtime.usleep") || (fn.Name == "runtime.clone")) {
		return true
	}
	return false
}

func (thread *Thread) saveRegisters() (Registers, error) {
	if err := sys.PtraceGetRegs(thread.Id, &thread.os.registers); err != nil {
		return nil, fmt.Errorf("could not save register contents")
	}
	return &Regs{&thread.os.registers}, nil
}

func (thread *Thread) restoreRegisters() error {
	return sys.PtraceSetRegs(thread.Id, &thread.os.registers)
}

func writeMemory(thread *Thread, addr uintptr, data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	return sys.PtracePokeData(thread.Id, addr, data)
}

func readMemory(thread *Thread, addr uintptr, data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	return sys.PtracePeekData(thread.Id, addr, data)
}
