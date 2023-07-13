package native

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	sys "golang.org/x/sys/windows"

	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/internal/ebpf"
)

// osProcessDetails holds Windows specific information.
type osProcessDetails struct {
	hProcess    syscall.Handle
	breakThread int
	entryPoint  uint64
	running     bool
}

func (os *osProcessDetails) Close() {}

// Launch creates and begins debugging a new process.
func Launch(cmd []string, wd string, flags proc.LaunchFlags, _ []string, _ string, stdinPath string, stdoutOR proc.OutputRedirect, stderrOR proc.OutputRedirect) (*proc.TargetGroup, error) {
	argv0Go := cmd[0]

	env := proc.DisableAsyncPreemptEnv()

	stdin, stdout, stderr, closefn, err := openRedirects(stdinPath, stdoutOR, stderrOR, true)
	if err != nil {
		return nil, err
	}

	creationFlags := uint32(_DEBUG_ONLY_THIS_PROCESS)
	if flags&proc.LaunchForeground == 0 {
		creationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
	}

	var p *os.Process
	dbp := newProcess(0)
	dbp.execPtraceFunc(func() {
		attr := &os.ProcAttr{
			Dir:   wd,
			Files: []*os.File{stdin, stdout, stderr},
			Sys: &syscall.SysProcAttr{
				CreationFlags: creationFlags,
			},
			Env: env,
		}
		p, err = os.StartProcess(argv0Go, cmd, attr)
	})
	closefn()
	if err != nil {
		return nil, err
	}
	defer p.Release()

	dbp.pid = p.Pid
	dbp.childProcess = true

	tgt, err := dbp.initialize(argv0Go, []string{})
	if err != nil {
		detachWithoutGroup(dbp, true)
		return nil, err
	}
	return tgt, nil
}

func initialize(dbp *nativeProcess) (string, error) {
	// It should not actually be possible for the
	// call to waitForDebugEvent to fail, since Windows
	// will always fire a CREATE_PROCESS_DEBUG_EVENT event
	// immediately after launching under DEBUG_ONLY_THIS_PROCESS.
	// Attaching with DebugActiveProcess has similar effect.
	var err error
	var tid, exitCode int
	dbp.execPtraceFunc(func() {
		tid, exitCode, err = dbp.waitForDebugEvent(waitBlocking)
	})
	if err != nil {
		return "", err
	}
	if tid == 0 {
		dbp.postExit()
		return "", proc.ErrProcessExited{Pid: dbp.pid, Status: exitCode}
	}

	cmdline := getCmdLine(dbp.os.hProcess)

	// Suspend all threads so that the call to _ContinueDebugEvent will
	// not resume the target.
	for _, thread := range dbp.threads {
		if !thread.os.dbgUiRemoteBreakIn {
			_, err := _SuspendThread(thread.os.hThread)
			if err != nil {
				return "", err
			}
		}
	}

	dbp.execPtraceFunc(func() {
		err = _ContinueDebugEvent(uint32(dbp.pid), uint32(dbp.os.breakThread), _DBG_CONTINUE)
	})
	return cmdline, err
}

// findExePath searches for process pid, and returns its executable path
func findExePath(pid int) (string, error) {
	// Original code suggested different approach (see below).
	// Maybe it could be useful in the future.
	//
	// Find executable path from PID/handle on Windows:
	// https://msdn.microsoft.com/en-us/library/aa366789(VS.85).aspx

	p, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer syscall.CloseHandle(p)

	n := uint32(128)
	for {
		buf := make([]uint16, int(n))
		err = _QueryFullProcessImageName(p, 0, &buf[0], &n)
		switch err {
		case syscall.ERROR_INSUFFICIENT_BUFFER:
			// try bigger buffer
			n *= 2
			// but stop if it gets too big
			if n > 10000 {
				return "", err
			}
		case nil:
			return syscall.UTF16ToString(buf[:n]), nil
		default:
			return "", err
		}
	}
}

var debugPrivilegeRequested = false

// Attach to an existing process with the given PID.
func Attach(pid int, waitFor *proc.WaitFor, _ []string) (*proc.TargetGroup, error) {
	var aperr error
	if !debugPrivilegeRequested {
		debugPrivilegeRequested = true
		// The following call will only work if the user is an administrator
		// has the "Debug Programs" privilege in Local security settings.
		// Since this privilege is not needed to debug processes owned by the
		// current user, do not complain about this unless attach actually fails.
		aperr = acquireDebugPrivilege()
	}

	if waitFor.Valid() {
		var err error
		pid, err = WaitFor(waitFor)
		if err != nil {
			return nil, err
		}
	}

	dbp := newProcess(pid)
	var err error
	dbp.execPtraceFunc(func() {
		// TODO: Probably should have SeDebugPrivilege before starting here.
		err = _DebugActiveProcess(uint32(pid))
	})
	if err != nil {
		if aperr != nil {
			return nil, fmt.Errorf("%v also %v", err, aperr)
		}
		return nil, err
	}
	exepath, err := findExePath(pid)
	if err != nil {
		return nil, err
	}
	tgt, err := dbp.initialize(exepath, []string{})
	if err != nil {
		detachWithoutGroup(dbp, true)
		return nil, err
	}
	return tgt, nil
}

// acquireDebugPrivilege acquires the debug privilege which is needed to
// debug other user's processes.
// See:
//
//   - https://learn.microsoft.com/en-us/windows-hardware/drivers/debugger/debug-privilege
//   - https://github.com/go-delve/delve/issues/3136
func acquireDebugPrivilege() error {
	var token sys.Token
	err := sys.OpenProcessToken(sys.CurrentProcess(), sys.TOKEN_QUERY|sys.TOKEN_ADJUST_PRIVILEGES, &token)
	if err != nil {
		return fmt.Errorf("could not acquire debug privilege (OpenCurrentProcessToken): %v", err)
	}
	defer token.Close()

	privName, _ := sys.UTF16FromString("SeDebugPrivilege")
	var luid sys.LUID
	err = sys.LookupPrivilegeValue(nil, &privName[0], &luid)
	if err != nil {
		return fmt.Errorf("could not acquire debug privilege  (LookupPrivilegeValue): %v", err)
	}

	var tp sys.Tokenprivileges
	tp.PrivilegeCount = 1
	tp.Privileges[0].Luid = luid
	tp.Privileges[0].Attributes = sys.SE_PRIVILEGE_ENABLED

	err = sys.AdjustTokenPrivileges(token, false, &tp, 0, nil, nil)
	if err != nil {
		return fmt.Errorf("could not acquire debug privilege (AdjustTokenPrivileges): %v", err)
	}

	return nil
}

func waitForSearchProcess(pfx string, seen map[int]struct{}) (int, error) {
	log := logflags.DebuggerLogger()
	handle, err := sys.CreateToolhelp32Snapshot(sys.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, fmt.Errorf("could not get process list: %v", err)
	}
	defer sys.CloseHandle(handle)

	var entry sys.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	err = sys.Process32First(handle, &entry)
	if err != nil {
		return 0, fmt.Errorf("could not get process list: %v", err)
	}

	for err = sys.Process32First(handle, &entry); err == nil; err = sys.Process32Next(handle, &entry) {
		if _, isseen := seen[int(entry.ProcessID)]; isseen {
			continue
		}
		seen[int(entry.ProcessID)] = struct{}{}

		hProcess, err := sys.OpenProcess(sys.PROCESS_QUERY_INFORMATION|sys.PROCESS_VM_READ, false, entry.ProcessID)
		if err != nil {
			continue
		}
		cmdline := getCmdLine(syscall.Handle(hProcess))
		sys.CloseHandle(hProcess)

		log.Debugf("waitfor: new process %q", cmdline)
		if strings.HasPrefix(cmdline, pfx) {
			return int(entry.ProcessID), nil
		}
	}

	return 0, nil
}

// kill kills the process.
func (procgrp *processGroup) kill(dbp *nativeProcess) error {
	if dbp.exited {
		return nil
	}

	p, err := os.FindProcess(dbp.pid)
	if err != nil {
		return err
	}
	defer p.Release()

	// TODO: Should not have to ignore failures here,
	// but some tests appear to Kill twice causing
	// this to fail on second attempt.
	_ = syscall.TerminateProcess(dbp.os.hProcess, 1)

	dbp.execPtraceFunc(func() {
		dbp.waitForDebugEvent(waitBlocking | waitDontHandleExceptions)
	})

	p.Wait()

	dbp.postExit()
	return nil
}

func (dbp *nativeProcess) requestManualStop() error {
	if !dbp.os.running {
		return nil
	}
	dbp.os.running = false
	return _DebugBreakProcess(dbp.os.hProcess)
}

func (dbp *nativeProcess) updateThreadList() error {
	// We ignore this request since threads are being
	// tracked as they are created/killed in waitForDebugEvent.
	return nil
}

func (dbp *nativeProcess) addThread(hThread syscall.Handle, threadID int, attach, suspendNewThreads bool, dbgUiRemoteBreakIn bool) (*nativeThread, error) {
	if thread, ok := dbp.threads[threadID]; ok {
		return thread, nil
	}
	thread := &nativeThread{
		ID:  threadID,
		dbp: dbp,
		os:  new(osSpecificDetails),
	}
	thread.os.dbgUiRemoteBreakIn = dbgUiRemoteBreakIn
	thread.os.hThread = hThread
	dbp.threads[threadID] = thread
	if dbp.memthread == nil {
		dbp.memthread = dbp.threads[threadID]
	}
	if suspendNewThreads && !dbgUiRemoteBreakIn {
		_, err := _SuspendThread(thread.os.hThread)
		if err != nil {
			return nil, err
		}
	}

	for _, bp := range dbp.Breakpoints().M {
		if bp.WatchType != 0 {
			err := thread.writeHardwareBreakpoint(bp.Addr, bp.WatchType, bp.HWBreakIndex)
			if err != nil {
				return nil, err
			}
		}
	}

	return thread, nil
}

type waitForDebugEventFlags int

const (
	waitBlocking waitForDebugEventFlags = 1 << iota
	waitSuspendNewThreads
	waitDontHandleExceptions
)

const _MS_VC_EXCEPTION = 0x406D1388 // part of VisualC protocol to set thread names

func (dbp *nativeProcess) waitForDebugEvent(flags waitForDebugEventFlags) (threadID, exitCode int, err error) {
	var debugEvent _DEBUG_EVENT
	shouldExit := false
	for {
		continueStatus := uint32(_DBG_CONTINUE)
		var milliseconds uint32 = 0
		if flags&waitBlocking != 0 {
			milliseconds = syscall.INFINITE
		}
		// Wait for a debug event...
		err := _WaitForDebugEvent(&debugEvent, milliseconds)
		if err != nil {
			return 0, 0, err
		}

		// ... handle each event kind ...
		unionPtr := unsafe.Pointer(&debugEvent.U[0])
		switch debugEvent.DebugEventCode {
		case _CREATE_PROCESS_DEBUG_EVENT:
			debugInfo := (*_CREATE_PROCESS_DEBUG_INFO)(unionPtr)
			hFile := debugInfo.File
			if hFile != 0 && hFile != syscall.InvalidHandle {
				err = syscall.CloseHandle(hFile)
				if err != nil {
					return 0, 0, err
				}
			}
			dbp.os.entryPoint = uint64(debugInfo.BaseOfImage)
			dbp.os.hProcess = debugInfo.Process
			_, err = dbp.addThread(debugInfo.Thread, int(debugEvent.ThreadId), false,
				flags&waitSuspendNewThreads != 0, debugInfo.StartAddress == dbgUiRemoteBreakin.Addr())
			if err != nil {
				return 0, 0, err
			}
			break
		case _CREATE_THREAD_DEBUG_EVENT:
			debugInfo := (*_CREATE_THREAD_DEBUG_INFO)(unionPtr)
			_, err = dbp.addThread(debugInfo.Thread, int(debugEvent.ThreadId), false,
				flags&waitSuspendNewThreads != 0, debugInfo.StartAddress == dbgUiRemoteBreakin.Addr())
			if err != nil {
				return 0, 0, err
			}
			break
		case _EXIT_THREAD_DEBUG_EVENT:
			delete(dbp.threads, int(debugEvent.ThreadId))
			break
		case _OUTPUT_DEBUG_STRING_EVENT:
			//TODO: Handle debug output strings
			break
		case _LOAD_DLL_DEBUG_EVENT:
			debugInfo := (*_LOAD_DLL_DEBUG_INFO)(unionPtr)
			hFile := debugInfo.File
			if hFile != 0 && hFile != syscall.InvalidHandle {
				err = syscall.CloseHandle(hFile)
				if err != nil {
					return 0, 0, err
				}
			}
			break
		case _UNLOAD_DLL_DEBUG_EVENT:
			break
		case _RIP_EVENT:
			break
		case _EXCEPTION_DEBUG_EVENT:
			if flags&waitDontHandleExceptions != 0 {
				continueStatus = _DBG_EXCEPTION_NOT_HANDLED
				break
			}
			exception := (*_EXCEPTION_DEBUG_INFO)(unionPtr)
			tid := int(debugEvent.ThreadId)

			switch code := exception.ExceptionRecord.ExceptionCode; code {
			case _EXCEPTION_BREAKPOINT:

				// check if the exception address really is a breakpoint instruction, if
				// it isn't we already removed that breakpoint and we can't deal with
				// this exception anymore.
				atbp := true
				if thread, found := dbp.threads[tid]; found {
					data := make([]byte, dbp.bi.Arch.BreakpointSize())
					if _, err := thread.ReadMemory(data, uint64(exception.ExceptionRecord.ExceptionAddress)); err == nil {
						instr := dbp.bi.Arch.BreakpointInstruction()
						for i := range instr {
							if data[i] != instr[i] {
								atbp = false
								break
							}
						}
					}
					if !atbp {
						thread.setPC(uint64(exception.ExceptionRecord.ExceptionAddress))
					}
				}

				if atbp {
					dbp.os.breakThread = tid
					if th := dbp.threads[tid]; th != nil {
						th.os.setbp = true
					}
					return tid, 0, nil
				} else {
					continueStatus = _DBG_CONTINUE
				}
			case _EXCEPTION_SINGLE_STEP:
				dbp.os.breakThread = tid
				return tid, 0, nil
			case _MS_VC_EXCEPTION:
				// This exception is sent to set the thread name in VisualC, we should
				// mask it or it might crash the program.
				continueStatus = _DBG_CONTINUE
			default:
				continueStatus = _DBG_EXCEPTION_NOT_HANDLED
			}
		case _EXIT_PROCESS_DEBUG_EVENT:
			debugInfo := (*_EXIT_PROCESS_DEBUG_INFO)(unionPtr)
			exitCode = int(debugInfo.ExitCode)
			shouldExit = true
		default:
			return 0, 0, fmt.Errorf("unknown debug event code: %d", debugEvent.DebugEventCode)
		}

		// .. and then continue unless we received an event that indicated we should break into debugger.
		err = _ContinueDebugEvent(debugEvent.ProcessId, debugEvent.ThreadId, continueStatus)
		if err != nil {
			return 0, 0, err
		}

		if shouldExit {
			return 0, exitCode, nil
		}
	}
}

func trapWait(procgrp *processGroup, pid int) (*nativeThread, error) {
	dbp := procgrp.procs[0]
	var err error
	var tid, exitCode int
	dbp.execPtraceFunc(func() {
		tid, exitCode, err = dbp.waitForDebugEvent(waitBlocking)
	})
	if err != nil {
		return nil, err
	}
	if tid == 0 {
		dbp.postExit()
		return nil, proc.ErrProcessExited{Pid: dbp.pid, Status: exitCode}
	}
	th := dbp.threads[tid]
	return th, nil
}

func (dbp *nativeProcess) exitGuard(err error) error {
	return err
}

func (procgrp *processGroup) resume() error {
	dbp := procgrp.procs[0]
	for _, thread := range dbp.threads {
		if thread.CurrentBreakpoint.Breakpoint != nil {
			if err := procgrp.stepInstruction(thread); err != nil {
				return err
			}
			thread.CurrentBreakpoint.Clear()
		}
	}

	for _, thread := range dbp.threads {
		_, err := _ResumeThread(thread.os.hThread)
		if err != nil {
			return err
		}
	}
	dbp.os.running = true

	return nil
}

// stop stops all running threads threads and sets breakpoints
func (procgrp *processGroup) stop(cctx *proc.ContinueOnceContext, trapthread *nativeThread) (*nativeThread, error) {
	dbp := procgrp.procs[0]
	if dbp.exited {
		return nil, proc.ErrProcessExited{Pid: dbp.pid}
	}

	dbp.os.running = false
	for _, th := range dbp.threads {
		th.os.setbp = false
	}
	trapthread.os.setbp = true

	// While the debug event that stopped the target was being propagated
	// other target threads could generate other debug events.
	// After this function we need to know about all the threads
	// stopped on a breakpoint. To do that we first suspend all target
	// threads and then repeatedly call _ContinueDebugEvent followed by
	// waitForDebugEvent in non-blocking mode.
	// We need to explicitly call SuspendThread because otherwise the
	// call to _ContinueDebugEvent will resume execution of some of the
	// target threads.

	err := trapthread.SetCurrentBreakpoint(true)
	if err != nil {
		return nil, err
	}

	context := newContext()

	for _, thread := range dbp.threads {
		thread.os.delayErr = nil
		if !thread.os.dbgUiRemoteBreakIn {
			// Wait before reporting the error, the thread could be removed when we
			// call waitForDebugEvent in the next loop.
			_, thread.os.delayErr = _SuspendThread(thread.os.hThread)
			if thread.os.delayErr == nil {
				// This call will block until the thread has stopped.
				_ = thread.getContext(context)
			}
		}
	}

	for {
		var err error
		var tid int
		dbp.execPtraceFunc(func() {
			err = _ContinueDebugEvent(uint32(dbp.pid), uint32(dbp.os.breakThread), _DBG_CONTINUE)
			if err == nil {
				tid, _, _ = dbp.waitForDebugEvent(waitSuspendNewThreads)
			}
		})
		if err != nil {
			return nil, err
		}
		if tid == 0 {
			break
		}
		err = dbp.threads[tid].SetCurrentBreakpoint(true)
		if err != nil {
			return nil, err
		}
	}

	// Check if trapthread still exist, if the process is dying it could have
	// been removed while we were stopping the other threads.
	trapthreadFound := false
	for _, thread := range dbp.threads {
		if thread.ID == trapthread.ID {
			trapthreadFound = true
		}
		if thread.os.delayErr != nil && thread.os.delayErr != syscall.Errno(0x5) {
			// Do not report Access is denied error, it is caused by the thread
			// having already died but we haven't been notified about it yet.
			return nil, thread.os.delayErr
		}
	}

	if !trapthreadFound {
		wasDbgUiRemoteBreakIn := trapthread.os.dbgUiRemoteBreakIn
		// trapthread exited during stop, pick another one
		trapthread = nil
		for _, thread := range dbp.threads {
			if thread.CurrentBreakpoint.Breakpoint != nil && thread.os.delayErr == nil {
				trapthread = thread
				break
			}
		}
		if trapthread == nil && wasDbgUiRemoteBreakIn {
			// If this was triggered by a manual stop request we should stop
			// regardless, pick a thread.
			for _, thread := range dbp.threads {
				return thread, nil
			}
		}
	}

	return trapthread, nil
}

func (dbp *nativeProcess) detach(kill bool) error {
	if !kill {
		//TODO(aarzilli): when debug.Target exist Detach should be moved to
		// debug.Target and the call to RestoreAsyncPreempt should be moved there.
		for _, thread := range dbp.threads {
			_, err := _ResumeThread(thread.os.hThread)
			if err != nil {
				return err
			}
		}
	}
	return _DebugActiveProcessStop(uint32(dbp.pid))
}

func (dbp *nativeProcess) EntryPoint() (uint64, error) {
	return dbp.os.entryPoint, nil
}

func (dbp *nativeProcess) SupportsBPF() bool {
	return false
}

func (dbp *nativeProcess) SetUProbe(fnName string, goidOffset int64, args []ebpf.UProbeArgMap) error {
	return nil
}

func (dbp *nativeProcess) GetBufferedTracepoints() []ebpf.RawUProbeParams {
	return nil
}

type _PROCESS_BASIC_INFORMATION struct {
	ExitStatus                   sys.NTStatus
	PebBaseAddress               uintptr
	AffinityMask                 uintptr
	BasePriority                 int32
	UniqueProcessId              uintptr
	InheritedFromUniqueProcessId uintptr
}

type _PEB struct {
	reserved1              [2]byte
	BeingDebugged          byte
	BitField               byte
	reserved3              uintptr
	ImageBaseAddress       uintptr
	Ldr                    uintptr
	ProcessParameters      uintptr
	reserved4              [3]uintptr
	AtlThunkSListPtr       uintptr
	reserved5              uintptr
	reserved6              uint32
	reserved7              uintptr
	reserved8              uint32
	AtlThunkSListPtr32     uint32
	reserved9              [45]uintptr
	reserved10             [96]byte
	PostProcessInitRoutine uintptr
	reserved11             [128]byte
	reserved12             [1]uintptr
	SessionId              uint32
}

type _RTL_USER_PROCESS_PARAMETERS struct {
	MaximumLength, Length uint32

	Flags, DebugFlags uint32

	ConsoleHandle                                sys.Handle
	ConsoleFlags                                 uint32
	StandardInput, StandardOutput, StandardError sys.Handle

	CurrentDirectory struct {
		DosPath _NTUnicodeString
		Handle  sys.Handle
	}

	DllPath       _NTUnicodeString
	ImagePathName _NTUnicodeString
	CommandLine   _NTUnicodeString
	Environment   unsafe.Pointer

	StartingX, StartingY, CountX, CountY, CountCharsX, CountCharsY, FillAttribute uint32

	WindowFlags, ShowWindowFlags                     uint32
	WindowTitle, DesktopInfo, ShellInfo, RuntimeData _NTUnicodeString
	CurrentDirectories                               [32]struct {
		Flags     uint16
		Length    uint16
		TimeStamp uint32
		DosPath   _NTString
	}

	EnvironmentSize, EnvironmentVersion uintptr

	PackageDependencyData uintptr
	ProcessGroupId        uint32
	LoaderThreads         uint32

	RedirectionDllName               _NTUnicodeString
	HeapPartitionName                _NTUnicodeString
	DefaultThreadpoolCpuSetMasks     uintptr
	DefaultThreadpoolCpuSetMaskCount uint32
}

type _NTString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        uintptr
}

type _NTUnicodeString struct {
	Length        uint16
	MaximumLength uint16
	Buffer        uintptr
}

func getCmdLine(hProcess syscall.Handle) string {
	logger := logflags.DebuggerLogger()
	var info _PROCESS_BASIC_INFORMATION
	err := sys.NtQueryInformationProcess(sys.Handle(hProcess), sys.ProcessBasicInformation, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), nil)
	if err != nil {
		logger.Errorf("NtQueryInformationProcess: %v", err)
		return ""
	}
	var peb _PEB
	err = _ReadProcessMemory(hProcess, info.PebBaseAddress, (*byte)(unsafe.Pointer(&peb)), unsafe.Sizeof(peb), nil)
	if err != nil {
		logger.Errorf("Reading PEB: %v", err)
		return ""
	}
	var upp _RTL_USER_PROCESS_PARAMETERS
	err = _ReadProcessMemory(hProcess, peb.ProcessParameters, (*byte)(unsafe.Pointer(&upp)), unsafe.Sizeof(upp), nil)
	if err != nil {
		logger.Errorf("Reading ProcessParameters: %v", err)
		return ""
	}
	if upp.CommandLine.Length%2 != 0 {
		logger.Errorf("CommandLine length not a multiple of 2")
		return ""
	}
	buf := make([]byte, upp.CommandLine.Length)
	err = _ReadProcessMemory(hProcess, upp.CommandLine.Buffer, &buf[0], uintptr(len(buf)), nil)
	if err != nil {
		logger.Errorf("Reading CommandLine: %v", err)
		return ""
	}
	utf16buf := make([]uint16, len(buf)/2)
	for i := 0; i < len(buf); i += 2 {
		utf16buf[i/2] = uint16(buf[i+1])<<8 + uint16(buf[i])
	}
	return string(utf16.Decode(utf16buf))
}

func killProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	defer p.Release()

	return p.Kill()
}
