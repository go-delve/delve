//go:generate go run $GOROOT/src/syscall/mksyscall_windows.go -output zsyscall_windows.go syscall_windows.go

package proc

import (
	"syscall"
)

type _NTSTATUS int32

type _CLIENT_ID struct {
	UniqueProcess syscall.Handle
	UniqueThread  syscall.Handle
}

type _THREAD_BASIC_INFORMATION struct {
	ExitStatus     _NTSTATUS
	TebBaseAddress uintptr
	ClientId       _CLIENT_ID
	AffinityMask   uintptr
	Priority       int32
	BasePriority   int32
}

const (
	ThreadBasicInformation = 0
)

func _NT_SUCCESS(x _NTSTATUS) bool {
	return x >= 0
}

//sys	_NtQueryInformationThread(threadHandle syscall.Handle, infoclass int32, info uintptr, infolen uint32, retlen *uint32) (status _NTSTATUS) = ntdll.NtQueryInformationThread
