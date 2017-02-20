package proc

import (
	"debug/gosym"
	"debug/pe"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/windows"

	"github.com/derekparker/delve/pkg/dwarf/frame"
	"github.com/derekparker/delve/pkg/dwarf/line"
	"golang.org/x/debug/dwarf"
)

// OSProcessDetails holds Windows specific information.
type OSProcessDetails struct {
	hProcess    syscall.Handle
	breakThread int
}

// Launch creates and begins debugging a new process.
func Launch(cmd []string, wd string) (*Process, error) {
	argv0Go, err := filepath.Abs(cmd[0])
	if err != nil {
		return nil, err
	}

	// Make sure the binary exists and is an executable file
	if filepath.Base(cmd[0]) == cmd[0] {
		if _, err := exec.LookPath(cmd[0]); err != nil {
			return nil, err
		}
	}

	peFile, err := openExecutablePath(argv0Go)
	if err != nil {
		return nil, NotExecutableErr
	}
	peFile.Close()

	// Duplicate the stdin/stdout/stderr handles
	files := []uintptr{uintptr(syscall.Stdin), uintptr(syscall.Stdout), uintptr(syscall.Stderr)}
	p, _ := syscall.GetCurrentProcess()
	fd := make([]syscall.Handle, len(files))
	for i := range files {
		err := syscall.DuplicateHandle(p, syscall.Handle(files[i]), p, &fd[i], 0, true, syscall.DUPLICATE_SAME_ACCESS)
		if err != nil {
			return nil, err
		}
		defer syscall.CloseHandle(syscall.Handle(fd[i]))
	}

	argv0, err := syscall.UTF16PtrFromString(argv0Go)
	if err != nil {
		return nil, err
	}

	// create suitable command line for CreateProcess
	// see https://github.com/golang/go/blob/master/src/syscall/exec_windows.go#L326
	// adapted from standard library makeCmdLine
	// see https://github.com/golang/go/blob/master/src/syscall/exec_windows.go#L86
	var cmdLineGo string
	if len(cmd) >= 1 {
		for _, v := range cmd {
			if cmdLineGo != "" {
				cmdLineGo += " "
			}
			cmdLineGo += syscall.EscapeArg(v)
		}
	}

	var cmdLine *uint16
	if cmdLineGo != "" {
		if cmdLine, err = syscall.UTF16PtrFromString(cmdLineGo); err != nil {
			return nil, err
		}
	}

	var workingDir *uint16
	if wd != "" {
		if workingDir, err = syscall.UTF16PtrFromString(wd); err != nil {
			return nil, err
		}
	}

	// Initialize the startup info and create process
	si := new(sys.StartupInfo)
	si.Cb = uint32(unsafe.Sizeof(*si))
	si.Flags = syscall.STARTF_USESTDHANDLES
	si.StdInput = sys.Handle(fd[0])
	si.StdOutput = sys.Handle(fd[1])
	si.StdErr = sys.Handle(fd[2])
	pi := new(sys.ProcessInformation)

	dbp := New(0)
	dbp.execPtraceFunc(func() {
		if wd == "" {
			err = sys.CreateProcess(argv0, cmdLine, nil, nil, true, _DEBUG_ONLY_THIS_PROCESS, nil, nil, si, pi)
		} else {
			err = sys.CreateProcess(argv0, cmdLine, nil, nil, true, _DEBUG_ONLY_THIS_PROCESS, nil, workingDir, si, pi)
		}
	})
	if err != nil {
		return nil, err
	}
	sys.CloseHandle(sys.Handle(pi.Process))
	sys.CloseHandle(sys.Handle(pi.Thread))

	dbp.pid = int(pi.ProcessId)

	return newDebugProcess(dbp, argv0Go)
}

// newDebugProcess prepares process pid for debugging.
func newDebugProcess(dbp *Process, exepath string) (*Process, error) {
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
		return nil, err
	}
	if tid == 0 {
		dbp.postExit()
		return nil, ProcessExitedError{Pid: dbp.pid, Status: exitCode}
	}
	// Suspend all threads so that the call to _ContinueDebugEvent will
	// not resume the target.
	for _, thread := range dbp.threads {
		_, err := _SuspendThread(thread.os.hThread)
		if err != nil {
			return nil, err
		}
	}

	dbp.execPtraceFunc(func() {
		err = _ContinueDebugEvent(uint32(dbp.pid), uint32(dbp.os.breakThread), _DBG_CONTINUE)
	})
	if err != nil {
		return nil, err
	}

	return initializeDebugProcess(dbp, exepath, false)
}

// findExePath searches for process pid, and returns its executable path.
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

// Attach to an existing process with the given PID.
func Attach(pid int) (*Process, error) {
	// TODO: Probably should have SeDebugPrivilege before starting here.
	err := _DebugActiveProcess(uint32(pid))
	if err != nil {
		return nil, err
	}
	exepath, err := findExePath(pid)
	if err != nil {
		return nil, err
	}
	return newDebugProcess(New(pid), exepath)
}

// Kill kills the process.
func (dbp *Process) Kill() error {
	if dbp.exited {
		return nil
	}
	if !dbp.threads[dbp.pid].Stopped() {
		return errors.New("process must be stopped in order to kill it")
	}
	// TODO: Should not have to ignore failures here,
	// but some tests appear to Kill twice causing
	// this to fail on second attempt.
	_ = syscall.TerminateProcess(dbp.os.hProcess, 1)
	dbp.exited = true
	return nil
}

func (dbp *Process) requestManualStop() error {
	return _DebugBreakProcess(dbp.os.hProcess)
}

func (dbp *Process) updateThreadList() error {
	// We ignore this request since threads are being
	// tracked as they are created/killed in waitForDebugEvent.
	return nil
}

func (dbp *Process) addThread(hThread syscall.Handle, threadID int, attach, suspendNewThreads bool) (*Thread, error) {
	if thread, ok := dbp.threads[threadID]; ok {
		return thread, nil
	}
	thread := &Thread{
		ID:  threadID,
		dbp: dbp,
		os:  new(OSSpecificDetails),
	}
	thread.os.hThread = hThread
	dbp.threads[threadID] = thread
	if dbp.currentThread == nil {
		dbp.SwitchThread(thread.ID)
	}
	if suspendNewThreads {
		_, err := _SuspendThread(thread.os.hThread)
		if err != nil {
			return nil, err
		}
	}
	return thread, nil
}

func (dbp *BinaryInfo) parseDebugFrame(exe *pe.File, wg *sync.WaitGroup) {
	defer wg.Done()

	debugFrameSec := exe.Section(".debug_frame")
	debugInfoSec := exe.Section(".debug_info")

	if debugFrameSec != nil && debugInfoSec != nil {
		debugFrame, err := debugFrameSec.Data()
		if err != nil && uint32(len(debugFrame)) < debugFrameSec.Size {
			fmt.Println("could not get .debug_frame section", err)
			os.Exit(1)
		}
		if 0 < debugFrameSec.VirtualSize && debugFrameSec.VirtualSize < debugFrameSec.Size {
			debugFrame = debugFrame[:debugFrameSec.VirtualSize]
		}
		dat, err := debugInfoSec.Data()
		if err != nil {
			fmt.Println("could not get .debug_info section", err)
			os.Exit(1)
		}
		dbp.frameEntries = frame.Parse(debugFrame, frame.DwarfEndian(dat))
	} else {
		fmt.Println("could not find .debug_frame section in binary")
		os.Exit(1)
	}
}

// Borrowed from https://golang.org/src/cmd/internal/objfile/pe.go
func findPESymbol(f *pe.File, name string) (*pe.Symbol, error) {
	for _, s := range f.Symbols {
		if s.Name != name {
			continue
		}
		if s.SectionNumber <= 0 {
			return nil, fmt.Errorf("symbol %s: invalid section number %d", name, s.SectionNumber)
		}
		if len(f.Sections) < int(s.SectionNumber) {
			return nil, fmt.Errorf("symbol %s: section number %d is larger than max %d", name, s.SectionNumber, len(f.Sections))
		}
		return s, nil
	}
	return nil, fmt.Errorf("no %s symbol found", name)
}

// Borrowed from https://golang.org/src/cmd/internal/objfile/pe.go
func loadPETable(f *pe.File, sname, ename string) ([]byte, error) {
	ssym, err := findPESymbol(f, sname)
	if err != nil {
		return nil, err
	}
	esym, err := findPESymbol(f, ename)
	if err != nil {
		return nil, err
	}
	if ssym.SectionNumber != esym.SectionNumber {
		return nil, fmt.Errorf("%s and %s symbols must be in the same section", sname, ename)
	}
	sect := f.Sections[ssym.SectionNumber-1]
	data, err := sect.Data()
	if err != nil {
		return nil, err
	}
	return data[ssym.Value:esym.Value], nil
}

// Borrowed from https://golang.org/src/cmd/internal/objfile/pe.go
func pcln(exe *pe.File) (textStart uint64, symtab, pclntab []byte, err error) {
	var imageBase uint64
	switch oh := exe.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		imageBase = uint64(oh.ImageBase)
	case *pe.OptionalHeader64:
		imageBase = oh.ImageBase
	default:
		return 0, nil, nil, fmt.Errorf("pe file format not recognized")
	}
	if sect := exe.Section(".text"); sect != nil {
		textStart = imageBase + uint64(sect.VirtualAddress)
	}
	if pclntab, err = loadPETable(exe, "runtime.pclntab", "runtime.epclntab"); err != nil {
		// We didn't find the symbols, so look for the names used in 1.3 and earlier.
		// TODO: Remove code looking for the old symbols when we no longer care about 1.3.
		var err2 error
		if pclntab, err2 = loadPETable(exe, "pclntab", "epclntab"); err2 != nil {
			return 0, nil, nil, err
		}
	}
	if symtab, err = loadPETable(exe, "runtime.symtab", "runtime.esymtab"); err != nil {
		// Same as above.
		var err2 error
		if symtab, err2 = loadPETable(exe, "symtab", "esymtab"); err2 != nil {
			return 0, nil, nil, err
		}
	}
	return textStart, symtab, pclntab, nil
}

func (dbp *BinaryInfo) obtainGoSymbols(exe *pe.File, wg *sync.WaitGroup) {
	defer wg.Done()

	_, symdat, pclndat, err := pcln(exe)
	if err != nil {
		fmt.Println("could not get Go symbols", err)
		os.Exit(1)
	}

	pcln := gosym.NewLineTable(pclndat, uint64(exe.Section(".text").Offset))
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		fmt.Println("could not get initialize line table", err)
		os.Exit(1)
	}

	dbp.goSymTable = tab
}

func (dbp *BinaryInfo) parseDebugLineInfo(exe *pe.File, wg *sync.WaitGroup) {
	defer wg.Done()

	if sec := exe.Section(".debug_line"); sec != nil {
		debugLine, err := sec.Data()
		if err != nil && uint32(len(debugLine)) < sec.Size {
			fmt.Println("could not get .debug_line section", err)
			os.Exit(1)
		}
		if 0 < sec.VirtualSize && sec.VirtualSize < sec.Size {
			debugLine = debugLine[:sec.VirtualSize]
		}
		dbp.lineInfo = line.Parse(debugLine)
	} else {
		fmt.Println("could not find .debug_line section in binary")
		os.Exit(1)
	}
}

var UnsupportedArchErr = errors.New("unsupported architecture of windows/386 - only windows/amd64 is supported")

func (dbp *BinaryInfo) findExecutable(path string, pid int) (*pe.File, string, error) {
	peFile, err := openExecutablePath(path)
	if err != nil {
		return nil, path, err
	}
	if peFile.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return nil, path, UnsupportedArchErr
	}
	dbp.dwarf, err = dwarfFromPE(peFile)
	if err != nil {
		return nil, path, err
	}
	return peFile, path, nil
}

func openExecutablePath(path string) (*pe.File, error) {
	f, err := os.OpenFile(path, 0, os.ModePerm)
	if err != nil {
		return nil, err
	}
	return pe.NewFile(f)
}

// Adapted from src/debug/pe/file.go: pe.(*File).DWARF()
func dwarfFromPE(f *pe.File) (*dwarf.Data, error) {
	// There are many other DWARF sections, but these
	// are the ones the debug/dwarf package uses.
	// Don't bother loading others.
	var names = [...]string{"abbrev", "info", "line", "str"}
	var dat [len(names)][]byte
	for i, name := range names {
		name = ".debug_" + name
		s := f.Section(name)
		if s == nil {
			continue
		}
		b, err := s.Data()
		if err != nil && uint32(len(b)) < s.Size {
			return nil, err
		}
		if 0 < s.VirtualSize && s.VirtualSize < s.Size {
			b = b[:s.VirtualSize]
		}
		dat[i] = b
	}

	abbrev, info, line, str := dat[0], dat[1], dat[2], dat[3]
	return dwarf.New(abbrev, nil, nil, info, line, nil, nil, str)
}

type waitForDebugEventFlags int

const (
	waitBlocking waitForDebugEventFlags = 1 << iota
	waitSuspendNewThreads
)

func (dbp *Process) waitForDebugEvent(flags waitForDebugEventFlags) (threadID, exitCode int, err error) {
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
			dbp.os.hProcess = debugInfo.Process
			_, err = dbp.addThread(debugInfo.Thread, int(debugEvent.ThreadId), false, flags&waitSuspendNewThreads != 0)
			if err != nil {
				return 0, 0, err
			}
			break
		case _CREATE_THREAD_DEBUG_EVENT:
			debugInfo := (*_CREATE_THREAD_DEBUG_INFO)(unionPtr)
			_, err = dbp.addThread(debugInfo.Thread, int(debugEvent.ThreadId), false, flags&waitSuspendNewThreads != 0)
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
			exception := (*_EXCEPTION_DEBUG_INFO)(unionPtr)
			tid := int(debugEvent.ThreadId)

			switch code := exception.ExceptionRecord.ExceptionCode; code {
			case _EXCEPTION_BREAKPOINT:

				// check if the exception address really is a breakpoint instruction, if
				// it isn't we already removed that breakpoint and we can't deal with
				// this exception anymore.
				atbp := true
				if thread, found := dbp.threads[tid]; found {
					if data, err := thread.readMemory(exception.ExceptionRecord.ExceptionAddress, dbp.arch.BreakpointSize()); err == nil {
						instr := dbp.arch.BreakpointInstruction()
						for i := range instr {
							if data[i] != instr[i] {
								atbp = false
								break
							}
						}
					}
					if !atbp {
						thread.SetPC(uint64(exception.ExceptionRecord.ExceptionAddress))
					}
				}

				if atbp {
					dbp.os.breakThread = tid
					return tid, 0, nil
				} else {
					continueStatus = _DBG_CONTINUE
				}
			case _EXCEPTION_SINGLE_STEP:
				dbp.os.breakThread = tid
				return tid, 0, nil
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

func (dbp *Process) trapWait(pid int) (*Thread, error) {
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
		return nil, ProcessExitedError{Pid: dbp.pid, Status: exitCode}
	}
	th := dbp.threads[tid]
	return th, nil
}

func (dbp *Process) loadProcessInformation(wg *sync.WaitGroup) {
	wg.Done()
}

func (dbp *Process) wait(pid, options int) (int, *sys.WaitStatus, error) {
	return 0, nil, fmt.Errorf("not implemented: wait")
}

func (dbp *Process) setCurrentBreakpoints(trapthread *Thread) error {
	// While the debug event that stopped the target was being propagated
	// other target threads could generate other debug events.
	// After this function we need to know about all the threads
	// stopped on a breakpoint. To do that we first suspend all target
	// threads and then repeatedly call _ContinueDebugEvent followed by
	// waitForDebugEvent in non-blocking mode.
	// We need to explicitly call SuspendThread because otherwise the
	// call to _ContinueDebugEvent will resume execution of some of the
	// target threads.

	err := trapthread.SetCurrentBreakpoint()
	if err != nil {
		return err
	}

	for _, thread := range dbp.threads {
		thread.running = false
		_, err := _SuspendThread(thread.os.hThread)
		if err != nil {
			return err
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
			return err
		}
		if tid == 0 {
			break
		}
		err = dbp.threads[tid].SetCurrentBreakpoint()
		if err != nil {
			return err
		}
	}

	return nil
}

func (dbp *Process) exitGuard(err error) error {
	return err
}

func (dbp *Process) resume() error {
	for _, thread := range dbp.threads {
		if thread.CurrentBreakpoint != nil {
			if err := thread.StepInstruction(); err != nil {
				return err
			}
			thread.CurrentBreakpoint = nil
		}
	}

	for _, thread := range dbp.threads {
		thread.running = true
		_, err := _ResumeThread(thread.os.hThread)
		if err != nil {
			return err
		}
	}

	return nil
}

func (dbp *Process) detach() error {
	for _, thread := range dbp.threads {
		_, err := _ResumeThread(thread.os.hThread)
		if err != nil {
			return err
		}
	}
	return PtraceDetach(dbp.pid, 0)
}

func killProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
