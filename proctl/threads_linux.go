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
		return fmt.Errorf("Halt err %s %d", err, pid)
	}
	pid, _, err := wait(th.Id, sys.WNOHANG)
	if err != nil {
		return fmt.Errorf("wait err %s %d", err, pid)
	}
	return nil
}

func (t *ThreadContext) cont() error {
	return PtraceCont(thread.Id, 0)
}

func (t *ThreadContext) singleStep() error {
	err := sys.PtraceSingleStep(t.Id)
	if err != nill {
		return err
	}
	_, _, err = wait(thread.Id, 0)
	return err
}

func writeMemory(tid int, addr uintptr, data []byte) (int, error) {
	return sys.PtracePokeData(tid, addr, data)
}

func readMemory(tid int, addr uintptr, data []byte) (int, error) {
	return sys.PtracePeekData(tid, addr, data)
}
