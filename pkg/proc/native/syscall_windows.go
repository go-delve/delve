//go:generate go run $GOROOT/src/syscall/mksyscall_windows.go -output zsyscall_windows.go syscall_windows.go

package native

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

type _CREATE_PROCESS_DEBUG_INFO struct {
	File                syscall.Handle
	Process             syscall.Handle
	Thread              syscall.Handle
	BaseOfImage         uintptr
	DebugInfoFileOffset uint32
	DebugInfoSize       uint32
	ThreadLocalBase     uintptr
	StartAddress        uintptr
	ImageName           uintptr
	Unicode             uint16
}

type _CREATE_THREAD_DEBUG_INFO struct {
	Thread          syscall.Handle
	ThreadLocalBase uintptr
	StartAddress    uintptr
}

type _EXIT_PROCESS_DEBUG_INFO struct {
	ExitCode uint32
}

type _LOAD_DLL_DEBUG_INFO struct {
	File                syscall.Handle
	BaseOfDll           uintptr
	DebugInfoFileOffset uint32
	DebugInfoSize       uint32
	ImageName           uintptr
	Unicode             uint16
}

type _EXCEPTION_DEBUG_INFO struct {
	ExceptionRecord _EXCEPTION_RECORD
	FirstChance     uint32
}

type _EXCEPTION_RECORD struct {
	ExceptionCode        uint32
	ExceptionFlags       uint32
	ExceptionRecord      *_EXCEPTION_RECORD
	ExceptionAddress     uintptr
	NumberParameters     uint32
	ExceptionInformation [_EXCEPTION_MAXIMUM_PARAMETERS]uintptr
}

type _MEMORY_BASIC_INFORMATION struct {
	BaseAddress       uintptr
	AllocationBase    uintptr
	AllocationProtect uint32
	PartitionId       uint16
	RegionSize        uint64
	State             uint32
	Protect           uint32
	Type              uint32
}

const (
	_ThreadBasicInformation = 0

	_DBG_CONTINUE              = 0x00010002
	_DBG_EXCEPTION_NOT_HANDLED = 0x80010001

	_EXCEPTION_DEBUG_EVENT      = 1
	_CREATE_THREAD_DEBUG_EVENT  = 2
	_CREATE_PROCESS_DEBUG_EVENT = 3
	_EXIT_THREAD_DEBUG_EVENT    = 4
	_EXIT_PROCESS_DEBUG_EVENT   = 5
	_LOAD_DLL_DEBUG_EVENT       = 6
	_UNLOAD_DLL_DEBUG_EVENT     = 7
	_OUTPUT_DEBUG_STRING_EVENT  = 8
	_RIP_EVENT                  = 9

	// DEBUG_ONLY_THIS_PROCESS and _DEBUG_PROCESS tracks https://msdn.microsoft.com/en-us/library/windows/desktop/ms684863(v=vs.85).aspx
	_DEBUG_ONLY_THIS_PROCESS = 0x00000002
	_DEBUG_PROCESS           = 0x00000001

	_EXCEPTION_BREAKPOINT  = 0x80000003
	_EXCEPTION_SINGLE_STEP = 0x80000004

	_EXCEPTION_MAXIMUM_PARAMETERS = 15

	_MEM_FREE    = 0x10000
	_MEM_RESERVE = 0x2000

	_PAGE_EXECUTE           = 0x10
	_PAGE_EXECUTE_READ      = 0x20
	_PAGE_EXECUTE_READWRITE = 0x40
	_PAGE_EXECUTE_WRITECOPY = 0x80
	_PAGE_NOACCESS          = 0x01
	_PAGE_READONLY          = 0x02
	_PAGE_READWRITE         = 0x04
	_PAGE_WRITECOPY         = 0x08

	_PAGE_GUARD = 0x100
)

func _NT_SUCCESS(x _NTSTATUS) bool {
	return x >= 0
}

type _DEBUG_EVENT struct {
	DebugEventCode uint32
	ProcessId      uint32
	ThreadId       uint32
	_              uint32 // to align Union properly
	U              [160]byte
}

//sys	_NtQueryInformationThread(threadHandle syscall.Handle, infoclass int32, info uintptr, infolen uint32, retlen *uint32) (status _NTSTATUS) = ntdll.NtQueryInformationThread
//sys	_GetThreadContext(thread syscall.Handle, context *_CONTEXT) (err error) = kernel32.GetThreadContext
//sys	_SetThreadContext(thread syscall.Handle, context *_CONTEXT) (err error) = kernel32.SetThreadContext
//sys	_SuspendThread(threadid syscall.Handle) (prevsuspcount uint32, err error) [failretval==0xffffffff] = kernel32.SuspendThread
//sys	_ResumeThread(threadid syscall.Handle) (prevsuspcount uint32, err error) [failretval==0xffffffff] = kernel32.ResumeThread
//sys	_ContinueDebugEvent(processid uint32, threadid uint32, continuestatus uint32) (err error) = kernel32.ContinueDebugEvent
//sys	_WriteProcessMemory(process syscall.Handle, baseaddr uintptr, buffer *byte, size uintptr, byteswritten *uintptr) (err error) = kernel32.WriteProcessMemory
//sys	_ReadProcessMemory(process syscall.Handle, baseaddr uintptr, buffer *byte, size uintptr, bytesread *uintptr) (err error) = kernel32.ReadProcessMemory
//sys	_DebugBreakProcess(process syscall.Handle) (err error) = kernel32.DebugBreakProcess
//sys	_WaitForDebugEvent(debugevent *_DEBUG_EVENT, milliseconds uint32) (err error) = kernel32.WaitForDebugEvent
//sys	_DebugActiveProcess(processid uint32) (err error) = kernel32.DebugActiveProcess
//sys	_DebugActiveProcessStop(processid uint32) (err error) = kernel32.DebugActiveProcessStop
//sys	_QueryFullProcessImageName(process syscall.Handle, flags uint32, exename *uint16, size *uint32) (err error) = kernel32.QueryFullProcessImageNameW
//sys   _VirtualQueryEx(process syscall.Handle, addr uintptr, buffer *_MEMORY_BASIC_INFORMATION, length uintptr) (lengthOut uintptr) = kernel32.VirtualQueryEx
//sys   _IsWow64Process(process syscall.Handle, wow64process *uint32) (ok uint32) = kernel32.IsWow64Process
