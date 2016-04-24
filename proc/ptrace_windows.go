package proc

import "errors"

func PtraceAttach(pid int) error {
	return errors.New("not implemented: PtraceAttach")
}

func PtraceDetach(tid, sig int) error {
	return errors.New("not implemented: PtraceDetach")
}
