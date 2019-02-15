package native

// #cgo LDFLAGS: -lprocstat
// #include <stdlib.h>
// #include "proc_freebsd.h"
import "C"
import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"

	isatty "github.com/mattn/go-isatty"
)

// Process statuses
const (
	StatusIdle     = 1
	StatusRunning  = 2
	StatusSleeping = 3
	StatusStopped  = 4
	StatusZombie   = 5
	StatusWaiting  = 6
	StatusLocked   = 7
)

// OSProcessDetails contains FreeBSD specific
// process details.
type OSProcessDetails struct {
	comm string
	tid  int
}

// Launch creates and begins debugging a new process. First entry in
// `cmd` is the program to run, and then rest are the arguments
// to be supplied to that process. `wd` is working directory of the program.
// If the DWARF information cannot be found in the binary, Delve will look
// for external debug files in the directories passed in.
func Launch(cmd []string, wd string, foreground bool, debugInfoDirs []string) (*Process, error) {
	var (
		process *exec.Cmd
		err     error
	)
	// check that the argument to Launch is an executable file
	if fi, staterr := os.Stat(cmd[0]); staterr == nil && (fi.Mode()&0111) == 0 {
		return nil, proc.ErrNotExecutable
	}

	if !isatty.IsTerminal(os.Stdin.Fd()) {
		// exec.(*Process).Start will fail if we try to send a process to
		// foreground but we are not attached to a terminal.
		foreground = false
	}

	dbp := New(0)
	dbp.common = proc.NewCommonProcess(true)
	dbp.execPtraceFunc(func() {
		process = exec.Command(cmd[0])
		process.Args = cmd
		process.Stdout = os.Stdout
		process.Stderr = os.Stderr
		process.SysProcAttr = &syscall.SysProcAttr{Ptrace: true, Setpgid: true, Foreground: foreground}
		if foreground {
			signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)
			process.Stdin = os.Stdin
		}
		if wd != "" {
			process.Dir = wd
		}
		err = process.Start()
	})
	if err != nil {
		return nil, err
	}
	dbp.pid = process.Process.Pid
	dbp.childProcess = true
	_, _, err = dbp.wait(process.Process.Pid, 0)
	if err != nil {
		return nil, fmt.Errorf("waiting for target execve failed: %s", err)
	}
	if err = dbp.initialize(cmd[0], debugInfoDirs); err != nil {
		return nil, err
	}
	return dbp, nil
}

// Attach to an existing process with the given PID. Once attached, if
// the DWARF information cannot be found in the binary, Delve will look
// for external debug files in the directories passed in.
func Attach(pid int, debugInfoDirs []string) (*Process, error) {
	dbp := New(pid)
	dbp.common = proc.NewCommonProcess(true)

	var err error
	dbp.execPtraceFunc(func() { err = PtraceAttach(dbp.pid) })
	if err != nil {
		return nil, err
	}
	_, _, err = dbp.wait(dbp.pid, 0)
	if err != nil {
		return nil, err
	}

	err = dbp.initialize(findExecutable("", dbp.pid), debugInfoDirs)
	if err != nil {
		dbp.Detach(false)
		return nil, err
	}
	return dbp, nil
}

func initialize(dbp *Process) error {
	comm, _ := C.find_command_name(C.int(dbp.pid))
	defer C.free(unsafe.Pointer(comm))
	comm_str := C.GoString(comm)
	dbp.os.comm = strings.Replace(string(comm_str), "%", "%%", -1)
	return nil
}

// kill kills the target process.
func (dbp *Process) kill() (err error) {
	if dbp.exited {
		return nil
	}

	if err = sys.Kill(-dbp.pid, sys.SIGKILL); err != nil {
		return errors.New("could not deliver signal " + err.Error())
	}
	// If the process is stopped, we must continue it so it can receive the signal
	PtraceCont(dbp.pid, 0)
	if _, _, err = dbp.wait(dbp.pid, 0); err != nil {
		return
	}
	dbp.postExit()
	return
}

// Used by RequestManualStop
func (dbp *Process) requestManualStop() (err error) {
	return sys.Kill(dbp.pid, sys.SIGTRAP)
}

// Attach to a newly created thread, and store that thread in our list of
// known threads.
func (dbp *Process) addThread(tid int, attach bool) (*Thread, error) {
	if thread, ok := dbp.threads[tid]; ok {
		return thread, nil
	}

	var err error
	dbp.execPtraceFunc(func() { err = sys.PtraceLwpEvents(dbp.pid, 1) })
	if err == syscall.ESRCH {
		// XXX why do we wait here?
		if _, _, err = dbp.waitFast(dbp.pid); err != nil {
			return nil, fmt.Errorf("error while waiting after adding process: %d %s", dbp.pid, err)
		}
	}

	dbp.threads[tid] = &Thread{
		ID:  tid,
		dbp: dbp,
		os:  new(OSSpecificDetails),
	}

	if dbp.currentThread == nil {
		dbp.SwitchThread(tid)
	}

	return dbp.threads[tid], nil
}

// Used by initialize
func (dbp *Process) updateThreadList() error {
	tids := PtraceGetLwpList(dbp.pid)
	for _, tid := range tids {
		if _, err := dbp.addThread(int(tid), false); err != nil {
			return err
		}
	}
	dbp.os.tid = int(tids[0])
	return nil
}

// Used by Attach
func findExecutable(path string, pid int) string {
	if path == "" {
		cstr := C.find_executable(C.int(pid))
		defer C.free(unsafe.Pointer(cstr))
		path = C.GoString(cstr)
	}
	return path
}

func (dbp *Process) trapWait(pid int) (*Thread, error) {
	return dbp.trapWaitInternal(pid, false)
}

// Used by stop and trapWait
func (dbp *Process) trapWaitInternal(pid int, halt bool) (*Thread, error) {
	for {
		wpid, status, err := dbp.wait(pid, 0)
		if err != nil {
			return nil, fmt.Errorf("wait err %s %d", err, pid)
		}

		if status.Exited() {
			dbp.postExit()
			return nil, proc.ErrProcessExited{Pid: wpid, Status: status.ExitStatus()}
		}

		tid, pl_flags, _, err := ptraceGetLwpInfo(wpid)
		if err != nil {
			return nil, fmt.Errorf("ptraceGetLwpInfo err %s %d", err, pid)
		}
		th, ok := dbp.threads[tid]
		if ok {
			th.Status = (*WaitStatus)(status)
		}

		if status.StopSignal() == sys.SIGTRAP {
			if pl_flags&sys.PL_FLAG_EXITED != 0 {
				delete(dbp.threads, tid)
				PtraceCont(tid, 0)
				continue
			} else if pl_flags&sys.PL_FLAG_BORN != 0 {
				th, err = dbp.addThread(int(tid), false)
				if err != nil {
					if err == sys.ESRCH {
						// process died while we were adding it
						continue
					}
					return nil, err
				}
				if halt {
					return nil, nil
				}
				if err = th.Continue(); err != nil {
					if err == sys.ESRCH {
						// thread died while we were adding it
						delete(dbp.threads, int(tid))
						continue
					}
					return nil, fmt.Errorf("could not continue new thread %d %s", tid, err)
				}
				continue
			}
		}

		if th == nil {
			continue
		}

		if (halt && status.StopSignal() == sys.SIGSTOP) || (status.StopSignal() == sys.SIGTRAP) {
			return th, nil
		}

		// TODO(dp) alert user about unexpected signals here.
		if err := th.resumeWithSig(int(status.StopSignal())); err != nil {
			if err == sys.ESRCH {
				return nil, proc.ErrProcessExited{Pid: dbp.pid}
			}
			return nil, err
		}
	}
}

func (dbp *Process) loadProcessInformation() {
}

// Helper function used here and in threads_freebsd.go
// Return the status code
func status(pid int) rune {
	status := rune(C.find_status(C.int(pid)))
	return status
}

// Used by stop and singleStep
// waitFast is like wait but does not handle process-exit correctly
func (dbp *Process) waitFast(pid int) (int, *sys.WaitStatus, error) {
	var s sys.WaitStatus
	wpid, err := sys.Wait4(pid, &s, 0, nil)
	return wpid, &s, err
}

// Only used in this file
func (dbp *Process) wait(pid, options int) (int, *sys.WaitStatus, error) {
	var s sys.WaitStatus
	wpid, err := sys.Wait4(pid, &s, options, nil)
	return wpid, &s, err
}

// Only used in this file
func (dbp *Process) exitGuard(err error) error {
	if err != sys.ESRCH {
		return err
	}
	if status(dbp.pid) == StatusZombie {
		_, err := dbp.trapWaitInternal(-1, false)
		return err
	}

	return err
}

// Used by ContinueOnce
func (dbp *Process) resume() error {
	// all threads stopped over a breakpoint are made to step over it
	for _, thread := range dbp.threads {
		if thread.CurrentBreakpoint.Breakpoint != nil {
			if err := thread.StepInstruction(); err != nil {
				return err
			}
			thread.CurrentBreakpoint.Clear()
		}
	}
	// all threads are resumed
	PtraceCont(dbp.pid, 0)
	return nil
}

// Used by ContinueOnce
// stop stops all running threads and sets breakpoints
func (dbp *Process) stop(trapthread *Thread) (err error) {
	if dbp.exited {
		return &proc.ErrProcessExited{Pid: dbp.Pid()}
	}
	// set breakpoints on all threads
	for _, th := range dbp.threads {
		if th.CurrentBreakpoint.Breakpoint == nil {
			if err := th.SetCurrentBreakpoint(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Used by Detach
func (dbp *Process) detach(kill bool) error {
	err := PtraceDetach(dbp.pid, 0)
	if err != nil {
		return err
	}
	if kill {
		return nil
	}
	return nil
}

// Used by PostInitializationSetup
// EntryPoint will return the process entry point address, useful for debugging PIEs.
func (dbp *Process) EntryPoint() (uint64, error) {
	ep, err := C.get_entry_point(C.int(dbp.pid))
	return uint64(ep), err
}

// Usedy by Detach
func killProcess(pid int) error {
	return sys.Kill(pid, sys.SIGINT)
}
