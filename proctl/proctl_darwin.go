package proctl

// #include "proctl_darwin.h"
// #include "exec_darwin.h"
import "C"
import (
	"debug/gosym"
	"debug/macho"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"unsafe"

	"github.com/derekparker/delve/dwarf/frame"
	"github.com/derekparker/delve/dwarf/line"
	"github.com/derekparker/delve/source"
	sys "golang.org/x/sys/unix"
)

type OSProcessDetails struct {
	task             C.mach_port_name_t
	portSet          C.mach_port_t
	exceptionPort    C.mach_port_t
	notificationPort C.mach_port_t
}

// Create and begin debugging a new process. Uses a
// custom fork/exec process in order to take advantage of
// PT_SIGEXC on Darwin.
func Launch(cmd []string) (*DebuggedProcess, error) {
	argv0, err := filepath.Abs(cmd[0])
	if err != nil {
		return nil, err
	}

	argv := C.CString(cmd[0])
	if len(cmd) == 1 {
		argv = nil
	}

	dbp := &DebuggedProcess{
		Threads:     make(map[int]*ThreadContext),
		BreakPoints: make(map[uint64]*BreakPoint),
		firstStart:  true,
		os:          new(OSProcessDetails),
		ast:         source.New(),
	}

	pid := int(C.fork_exec(C.CString(argv0), &argv, &dbp.os.task, &dbp.os.portSet, &dbp.os.exceptionPort, &dbp.os.notificationPort))
	if pid <= 0 {
		return nil, errors.New("could not fork/exec")
	}
	dbp.Pid = pid

	dbp, err = initializeDebugProcess(dbp, argv0, false)
	if err != nil {
		return nil, err
	}
	err = dbp.Continue()
	return dbp, err
}

func (dbp *DebuggedProcess) requestManualStop() (err error) {
	var (
		task          = C.mach_port_t(dbp.os.task)
		thread        = C.mach_port_t(dbp.CurrentThread.os.thread_act)
		exceptionPort = C.mach_port_t(dbp.os.exceptionPort)
	)
	kret := C.raise_exception(task, thread, exceptionPort, C.EXC_BREAKPOINT)
	if kret != C.KERN_SUCCESS {
		return errors.New("could not raise mach exception")
	}
	return nil
}

func (dbp *DebuggedProcess) updateThreadList() error {
	var (
		err   error
		kret  C.kern_return_t
		count = C.thread_count(C.task_t(dbp.os.task))
	)
	if count == -1 {
		return errors.New("could not get thread count")
	}
	list := make([]uint32, count)

	// TODO(dp) might be better to malloc mem in C and then free it here
	// instead of getting count above and passing in a slice
	kret = C.get_threads(C.task_t(dbp.os.task), unsafe.Pointer(&list[0]))
	if kret != C.KERN_SUCCESS {
		return errors.New("could not get thread list")
	}
	if count < 0 {
		return errors.New("could not get thread list")
	}

	for _, port := range list {
		if _, ok := dbp.Threads[int(port)]; !ok {
			_, err = dbp.addThread(int(port), false)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (dbp *DebuggedProcess) addThread(port int, attach bool) (*ThreadContext, error) {
	if thread, ok := dbp.Threads[port]; ok {
		return thread, nil
	}
	thread := &ThreadContext{
		Id:      port,
		Process: dbp,
		os:      new(OSSpecificDetails),
	}
	dbp.Threads[port] = thread
	thread.os.thread_act = C.thread_act_t(port)
	if dbp.CurrentThread == nil {
		dbp.CurrentThread = thread
	}
	return thread, nil
}

func (dbp *DebuggedProcess) parseDebugFrame(exe *macho.File, wg *sync.WaitGroup) {
	defer wg.Done()

	if sec := exe.Section("__debug_frame"); sec != nil {
		debugFrame, err := exe.Section("__debug_frame").Data()
		if err != nil {
			fmt.Println("could not get __debug_frame section", err)
			os.Exit(1)
		}
		dbp.frameEntries = frame.Parse(debugFrame)
	} else {
		fmt.Println("could not find __debug_frame section in binary")
		os.Exit(1)
	}
}

func (dbp *DebuggedProcess) obtainGoSymbols(exe *macho.File, wg *sync.WaitGroup) {
	defer wg.Done()

	var (
		symdat  []byte
		pclndat []byte
		err     error
	)

	if sec := exe.Section("__gosymtab"); sec != nil {
		symdat, err = sec.Data()
		if err != nil {
			fmt.Println("could not get .gosymtab section", err)
			os.Exit(1)
		}
	}

	if sec := exe.Section("__gopclntab"); sec != nil {
		pclndat, err = sec.Data()
		if err != nil {
			fmt.Println("could not get .gopclntab section", err)
			os.Exit(1)
		}
	}

	pcln := gosym.NewLineTable(pclndat, exe.Section("__text").Addr)
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		fmt.Println("could not get initialize line table", err)
		os.Exit(1)
	}

	dbp.goSymTable = tab
}

func (dbp *DebuggedProcess) parseDebugLineInfo(exe *macho.File, wg *sync.WaitGroup) {
	defer wg.Done()

	if sec := exe.Section("__debug_line"); sec != nil {
		debugLine, err := exe.Section("__debug_line").Data()
		if err != nil {
			fmt.Println("could not get __debug_line section", err)
			os.Exit(1)
		}
		dbp.lineInfo = line.Parse(debugLine)
	} else {
		fmt.Println("could not find __debug_line section in binary")
		os.Exit(1)
	}
}

func (dbp *DebuggedProcess) findExecutable(path string) (*macho.File, error) {
	if path == "" {
		path = C.GoString(C.find_executable(C.int(dbp.Pid)))
	}
	exe, err := macho.Open(path)
	if err != nil {
		return nil, err
	}
	data, err := exe.DWARF()
	if err != nil {
		return nil, err
	}
	dbp.dwarf = data
	return exe, nil
}

func (dbp *DebuggedProcess) trapWait(pid int) (*ThreadContext, error) {
	var (
		th  *ThreadContext
		err error
	)
	for {
		port := C.mach_port_wait(dbp.os.portSet)

		switch port {
		case dbp.os.notificationPort:
			_, status, err := wait(dbp.Pid, 0)
			if err != nil {
				return nil, err
			}
			dbp.exited = true
			return nil, ProcessExitedError{Pid: dbp.Pid, Status: status.ExitStatus()}
		case C.MACH_RCV_INTERRUPTED:
			if !dbp.halt {
				// Call trapWait again, it seems
				// MACH_RCV_INTERRUPTED is emitted before
				// process natural death _sometimes_.
				continue
			}
			return nil, ManualStopError{}
		case 0:
			return nil, errors.New("error while waiting for task")
		}

		// Since we cannot be notified of new threads on OS X
		// this is as good a time as any to check for them.
		dbp.updateThreadList()
		th, err = dbp.handleBreakpointOnThread(int(port))
		if err != nil {
			if _, ok := err.(NoBreakPointError); ok {
				th := dbp.Threads[int(port)]
				if dbp.firstStart || dbp.singleStepping || th.singleStepping {
					dbp.firstStart = false
					return dbp.Threads[int(port)], nil
				}
				if err := th.Continue(); err != nil {
					return nil, err
				}
				continue
			}
			return nil, err
		}
		return th, nil
	}
	return th, nil
}

func wait(pid, options int) (int, *sys.WaitStatus, error) {
	var status sys.WaitStatus
	wpid, err := sys.Wait4(pid, &status, options, nil)
	return wpid, &status, err
}
