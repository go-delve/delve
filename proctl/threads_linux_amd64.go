package proctl

import "syscall"

func WriteMemory(tid int, addr uintptr, data []byte) (int, error) {
	return syscall.PtracePokeData(tid, addr, data)
}

func ReadMemory(tid int, addr uintptr, data []byte) (int, error) {
	return syscall.PtracePeekData(tid, addr, data)
}
