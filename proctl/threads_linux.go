package proctl

import (
	"fmt"

	sys "golang.org/x/sys/unix"
)

// Not actually used, but necessary
// to be defined.
type OSSpecificDetails interface{}

func (t *ThreadContext) Halt() error {
	if stopped(t.Id) {
		return nil
	}
	err := sys.Tgkill(t.Process.Pid, t.Id, sys.SIGSTOP)
	if err != nil {
		return fmt.Errorf("Halt err %s %d", err, t.Id)
	}
	_, _, err = wait(t.Id, 0)
	if err != nil {
		return fmt.Errorf("wait err %s %d", err, t.Id)
	}
	return nil
}

func (t *ThreadContext) resume() error {
	return PtraceCont(t.Id, 0)
}

func (t *ThreadContext) singleStep() error {
	err := sys.PtraceSingleStep(t.Id)
	if err != nil {
		return err
	}
	_, _, err = wait(t.Id, 0)
	return err
}

func (t *ThreadContext) blocked() bool {
	// TODO(dp) cache the func pc to remove this lookup
	pc, _ := t.CurrentPC()
	fn := t.Process.GoSymTable.PCToFunc(pc)
	if fn != nil && ((fn.Name == "runtime.futex") || (fn.Name == "runtime.usleep") || (fn.Name == "runtime.clone")) {
		return true
	}
	return false
}

func writeMemory(thread *ThreadContext, addr uintptr, data []byte) (int, error) {
	return sys.PtracePokeData(thread.Id, addr, data)
}

func readMemory(thread *ThreadContext, addr uintptr, data []byte) (int, error) {
	return sys.PtracePeekData(thread.Id, addr, data)
}
