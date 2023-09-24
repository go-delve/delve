//go:build darwin && macnative
// +build darwin,macnative

package native

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
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/internal/ebpf"
	"github.com/go-delve/delve/pkg/proc/macutil"
)

// osProcessDetails holds Darwin specific information.
type osProcessDetails struct {
	task             C.task_t      // mach task for the debugged process.
	exceptionPort    C.mach_port_t // mach port for receiving mach exceptions.
	notificationPort C.mach_port_t // mach port for dead name notification (process exit).
	initialized      bool
	halt             bool

	// the main port we use, will return messages from both the
	// exception and notification ports.
	portSet C.mach_port_t
}

func (os *osProcessDetails) Close() {}

// Launch creates and begins debugging a new process. Uses a
// custom fork/exec process in order to take advantage of
// PT_SIGEXC on Darwin which will turn Unix signals into
// Mach exceptions.
func Launch(cmd []string, wd string, flags proc.LaunchFlags, _ []string, _ string, _ string, _ proc.OutputRedirect, _ proc.OutputRedirect) (*proc.TargetGroup, error) {
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
	if err := macutil.CheckRosetta(); err != nil {
		return nil, err
	}

	argv0 := C.CString(argv0Go)
	defer C.free(unsafe.Pointer(argv0))
	argvSlice := make([]*C.char, 0, len(cmd)+1)
	for _, arg := range cmd {
		argvSlice = append(argvSlice, C.CString(arg))
	}
	// argv array must be null terminated.
	argvSlice = append(argvSlice, nil)

	dbp := newProcess(0)
	defer func() {
		if err != nil && dbp.pid != 0 {
			_ = detachWithoutGroup(dbp, true)
		}
	}()
	var pid int
	dbp.execPtraceFunc(func() {
		wd := C.CString(wd)
		defer C.free(unsafe.Pointer(wd))
		ret := C.fork_exec(argv0, &argvSlice[0], C.int(len(argvSlice)),
			wd,
			&dbp.os.task, &dbp.os.portSet, &dbp.os.exceptionPort,
			&dbp.os.notificationPort)
		pid = int(ret)
	})
	if pid <= 0 {
		return nil, fmt.Errorf("could not fork/exec")
	}
	dbp.pid = pid
	dbp.childProcess = true
	for i := range argvSlice {
		C.free(unsafe.Pointer(argvSlice[i]))
	}

	// Initialize enough of the Process state so that we can use resume and
	// trapWait to wait until the child process calls execve.

	for {
		task := C.get_task_for_pid(C.int(dbp.pid))
		// The task_for_pid call races with the fork call. This can
		// result in the parent task being returned instead of the child.
		if task != dbp.os.task {
			err = dbp.updateThreadListForTask(task)
			if err == nil {
				break
			}
			if err != couldNotGetThreadCount && err != couldNotGetThreadList {
				return nil, err
			}
		}
	}

	procgrp := &processGroup{procs: []*nativeProcess{dbp}}
	if err := procgrp.resume(); err != nil {
		return nil, err
	}

	for _, th := range dbp.threads {
		th.CurrentBreakpoint.Clear()
	}

	trapthread, err := dbp.trapWait(-1)
	if err != nil {
		return nil, err
	}
	if _, err := dbp.stop(nil); err != nil {
		return nil, err
	}

	dbp.os.initialized = true
	dbp.memthread = trapthread

	tgt, err := dbp.initialize(argv0Go, []string{})
	if err != nil {
		return nil, err
	}

	return tgt, err
}

func waitForSearchProcess(string, map[int]struct{}) (int, error) {
	return 0, proc.ErrWaitForNotImplemented
}

// Attach to an existing process with the given PID.
func Attach(pid int, waitFor *proc.WaitFor, _ []string) (*proc.TargetGroup, error) {
	if waitFor.Valid() {
		return nil, proc.ErrWaitForNotImplemented
	}
	if err := macutil.CheckRosetta(); err != nil {
		return nil, err
	}
	dbp := newProcess(pid)

	kret := C.acquire_mach_task(C.int(pid),
		&dbp.os.task, &dbp.os.portSet, &dbp.os.exceptionPort,
		&dbp.os.notificationPort)

	if kret != C.KERN_SUCCESS {
		return nil, fmt.Errorf("could not attach to %d", pid)
	}

	dbp.os.initialized = true

	var err error
	dbp.execPtraceFunc(func() { err = ptraceAttach(dbp.pid) })
	if err != nil {
		return nil, err
	}
	_, _, err = dbp.wait(dbp.pid, 0)
	if err != nil {
		return nil, err
	}

	tgt, err := dbp.initialize("", []string{})
	if err != nil {
		detachWithoutGroup(dbp, false)
		return nil, err
	}
	return tgt, nil
}

// Kill kills the process.
func (procgrp *processGroup) kill(dbp *nativeProcess) (err error) {
	if dbp.exited {
		return nil
	}
	err = sys.Kill(-dbp.pid, sys.SIGKILL)
	if err != nil {
		return errors.New("could not deliver signal: " + err.Error())
	}
	for port := range dbp.threads {
		if C.thread_resume(C.thread_act_t(port)) != C.KERN_SUCCESS {
			return errors.New("could not resume task")
		}
	}
	for {
		var task C.task_t
		port := C.mach_port_wait(dbp.os.portSet, &task, C.int(0))
		if port == dbp.os.notificationPort {
			break
		}
	}
	dbp.postExit()
	return
}

func (dbp *nativeProcess) requestManualStop() (err error) {
	var (
		task          = C.mach_port_t(dbp.os.task)
		thread        = C.mach_port_t(dbp.memthread.os.threadAct)
		exceptionPort = C.mach_port_t(dbp.os.exceptionPort)
	)
	dbp.os.halt = true
	kret := C.raise_exception(task, thread, exceptionPort, C.EXC_BREAKPOINT)
	if kret != C.KERN_SUCCESS {
		return fmt.Errorf("could not raise mach exception")
	}
	return nil
}

var couldNotGetThreadCount = errors.New("could not get thread count")
var couldNotGetThreadList = errors.New("could not get thread list")

func (dbp *nativeProcess) updateThreadList() error {
	return dbp.updateThreadListForTask(dbp.os.task)
}

func (dbp *nativeProcess) updateThreadListForTask(task C.task_t) error {
	var (
		err   error
		kret  C.kern_return_t
		count C.int
		list  []uint32
	)

	for {
		count = C.thread_count(task)
		if count == -1 {
			return couldNotGetThreadCount
		}
		list = make([]uint32, count)

		// TODO(dp) might be better to malloc mem in C and then free it here
		// instead of getting count above and passing in a slice
		kret = C.get_threads(task, unsafe.Pointer(&list[0]), count)
		if kret != -2 {
			break
		}
	}
	if kret != C.KERN_SUCCESS {
		return couldNotGetThreadList
	}

	for _, thread := range dbp.threads {
		thread.os.exists = false
	}

	for _, port := range list {
		thread, ok := dbp.threads[int(port)]
		if !ok {
			thread, err = dbp.addThread(int(port), false)
			if err != nil {
				return err
			}
		}
		thread.os.exists = true
	}

	for threadID, thread := range dbp.threads {
		if !thread.os.exists {
			delete(dbp.threads, threadID)
		}
	}

	return nil
}

func (dbp *nativeProcess) addThread(port int, attach bool) (*nativeThread, error) {
	if thread, ok := dbp.threads[port]; ok {
		return thread, nil
	}
	thread := &nativeThread{
		ID:  port,
		dbp: dbp,
		os:  new(osSpecificDetails),
	}
	dbp.threads[port] = thread
	thread.os.threadAct = C.thread_act_t(port)
	if dbp.memthread == nil {
		dbp.memthread = thread
	}
	return thread, nil
}

func findExecutable(path string, pid int) string {
	if path == "" {
		path = C.GoString(C.find_executable(C.int(pid)))
	}
	return path
}

func trapWait(procgrp *processGroup, pid int) (*nativeThread, error) {
	return procgrp.procs[0].trapWait(pid)
}

func (dbp *nativeProcess) trapWait(pid int) (*nativeThread, error) {
	for {
		task := dbp.os.task
		port := C.mach_port_wait(dbp.os.portSet, &task, C.int(0))

		switch port {
		case dbp.os.notificationPort:
			// on macOS >= 10.12.1 the task_t changes after an execve, we could
			// receive the notification for the death of the pre-execve task_t,
			// this could also happen *before* we are notified that our task_t has
			// changed.
			if dbp.os.task != task {
				continue
			}
			if !dbp.os.initialized {
				if pidtask := C.get_task_for_pid(C.int(dbp.pid)); pidtask != 0 && dbp.os.task != pidtask {
					continue
				}
			}
			_, status, err := dbp.wait(dbp.pid, 0)
			if err != nil {
				return nil, err
			}
			dbp.postExit()
			return nil, proc.ErrProcessExited{Pid: dbp.pid, Status: status.ExitStatus()}

		case C.MACH_RCV_INTERRUPTED:
			halt := dbp.os.halt
			if !halt {
				// Call trapWait again, it seems
				// MACH_RCV_INTERRUPTED is emitted before
				// process natural death _sometimes_.
				continue
			}
			return nil, nil

		case 0:
			return nil, fmt.Errorf("error while waiting for task")
		}

		// In macOS 10.12.1 if we received a notification for a task other than
		// the inferior's task and the inferior's task is no longer valid, this
		// means inferior called execve and its task_t changed.
		if dbp.os.task != task && C.task_is_valid(dbp.os.task) == 0 {
			dbp.os.task = task
			kret := C.reset_exception_ports(dbp.os.task, &dbp.os.exceptionPort, &dbp.os.notificationPort)
			if kret != C.KERN_SUCCESS {
				return nil, fmt.Errorf("could not follow task across exec: %d\n", kret)
			}
		}

		// Since we cannot be notified of new threads on OS X
		// this is as good a time as any to check for them.
		dbp.updateThreadList()
		th, ok := dbp.threads[int(port)]
		if !ok {
			halt := dbp.os.halt
			if halt {
				dbp.os.halt = false
				return th, nil
			}
			if dbp.firstStart || th.singleStepping {
				dbp.firstStart = false
				return th, nil
			}
			if err := th.resume(); err != nil {
				return nil, err
			}
			continue
		}
		return th, nil
	}
}

func (dbp *nativeProcess) waitForStop() ([]int, error) {
	ports := make([]int, 0, len(dbp.threads))
	count := 0
	for {
		var task C.task_t
		port := C.mach_port_wait(dbp.os.portSet, &task, C.int(1))
		if port != 0 && port != dbp.os.notificationPort && port != C.MACH_RCV_INTERRUPTED {
			count = 0
			ports = append(ports, int(port))
		} else {
			n := C.num_running_threads(dbp.os.task)
			if n == 0 {
				return ports, nil
			} else if n < 0 {
				return nil, fmt.Errorf("error waiting for thread stop %d", n)
			} else if count > 16 {
				return nil, fmt.Errorf("could not stop process %d", n)
			}
		}
	}
}

func (dbp *nativeProcess) wait(pid, options int) (int, *sys.WaitStatus, error) {
	var status sys.WaitStatus
	wpid, err := sys.Wait4(pid, &status, options, nil)
	return wpid, &status, err
}

func killProcess(pid int) error {
	return sys.Kill(pid, sys.SIGINT)
}

func (dbp *nativeProcess) exitGuard(err error) error {
	if err != ErrContinueThread {
		return err
	}
	_, status, werr := dbp.wait(dbp.pid, sys.WNOHANG)
	if werr == nil && status.Exited() {
		dbp.postExit()
		return proc.ErrProcessExited{Pid: dbp.pid, Status: status.ExitStatus()}
	}
	return err
}

func (procgrp *processGroup) resume() error {
	dbp := procgrp.procs[0]
	// all threads stopped over a breakpoint are made to step over it
	for _, thread := range dbp.threads {
		if thread.CurrentBreakpoint.Breakpoint != nil {
			if err := procgrp.stepInstruction(thread); err != nil {
				return err
			}
			thread.CurrentBreakpoint.Clear()
		}
	}
	// everything is resumed
	for _, thread := range dbp.threads {
		if err := thread.resume(); err != nil {
			return dbp.exitGuard(err)
		}
	}
	return nil
}

// stop stops all running threads and sets breakpoints
func (procgrp *processGroup) stop(cctx *proc.ContinueOnceContext, trapthread *nativeThread) (*nativeThread, error) {
	return procgrp.procs[0].stop(trapthread)
}

func (dbp *nativeProcess) stop(trapthread *nativeThread) (*nativeThread, error) {
	if dbp.exited {
		return nil, proc.ErrProcessExited{Pid: dbp.pid}
	}
	for _, th := range dbp.threads {
		if !th.Stopped() {
			if err := th.stop(); err != nil {
				return nil, dbp.exitGuard(err)
			}
		}
	}

	ports, err := dbp.waitForStop()
	if err != nil {
		return nil, err
	}
	if !dbp.os.initialized {
		return nil, nil
	}
	trapthread.SetCurrentBreakpoint(true)
	for _, port := range ports {
		if th, ok := dbp.threads[port]; ok {
			err := th.SetCurrentBreakpoint(true)
			if err != nil {
				return nil, err
			}
		}
	}
	return trapthread, nil
}

func (dbp *nativeProcess) detach(kill bool) error {
	return ptraceDetach(dbp.pid, 0)
}

func (dbp *nativeProcess) EntryPoint() (uint64, error) {
	//TODO(aarzilli): implement this
	return 0, nil
}

func (dbp *nativeProcess) SupportsBPF() bool {
	return false
}

func (dbp *nativeProcess) SetUProbe(fnName string, goidOffset int64, args []ebpf.UProbeArgMap) error {
	panic("not implemented")
}

func (dbp *nativeProcess) GetBufferedTracepoints() []ebpf.RawUProbeParams {
	panic("not implemented")
}

func initialize(dbp *nativeProcess) (string, error) { return "", nil }
