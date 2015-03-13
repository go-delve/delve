package proctl

// #include "proctl_darwin.h"
import "C"
import (
	"debug/gosym"
	"debug/macho"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/derekparker/delve/dwarf/frame"
	sys "golang.org/x/sys/unix"
)

type OSProcessDetails struct {
	task          C.mach_port_name_t
	exceptionPort C.mach_port_t
}

func (dbp *DebuggedProcess) Halt() (err error) {
	for _, th := range dbp.Threads {
		err := th.Halt()
		if err != nil {
			return err
		}
	}
	return nil
}

// Finds the executable and then uses it
// to parse the following information:
// * Dwarf .debug_frame section
// * Dwarf .debug_line section
// * Go symbol table.
func (dbp *DebuggedProcess) LoadInformation() error {
	var (
		wg  sync.WaitGroup
		exe *macho.File
		err error
	)

	if ret := C.acquire_mach_task(C.int(dbp.Pid), &dbp.os.task, &dbp.os.exceptionPort); ret != C.KERN_SUCCESS {
		return fmt.Errorf("could not acquire mach task %d", ret)
	}
	exe, err = dbp.findExecutable()
	if err != nil {
		return err
	}
	data, err := exe.DWARF()
	if err != nil {
		return err
	}
	dbp.Dwarf = data

	wg.Add(2)
	go dbp.parseDebugFrame(exe, &wg)
	go dbp.obtainGoSymbols(exe, &wg)
	wg.Wait()

	return nil
}

func (dbp *DebuggedProcess) updateThreadList() error {
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

func (dbp *DebuggedProcess) addThread(port int, attach bool) (*ThreadContext, error) {
	if thread, ok := dbp.Threads[port]; ok {
		return thread, nil
	}
	fmt.Println("new thread spawned", port)
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
		dbp.FrameEntries = frame.Parse(debugFrame)
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

	dbp.GoSymTable = tab
}

func (dbp *DebuggedProcess) findExecutable() (*macho.File, error) {
	pathptr, err := C.find_executable(C.int(dbp.Pid))
	if err != nil {
		return nil, err
	}
	return macho.Open(C.GoString(pathptr))
}

func trapWait(dbp *DebuggedProcess, pid int) (int, error) {
	port := C.mach_port_wait(dbp.os.exceptionPort)
	if port == C.MACH_RCV_INTERRUPTED {
		return -1, ManualStopError{}
	}

	if port == 0 {
		_, status, err := wait(dbp.Pid, 0)
		if err != nil {
			return -1, err
		}
		return -1, ProcessExitedError{Pid: dbp.Pid, Status: status.ExitStatus()}
	}

	dbp.updateThreadList()
	return int(port), nil
}

func wait(pid, options int) (int, *sys.WaitStatus, error) {
	var status sys.WaitStatus
	wpid, err := sys.Wait4(pid, &status, options, nil)
	return wpid, &status, err
}
