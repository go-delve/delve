package proc

// #include "windows.h"
import "C"
import (
	"debug/gosym"
	"debug/pe"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/debug/dwarf"

	sys "golang.org/x/sys/windows"

	"github.com/derekparker/delve/dwarf/frame"
	"github.com/derekparker/delve/dwarf/line"
)

const (
	// DEBUGONLYTHISPROCESS tracks https://msdn.microsoft.com/en-us/library/windows/desktop/ms684863(v=vs.85).aspx
	DEBUGONLYTHISPROCESS = 0x00000002
)

// OSProcessDetails holds Windows specific information.
type OSProcessDetails struct {
	hProcess    sys.Handle
	breakThread int
}

// Launch creates and begins debugging a new process.
func Launch(cmd []string) (*Process, error) {
	argv0Go, err := filepath.Abs(cmd[0])
	if err != nil {
		return nil, err
	}
	// Make sure the binary exists.
	if filepath.Base(cmd[0]) == cmd[0] {
		if _, err := exec.LookPath(cmd[0]); err != nil {
			return nil, err
		}
	}
	if _, err := os.Stat(argv0Go); err != nil {
		return nil, err
	}

	argv0, _ := syscall.UTF16PtrFromString(argv0Go)

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

	// Initialize the startup info and create process
	si := new(sys.StartupInfo)
	si.Cb = uint32(unsafe.Sizeof(*si))
	si.Flags = syscall.STARTF_USESTDHANDLES
	si.StdInput = sys.Handle(fd[0])
	si.StdOutput = sys.Handle(fd[1])
	si.StdErr = sys.Handle(fd[2])
	pi := new(sys.ProcessInformation)
	err = sys.CreateProcess(argv0, nil, nil, nil, true, DEBUGONLYTHISPROCESS, nil, nil, si, pi)
	if err != nil {
		return nil, err
	}
	sys.CloseHandle(sys.Handle(pi.Process))
	sys.CloseHandle(sys.Handle(pi.Thread))

	dbp := New(int(pi.ProcessId))

	switch runtime.GOARCH {
	case "amd64":
		dbp.arch = AMD64Arch()
	}

	// Note - it should not actually be possible for the
	// call to waitForDebugEvent to fail, since Windows
	// will always fire a CreateProcess event immediately
	// after launching under DEBUGONLYTHISPROCESS.
	var tid, exitCode int
	dbp.execPtraceFunc(func() {
		tid, exitCode, err = dbp.waitForDebugEvent()
	})
	if err != nil {
		return nil, err
	}
	if tid == 0 {
		dbp.postExit()
		return nil, ProcessExitedError{Pid: dbp.Pid, Status: exitCode}
	}

	return initializeDebugProcess(dbp, argv0Go, false)
}

// Attach to an existing process with the given PID.
func Attach(pid int) (*Process, error) {
	return nil, fmt.Errorf("not implemented: Attach")
}

// Kill kills the process.
func (dbp *Process) Kill() error {
	if dbp.exited {
		return nil
	}
	if !dbp.Threads[dbp.Pid].Stopped() {
		return errors.New("process must be stopped in order to kill it")
	}
	// TODO: Should not have to ignore failures here,
	// but some tests appear to Kill twice causing
	// this to fail on second attempt.
	_ = C.TerminateProcess(C.HANDLE(dbp.os.hProcess), 1)
	dbp.exited = true
	return nil
}

func (dbp *Process) requestManualStop() error {
	res := C.DebugBreakProcess(C.HANDLE(dbp.os.hProcess))
	if res == C.FALSE {
		return fmt.Errorf("failed to break process %d", dbp.Pid)
	}
	return nil
}

func (dbp *Process) updateThreadList() error {
	// We ignore this request since threads are being
	// tracked as they are created/killed in waitForDebugEvent.
	return nil
}

func (dbp *Process) addThread(hThread sys.Handle, threadID int, attach bool) (*Thread, error) {
	if thread, ok := dbp.Threads[threadID]; ok {
		return thread, nil
	}
	thread := &Thread{
		ID:  threadID,
		dbp: dbp,
		os:  new(OSSpecificDetails),
	}
	thread.os.hThread = hThread
	dbp.Threads[threadID] = thread
	if dbp.CurrentThread == nil {
		dbp.SwitchThread(thread.ID)
	}
	return thread, nil
}

func (dbp *Process) parseDebugFrame(exe *pe.File, wg *sync.WaitGroup) {
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

func (dbp *Process) obtainGoSymbols(exe *pe.File, wg *sync.WaitGroup) {
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

func (dbp *Process) parseDebugLineInfo(exe *pe.File, wg *sync.WaitGroup) {
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

func (dbp *Process) findExecutable(path string) (*pe.File, error) {
	if path == "" {
		// TODO: Find executable path from PID/handle on Windows:
		// https://msdn.microsoft.com/en-us/library/aa366789(VS.85).aspx
		return nil, fmt.Errorf("not yet implemented")
	}
	f, err := os.OpenFile(path, 0, os.ModePerm)
	if err != nil {
		return nil, err
	}
	peFile, err := pe.NewFile(f)
	if err != nil {
		return nil, err
	}
	dbp.dwarf, err = dwarfFromPE(peFile)
	if err != nil {
		return nil, err
	}
	return peFile, nil
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

func (dbp *Process) waitForDebugEvent() (threadID, exitCode int, err error) {
	var debugEvent C.DEBUG_EVENT
	for {
		// Wait for a debug event...
		res := C.WaitForDebugEvent(&debugEvent, C.INFINITE)
		if res == C.FALSE {
			return 0, 0, fmt.Errorf("could not WaitForDebugEvent")
		}

		// ... handle each event kind ...
		unionPtr := unsafe.Pointer(&debugEvent.u[0])
		switch debugEvent.dwDebugEventCode {
		case C.CREATE_PROCESS_DEBUG_EVENT:
			debugInfo := (*C.CREATE_PROCESS_DEBUG_INFO)(unionPtr)
			hFile := debugInfo.hFile
			if hFile != C.HANDLE(uintptr(0)) /* NULL */ && hFile != C.HANDLE(uintptr(0xFFFFFFFFFFFFFFFF)) /* INVALID_HANDLE_VALUE */ {
				res = C.CloseHandle(hFile)
				if res == C.FALSE {
					return 0, 0, fmt.Errorf("could not close create process file handle")
				}
			}
			dbp.os.hProcess = sys.Handle(debugInfo.hProcess)
			_, err = dbp.addThread(sys.Handle(debugInfo.hThread), int(debugEvent.dwThreadId), false)
			if err != nil {
				return 0, 0, err
			}
			break
		case C.CREATE_THREAD_DEBUG_EVENT:
			debugInfo := (*C.CREATE_THREAD_DEBUG_INFO)(unionPtr)
			_, err = dbp.addThread(sys.Handle(debugInfo.hThread), int(debugEvent.dwThreadId), false)
			if err != nil {
				return 0, 0, err
			}
			break
		case C.EXIT_THREAD_DEBUG_EVENT:
			delete(dbp.Threads, int(debugEvent.dwThreadId))
			break
		case C.OUTPUT_DEBUG_STRING_EVENT:
			//TODO: Handle debug output strings
			break
		case C.LOAD_DLL_DEBUG_EVENT:
			debugInfo := (*C.LOAD_DLL_DEBUG_INFO)(unionPtr)
			hFile := debugInfo.hFile
			if hFile != C.HANDLE(uintptr(0)) /* NULL */ && hFile != C.HANDLE(uintptr(0xFFFFFFFFFFFFFFFF)) /* INVALID_HANDLE_VALUE */ {
				res = C.CloseHandle(hFile)
				if res == C.FALSE {
					return 0, 0, fmt.Errorf("could not close DLL load file handle")
				}
			}
			break
		case C.UNLOAD_DLL_DEBUG_EVENT:
			break
		case C.RIP_EVENT:
			break
		case C.EXCEPTION_DEBUG_EVENT:
			tid := int(debugEvent.dwThreadId)
			dbp.os.breakThread = tid
			return tid, 0, nil
		case C.EXIT_PROCESS_DEBUG_EVENT:
			debugInfo := (*C.EXIT_PROCESS_DEBUG_INFO)(unionPtr)
			return 0, int(debugInfo.dwExitCode), nil
		default:
			return 0, 0, fmt.Errorf("unknown debug event code: %d", debugEvent.dwDebugEventCode)
		}

		// .. and then continue unless we received an event that indicated we should break into debugger.
		res = C.ContinueDebugEvent(debugEvent.dwProcessId, debugEvent.dwThreadId, C.DBG_CONTINUE)
		if res == C.WINBOOL(0) {
			return 0, 0, fmt.Errorf("could not ContinueDebugEvent")
		}
	}
}

func (dbp *Process) trapWait(pid int) (*Thread, error) {
	var err error
	var tid, exitCode int
	dbp.execPtraceFunc(func() {
		tid, exitCode, err = dbp.waitForDebugEvent()
	})
	if err != nil {
		return nil, err
	}
	if tid == 0 {
		dbp.postExit()
		return nil, ProcessExitedError{Pid: dbp.Pid, Status: exitCode}
	}
	th := dbp.Threads[tid]
	return th, nil
}

func (dbp *Process) loadProcessInformation(wg *sync.WaitGroup) {
	wg.Done()
}

func (dbp *Process) wait(pid, options int) (int, *sys.WaitStatus, error) {
	return 0, nil, fmt.Errorf("not implemented: wait")
}

func (dbp *Process) setCurrentBreakpoints(trapthread *Thread) error {
	// TODO: In theory, we should also be setting the breakpoints on other
	// threads that happen to have hit this BP. But doing so leads to periodic
	// failures in the TestBreakpointsCounts test with hit counts being too high,
	// which can be traced back to occurences of multiple threads hitting a BP
	// at the same time.

	// My guess is that Windows will correctly trigger multiple DEBUG_EVENT's
	// in this case, one for each thread, so we should only handle the BP hit
	// on the thread that the debugger was evented on.

	return trapthread.SetCurrentBreakpoint()
}

func (dbp *Process) exitGuard(err error) error {
	return err
}

func (dbp *Process) resume() error {
	// Only resume the thread that broke into the debugger
	thread := dbp.Threads[dbp.os.breakThread]
	// This relies on the same assumptions as dbp.setCurrentBreakpoints
	if thread.CurrentBreakpoint != nil {
		if err := thread.StepInstruction(); err != nil {
			return err
		}
		thread.CurrentBreakpoint = nil
	}
	// In case we are now on a different thread, make sure we resume
	// the thread that is broken.
	thread = dbp.Threads[dbp.os.breakThread]
	if err := thread.resume(); err != nil {
		return err
	}
	return nil
}

func killProcess(pid int) error {
	fmt.Println("killProcess")
	return fmt.Errorf("not implemented: killProcess")
}
