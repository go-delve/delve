package native

// #cgo LDFLAGS: -lprocstat
// #include <stdlib.h>
// #include "proc_freebsd.h"
import "C"
import (
	"fmt"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/internal/ebpf"

	isatty "github.com/mattn/go-isatty"
)

const (
	_PL_FLAG_BORN   = 0x100
	_PL_FLAG_EXITED = 0x200
	_PL_FLAG_SI     = 0x20
)

// Process statuses
const (
	statusIdle     = 1
	statusRunning  = 2
	statusSleeping = 3
	statusStopped  = 4
	statusZombie   = 5
	statusWaiting  = 6
	statusLocked   = 7
)

// osProcessDetails contains FreeBSD specific
// process details.
type osProcessDetails struct {
	comm string
	tid  int

	delayedSignal  syscall.Signal
	trapThreads    []int
	selectedThread *nativeThread
}

func (os *osProcessDetails) Close() {}

// Launch creates and begins debugging a new process. First entry in
// `cmd` is the program to run, and then rest are the arguments
// to be supplied to that process. `wd` is working directory of the program.
// If the DWARF information cannot be found in the binary, Delve will look
// for external debug files in the directories passed in.
func Launch(cmd []string, wd string, flags proc.LaunchFlags, debugInfoDirs []string, tty string, redirects [3]string) (*proc.TargetGroup, error) {
	var (
		process *exec.Cmd
		err     error
	)

	foreground := flags&proc.LaunchForeground != 0

	stdin, stdout, stderr, closefn, err := openRedirects(redirects, foreground)
	if err != nil {
		return nil, err
	}

	if stdin == nil || !isatty.IsTerminal(stdin.Fd()) {
		// exec.(*Process).Start will fail if we try to send a process to
		// foreground but we are not attached to a terminal.
		foreground = false
	}

	dbp := newProcess(0)
	defer func() {
		if err != nil && dbp.pid != 0 {
			_ = dbp.Detach(true)
		}
	}()
	dbp.execPtraceFunc(func() {
		process = exec.Command(cmd[0])
		process.Args = cmd
		process.Stdin = stdin
		process.Stdout = stdout
		process.Stderr = stderr
		process.SysProcAttr = &syscall.SysProcAttr{Ptrace: true, Setpgid: true, Foreground: foreground}
		if foreground {
			signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)
		}
		if tty != "" {
			dbp.ctty, err = attachProcessToTTY(process, tty)
			if err != nil {
				return
			}
		}
		if wd != "" {
			process.Dir = wd
		}
		err = process.Start()
	})
	closefn()
	if err != nil {
		return nil, err
	}
	dbp.pid = process.Process.Pid
	dbp.childProcess = true
	_, _, err = dbp.wait(process.Process.Pid, 0)
	if err != nil {
		return nil, fmt.Errorf("waiting for target execve failed: %s", err)
	}
	tgt, err := dbp.initialize(cmd[0], debugInfoDirs)
	if err != nil {
		return nil, err
	}
	return tgt, nil
}

// Attach to an existing process with the given PID. Once attached, if
// the DWARF information cannot be found in the binary, Delve will look
// for external debug files in the directories passed in.
func Attach(pid int, debugInfoDirs []string) (*proc.TargetGroup, error) {
	dbp := newProcess(pid)

	var err error
	dbp.execPtraceFunc(func() { err = ptraceAttach(dbp.pid) })
	if err != nil {
		return nil, err
	}
	_, _, err = dbp.wait(dbp.pid, 0)
	if err != nil {
		return nil, err
	}

	tgt, err := dbp.initialize(findExecutable("", dbp.pid), debugInfoDirs)
	if err != nil {
		dbp.Detach(false)
		return nil, err
	}
	return tgt, nil
}

func initialize(dbp *nativeProcess) (string, error) {
	comm, _ := C.find_command_name(C.int(dbp.pid))
	defer C.free(unsafe.Pointer(comm))
	comm_str := C.GoString(comm)
	dbp.os.comm = strings.ReplaceAll(string(comm_str), "%", "%%")
	return getCmdLine(dbp.pid), nil
}

// kill kills the target process.
func (dbp *nativeProcess) kill() (err error) {
	if dbp.exited {
		return nil
	}
	dbp.execPtraceFunc(func() {
		for _, th := range dbp.threads {
			ptraceResume(th.ID)
		}
		err = ptraceCont(dbp.pid, int(sys.SIGKILL))
	})
	if err != nil {
		return err
	}
	if _, _, err = dbp.wait(dbp.pid, 0); err != nil {
		return err
	}
	dbp.postExit()
	return nil
}

// Used by RequestManualStop
func (dbp *nativeProcess) requestManualStop() (err error) {
	return sys.Kill(dbp.pid, sys.SIGSTOP)
}

// Attach to a newly created thread, and store that thread in our list of
// known threads.
func (dbp *nativeProcess) addThread(tid int, attach bool) (*nativeThread, error) {
	if thread, ok := dbp.threads[tid]; ok {
		return thread, nil
	}

	var err error
	dbp.execPtraceFunc(func() { err = sys.PtraceLwpEvents(dbp.pid, 1) })
	if err == syscall.ESRCH {
		if _, _, err = dbp.wait(dbp.pid, 0); err != nil {
			return nil, fmt.Errorf("error while waiting after adding process: %d %s", dbp.pid, err)
		}
	}

	dbp.threads[tid] = &nativeThread{
		ID:  tid,
		dbp: dbp,
		os:  new(osSpecificDetails),
	}

	if dbp.memthread == nil {
		dbp.memthread = dbp.threads[tid]
	}

	return dbp.threads[tid], nil
}

// Used by initialize
func (dbp *nativeProcess) updateThreadList() error {
	var tids []int32
	dbp.execPtraceFunc(func() { tids = ptraceGetLwpList(dbp.pid) })
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

func getCmdLine(pid int) string {
	ps := C.procstat_open_sysctl()
	kp := C.kinfo_getproc(C.int(pid))
	argv := C.procstat_getargv(ps, kp, 0)
	goargv := []string{}
	for {
		arg := *argv
		if arg == nil {
			break
		}
		argv = (**C.char)(unsafe.Pointer(uintptr(unsafe.Pointer(argv)) + unsafe.Sizeof(*argv)))
		goargv = append(goargv, C.GoString(arg))
	}
	C.free(unsafe.Pointer(kp))
	C.procstat_close(ps)
	return strings.Join(goargv, " ")
}

func trapWait(procgrp *processGroup, pid int) (*nativeThread, error) {
	return procgrp.procs[0].trapWaitInternal(pid, trapWaitNormal)
}

type trapWaitMode uint8

const (
	trapWaitNormal trapWaitMode = iota
	trapWaitStepping
)

// Used by stop and trapWait
func (dbp *nativeProcess) trapWaitInternal(pid int, mode trapWaitMode) (*nativeThread, error) {
	if dbp.os.selectedThread != nil {
		th := dbp.os.selectedThread
		dbp.os.selectedThread = nil
		return th, nil
	}
	for {
		wpid, status, err := dbp.wait(pid, 0)
		if wpid != dbp.pid {
			// possibly a delayed notification from a process we just detached and killed, freebsd bug?
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("wait err %s %d", err, pid)
		}
		if status.Killed() {
			// "Killed" status may arrive as a result of a Process.Kill() of some other process in
			// the system performed by the same tracer (e.g. in the previous test)
			continue
		}
		if status.Exited() {
			dbp.postExit()
			return nil, proc.ErrProcessExited{Pid: wpid, Status: status.ExitStatus()}
		}
		if status.Signaled() {
			// Killed by a signal
			dbp.postExit()
			return nil, proc.ErrProcessExited{Pid: wpid, Status: -int(status.Signal())}
		}

		var info sys.PtraceLwpInfoStruct
		dbp.execPtraceFunc(func() { info, err = ptraceGetLwpInfo(wpid) })
		if err != nil {
			return nil, fmt.Errorf("ptraceGetLwpInfo err %s %d", err, pid)
		}
		tid := int(info.Lwpid)
		pl_flags := int(info.Flags)
		th, ok := dbp.threads[tid]
		if ok {
			th.Status = (*waitStatus)(status)
		}

		if status.StopSignal() == sys.SIGTRAP {
			if pl_flags&_PL_FLAG_EXITED != 0 {
				delete(dbp.threads, tid)
				dbp.execPtraceFunc(func() { err = ptraceCont(tid, 0) })
				if err != nil {
					return nil, err
				}
				continue
			} else if pl_flags&_PL_FLAG_BORN != 0 {
				th, err = dbp.addThread(int(tid), false)
				if err != nil {
					if err == sys.ESRCH {
						// process died while we were adding it
						continue
					}
					return nil, err
				}
				if mode == trapWaitStepping {
					dbp.execPtraceFunc(func() { ptraceSuspend(tid) })
				}
				if err = dbp.ptraceCont(0); err != nil {
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

		if mode == trapWaitStepping {
			return th, nil
		}
		if status.StopSignal() == sys.SIGTRAP || status.Continued() {
			// Continued in this case means we received the SIGSTOP signal
			return th, nil
		}

		// TODO(dp) alert user about unexpected signals here.
		if err := dbp.ptraceCont(int(status.StopSignal())); err != nil {
			if err == sys.ESRCH {
				return nil, proc.ErrProcessExited{Pid: dbp.pid}
			}
			return nil, err
		}
	}
}

// Helper function used here and in threads_freebsd.go
// Return the status code
func status(pid int) rune {
	status := rune(C.find_status(C.int(pid)))
	return status
}

// Only used in this file
func (dbp *nativeProcess) wait(pid, options int) (int, *sys.WaitStatus, error) {
	var s sys.WaitStatus
	wpid, err := sys.Wait4(pid, &s, options, nil)
	return wpid, &s, err
}

// Only used in this file
func (dbp *nativeProcess) exitGuard(err error) error {
	if err != sys.ESRCH {
		return err
	}
	if status(dbp.pid) == statusZombie {
		_, err := dbp.trapWaitInternal(-1, trapWaitNormal)
		return err
	}

	return err
}

// Used by ContinueOnce
func (dbp *nativeProcess) resume() error {
	// all threads stopped over a breakpoint are made to step over it
	for _, thread := range dbp.threads {
		if thread.CurrentBreakpoint.Breakpoint != nil {
			if err := thread.StepInstruction(); err != nil {
				return err
			}
			thread.CurrentBreakpoint.Clear()
		}
	}
	if len(dbp.os.trapThreads) > 0 {
		// On these threads we have already received a SIGTRAP while stepping on a different thread,
		// do not resume the process, instead record the breakpoint hits on them.
		tt := dbp.os.trapThreads
		dbp.os.trapThreads = dbp.os.trapThreads[:0]
		var err error
		dbp.os.selectedThread, err = dbp.postStop(tt...)
		return err
	}
	// all threads are resumed
	var err error
	dbp.execPtraceFunc(func() {
		for _, th := range dbp.threads {
			err = ptraceResume(th.ID)
			if err != nil {
				return
			}
		}
		sig := int(dbp.os.delayedSignal)
		dbp.os.delayedSignal = 0
		for _, thread := range dbp.threads {
			thread.Status = nil
		}
		err = ptraceCont(dbp.pid, sig)
	})
	return err
}

// Used by ContinueOnce
// stop stops all running threads and sets breakpoints
func (procgrp *processGroup) stop(cctx *proc.ContinueOnceContext, trapthread *nativeThread) (*nativeThread, error) {
	return procgrp.procs[0].stop(trapthread)
}

func (dbp *nativeProcess) stop(trapthread *nativeThread) (*nativeThread, error) {
	if dbp.exited {
		return nil, proc.ErrProcessExited{Pid: dbp.pid}
	}

	var err error
	dbp.execPtraceFunc(func() {
		for _, th := range dbp.threads {
			err = ptraceSuspend(th.ID)
			if err != nil {
				return
			}
		}
	})
	if err != nil {
		return nil, err
	}

	if trapthread.Status == nil || (*sys.WaitStatus)(trapthread.Status).StopSignal() != sys.SIGTRAP {
		return trapthread, nil
	}

	return dbp.postStop(trapthread.ID)
}

func (dbp *nativeProcess) postStop(tids ...int) (*nativeThread, error) {
	var pickedTrapThread *nativeThread
	for _, tid := range tids {
		trapthread := dbp.threads[tid]
		if trapthread == nil {
			continue
		}

		// Must either be a hardcoded breakpoint, a currently set breakpoint or a
		// phantom breakpoint hit caused by a SIGTRAP that was delayed.
		// If someone sent a SIGTRAP directly to the process this will fail, but we
		// can't do better.

		err := trapthread.SetCurrentBreakpoint(true)
		if err != nil {
			return nil, err
		}

		if trapthread.CurrentBreakpoint.Breakpoint == nil {
			// hardcoded breakpoint or phantom breakpoint hit
			if dbp.BinInfo().Arch.BreakInstrMovesPC() {
				pc, _ := trapthread.PC()
				if !trapthread.atHardcodedBreakpoint(pc) {
					// phantom breakpoint hit
					_ = trapthread.setPC(pc - uint64(len(dbp.BinInfo().Arch.BreakpointInstruction())))
					trapthread = nil
				}
			}
		}

		if pickedTrapThread == nil && trapthread != nil {
			pickedTrapThread = trapthread
		}
	}

	return pickedTrapThread, nil
}

// Used by Detach
func (dbp *nativeProcess) detach(kill bool) error {
	if !kill {
		for _, th := range dbp.threads {
			ptraceResume(th.ID)
		}
	}
	return ptraceDetach(dbp.pid)
}

// Used by PostInitializationSetup
// EntryPoint will return the process entry point address, useful for debugging PIEs.
func (dbp *nativeProcess) EntryPoint() (uint64, error) {
	ep, err := C.get_entry_point(C.int(dbp.pid))
	return uint64(ep), err
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

func (dbp *nativeProcess) ptraceCont(sig int) error {
	var err error
	dbp.execPtraceFunc(func() { err = ptraceCont(dbp.pid, sig) })
	return err
}

// Usedy by Detach
func killProcess(pid int) error {
	return sys.Kill(pid, sys.SIGINT)
}
