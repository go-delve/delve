package proc

import (
	"fmt"
)

func PtraceAttach(pid int) error {
	return fmt.Errorf("not implemented: PtraceAttach")
}

func PtraceDetach(tid, sig int) error {
	return _DebugActiveProcessStop(uint32(tid))
}
