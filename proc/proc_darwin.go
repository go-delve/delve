package proc

// #include "proc_darwin.h"
// #include "exec_darwin.h"
// #include <stdlib.h>
import "C"
import (
	"debug/gosym"
	"debug/macho"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"unsafe"

	"github.com/derekparker/delve/dwarf/frame"
	"github.com/derekparker/delve/dwarf/line"
	sys "golang.org/x/sys/unix"
)

// Darwin specific information.
type OSProcessDetails struct {
	task             C.mach_port_name_t // mach task for the debugged process.
	exceptionPort    C.mach_port_t      // mach port for receiving mach exceptions.
	notificationPort C.mach_port_t      // mach port for dead name notification (process exit).

	// the main port we use, will return messages from both the
	// exception and notification ports.
	portSet C.mach_port_t
}

// Create and begin debugging a new process. Uses a
// custom fork/exec process in order to take advantage of
// PT_SIGEXC on Darwin which will turn Unix signals into
// Mach exceptions.
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

	argv0 := C.CString(argv0Go)

	argvSlice := make([]*C.char, 0, len(cmd)+1)
	for _, arg := range cmd {
		argvSlice = append(argvSlice, C.CString(arg))
	}

	dbp := New(0)
	var pid int
	dbp.execPtraceFunc(func() {
		ret := C.fork_exec(argv0, &argvSlice[0], C.int(len(argvSlice)), &dbp.os.task, &dbp.os.portSet, &dbp.os.exceptionPort, &dbp.os.notificationPort)
		pid = int(ret)
	})
	if pid <= 0 {
		return nil, fmt.Errorf("could not fork/exec")
	}
	dbp.Pid = pid
	for i := range argvSlice {
		C.free(unsafe.Pointer(argvSlice[i]))
	}

	dbp, err = initializeDebugProcess(dbp, argv0Go, false)
	if err != nil {
		return nil, err
	}
	err = dbp.Continue()
	return dbp, err
}

func (dbp *Process) requestManualStop() (err error) {
	var (
		task          = C.mach_port_t(dbp.os.task)
		thread        = C.mach_port_t(dbp.CurrentThread.os.thread_act)
		exceptionPort = C.mach_port_t(dbp.os.exceptionPort)
	)
	kret := C.raise_exception(task, thread, exceptionPort, C.EXC_BREAKPOINT)
	if kret != C.KERN_SUCCESS {
		return fmt.Errorf("could not raise mach exception")
	}
	return nil
}

func (dbp *Process) updateThreadList() error {
	var (
		err   error
		kret  C.kern_return_t
		count = C.thread_count(C.task_t(dbp.os.task))
	)
	if count == -1 {
		return fmt.Errorf("could not get thread count")
	}
	list := make([]uint32, count)

	// TODO(dp) might be better to malloc mem in C and then free it here
	// instead of getting count above and passing in a slice
	kret = C.get_threads(C.task_t(dbp.os.task), unsafe.Pointer(&list[0]))
	if kret != C.KERN_SUCCESS {
		return fmt.Errorf("could not get thread list")
	}
	if count < 0 {
		return fmt.Errorf("could not get thread list")
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

func (dbp *Process) addThread(port int, attach bool) (*Thread, error) {
	if thread, ok := dbp.Threads[port]; ok {
		return thread, nil
	}
	thread := &Thread{
		Id:  port,
		dbp: dbp,
		os:  new(OSSpecificDetails),
	}
	dbp.Threads[port] = thread
	thread.os.thread_act = C.thread_act_t(port)
	if dbp.CurrentThread == nil {
		dbp.CurrentThread = thread
	}
	return thread, nil
}

func (dbp *Process) parseDebugFrame(exe *macho.File, wg *sync.WaitGroup) {
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

func (dbp *Process) obtainGoSymbols(exe *macho.File, wg *sync.WaitGroup) {
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

func (dbp *Process) parseDebugLineInfo(exe *macho.File, wg *sync.WaitGroup) {
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

func (dbp *Process) findExecutable(path string) (*macho.File, error) {
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

func (dbp *Process) trapWait(pid int) (*Thread, error) {
	var (
		th  *Thread
		err error
	)
	for {
		port := C.mach_port_wait(dbp.os.portSet)

		switch port {
		case dbp.os.notificationPort:
			_, status, err := wait(dbp.Pid, dbp.Pid, 0)
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
			return nil, fmt.Errorf("error while waiting for task")
		}

		// Since we cannot be notified of new threads on OS X
		// this is as good a time as any to check for them.
		dbp.updateThreadList()
		th, err = dbp.handleBreakpointOnThread(int(port))
		if err != nil {
			if _, ok := err.(NoBreakpointError); ok {
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

func wait(pid, tgid, options int) (int, *sys.WaitStatus, error) {
	var status sys.WaitStatus
	wpid, err := sys.Wait4(pid, &status, options, nil)
	return wpid, &status, err
}
