package proc

// #include "proc_darwin.h"
// #include "threads_darwin.h"
// #include "exec_darwin.h"
// #include <stdlib.h>
import "C"
import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"
)

// OSProcessDetails holds Darwin specific information.
type OSProcessDetails struct {
	task             C.task_t      // mach task for the debugged process.
	exceptionPort    C.mach_port_t // mach port for receiving mach exceptions.
	notificationPort C.mach_port_t // mach port for dead name notification (process exit).

	// the main port we use, will return messages from both the
	// exception and notification ports.
	portSet C.mach_port_t
}

// Launch creates and begins debugging a new process. Uses a
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
	// argv array must be null terminated.
	argvSlice = append(argvSlice, nil)

	dbp := New(0)
	var pid int
	execOnPtraceThread(func() {
		ret := C.fork_exec(argv0, &argvSlice[0], C.int(len(argvSlice)),
			&dbp.os.task, &dbp.os.portSet, &dbp.os.exceptionPort,
			&dbp.os.notificationPort)
		pid = int(ret)
	})
	if pid <= 0 {
		return nil, fmt.Errorf("could not fork/exec")
	}
	dbp.Pid = pid
	for i := range argvSlice {
		C.free(unsafe.Pointer(argvSlice[i]))
	}

	var hdr C.mach_msg_header_t
	var sig C.int
	port := C.mach_port_wait(dbp.os.portSet, &hdr, &sig, C.int(0))
	if port == 0 {
		return nil, errors.New("error while waiting for process to exec")
	}
	dbp, err = initializeDebugProcess(dbp, argv0Go, false)
	if err != nil {
		return dbp, err
	}
	th, ok := dbp.Threads[int(port)]
	if !ok {
		return nil, errors.New("could not find thread")
	}
	th.os.msgStop = true
	th.os.hdr = hdr
	th.os.sig = sig

	return dbp, nil
}

// Attach to an existing process with the given PID.
func Attach(pid int) (*Process, error) {
	dbp := New(pid)

	kret := C.acquire_mach_task(C.int(pid),
		&dbp.os.task, &dbp.os.portSet, &dbp.os.exceptionPort,
		&dbp.os.notificationPort)

	if kret != C.KERN_SUCCESS {
		return nil, fmt.Errorf("could not attach to %d", pid)
	}

	return initializeDebugProcess(dbp, "", true)
}

// Kill kills the process.
func (dbp *Process) Kill() error {
	if dbp.exited {
		return nil
	}

	for _, th := range dbp.Threads {
		th.halt()
	}
	for _, th := range dbp.Threads {
		th.sendMachReply()
	}
	kret := C.task_set_exception_ports(dbp.os.task, C.EXC_MASK_ALL, C.MACH_PORT_NULL, C.EXCEPTION_DEFAULT, C.THREAD_STATE_NONE)
	if kret != C.KERN_SUCCESS {
		return errors.New("could not restore exception ports")
	}
	if err := syscall.Kill(-dbp.Pid, syscall.SIGKILL); err != nil {
		return err
	}
	for _, th := range dbp.Threads {
		C.resume_thread(th.os.threadAct)
	}
	_, err := dbp.trapWait(-1)
	if _, ok := err.(ProcessExitedError); !ok {
		return errors.New("process did not exit")
	}
	selfTask := C.ipc_space_t(C.mach_task())
	for _, th := range dbp.Threads {
		C.mach_port_deallocate(selfTask, C.mach_port_name_t(th.os.threadAct))
	}
	C.mach_port_destroy(selfTask, C.mach_port_name_t(dbp.os.notificationPort))
	C.mach_port_deallocate(selfTask, C.mach_port_name_t(dbp.os.exceptionPort))
	C.mach_port_deallocate(selfTask, C.mach_port_name_t(dbp.os.task))
	dbp.postExit()
	return nil
}

func requestManualStop(dbp *Process) (err error) {
	var (
		task          = C.mach_port_t(dbp.os.task)
		thread        = C.mach_port_t(dbp.CurrentThread.os.threadAct)
		exceptionPort = C.mach_port_t(dbp.os.exceptionPort)
	)
	kret := C.raise_exception(task, thread, exceptionPort, C.EXC_BREAKPOINT)
	if kret != C.KERN_SUCCESS {
		return fmt.Errorf("could not raise mach exception")
	}
	return nil
}

func updateThreadList(dbp *Process) error {
	var (
		err   error
		kret  C.kern_return_t
		count C.int
		list  []uint32
	)

	for {
		count = C.thread_count(dbp.os.task)
		if count == -1 {
			return fmt.Errorf("could not get thread count")
		}
		list = make([]uint32, count)

		// TODO(dp) might be better to malloc mem in C and then free it here
		// instead of getting count above and passing in a slice
		kret = C.get_threads(dbp.os.task, unsafe.Pointer(&list[0]), count)
		if kret != -2 {
			break
		}
	}
	if kret != C.KERN_SUCCESS {
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
		ID:  port,
		dbp: dbp,
		os:  new(OSSpecificDetails),
	}
	dbp.Threads[port] = thread
	thread.os.threadAct = C.thread_act_t(port)
	if dbp.CurrentThread == nil {
		dbp.SwitchThread(thread.ID)
	}
	return thread, nil
}

func (dbp *Process) findExecutable(path string) (string, error) {
	if path == "" {
		path = C.GoString(C.find_executable(C.int(dbp.Pid)))
	}
	return path, nil
}

func (dbp *Process) trapWait(pid int) (*Thread, error) {
	for {
		var hdr C.mach_msg_header_t
		var sig C.int
		port := C.mach_port_wait(dbp.os.portSet, &hdr, &sig, C.int(0))
		th, ok := dbp.Threads[int(port)]
		if ok {
			th.os.msgStop = true
			th.os.hdr = hdr
			th.os.sig = sig
		}

		switch port {
		case dbp.os.notificationPort:
			_, status, err := dbp.wait(dbp.Pid, 0)
			if err != nil {
				return nil, err
			}
			dbp.postExit()
			return nil, ProcessExitedError{Pid: dbp.Pid, Status: status.ExitStatus()}

		case C.MACH_RCV_INTERRUPTED:
			if !dbp.halt {
				// Call trapWait again, it seems
				// MACH_RCV_INTERRUPTED is emitted before
				// process natural death _sometimes_.
				continue
			}
			return th, nil

		case 0:
			return nil, fmt.Errorf("error while waiting for task")
		}

		// Since we cannot be notified of new threads on OS X
		// this is as good a time as any to check for them.
		dbp.updateThreadList()
		_, err := dbp.drainPendingMesages()
		return th, err
	}
}

func (dbp *Process) drainPendingMesages() ([]int, error) {
	ports := make([]int, 0, len(dbp.Threads))
	for {
		var hdr C.mach_msg_header_t
		var sig C.int
		port := C.mach_port_wait(dbp.os.portSet, &hdr, &sig, C.int(1))
		if port == 0 {
			return ports, nil
		}
		if port == C.MACH_RCV_TIMED_OUT {
			return ports, nil
		}
		if port == dbp.os.notificationPort {
			_, status, err := dbp.wait(dbp.Pid, 0)
			if err != nil {
				return nil, err
			}
			dbp.postExit()
			return nil, ProcessExitedError{Pid: dbp.Pid, Status: status.ExitStatus()}
		}
		th, ok := dbp.Threads[int(port)]
		if ok {
			th.os.msgStop = true
			th.os.hdr = hdr
			th.os.sig = sig
			ports = append(ports, int(port))
		}
	}
}

func (dbp *Process) loadProcessInformation() {
	return
}

func (dbp *Process) wait(pid, options int) (int, *sys.WaitStatus, error) {
	var status sys.WaitStatus
	wpid, err := sys.Wait4(pid, &status, options, nil)
	return wpid, &status, err
}

func killProcess(pid int) error {
	return sys.Kill(pid, sys.SIGINT)
}

func (dbp *Process) exitGuard(err error) error {
	if err != ErrContinueThread {
		return err
	}
	_, status, werr := dbp.wait(dbp.Pid, sys.WNOHANG)
	if werr == nil && status.Exited() {
		dbp.postExit()
		return ProcessExitedError{Pid: dbp.Pid, Status: status.ExitStatus()}
	}
	return err
}

func (dbp *Process) resume() error {
	// all threads stopped over a breakpoint are made to step over it
	for _, thread := range dbp.Threads {
		if thread.CurrentBreakpoint != nil {
			if err := thread.StepInstruction(); err != nil {
				return err
			}
			thread.CurrentBreakpoint = nil
		}
	}
	// everything is resumed
	for _, thread := range dbp.Threads {
		if err := thread.resume(); err != nil {
			return dbp.exitGuard(err)
		}
	}
	return nil
}
