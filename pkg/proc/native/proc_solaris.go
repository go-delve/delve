package native

// #include <libproc.h>
// #include "Pcreate_solaris.h"
import "C"

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/internal/ebpf"
)

// osProcessDetails contains Solaris specific
// process details.
type osProcessDetails struct {
	pr         *C.struct_ps_prochandle
	path       string
	exitStatus int
	waitDone   chan struct{}
}

func (os *osProcessDetails) Close() {}

// Launch creates and begins debugging a new process. First entry in
// `cmd` is the program to run, and then rest are the arguments
// to be supplied to that process. `wd` is working directory of the program.
// If the DWARF information cannot be found in the binary, Delve will look
// for external debug files in the directories passed in.
func Launch(cmd []string, wd string, flags proc.LaunchFlags, debugInfoDirs []string, tty string, redirects [3]string) (*proc.Target, error) {
	if wd != "" && wd != "." {
		if _, err := os.Stat(wd); err != nil {
			return nil, err
		}
	}
	foreground := flags&proc.LaunchForeground != 0
	stdin, stdout, stderr, closefn, err := openRedirects(redirects, foreground)
	if err != nil {
		return nil, err
	}
	dbp := newProcess(0)
	dbp.execPtraceFunc(func() {
		C.Pcreate_stdin = C.int(stdin.Fd())
		C.Pcreate_stdout = C.int(stdout.Fd())
		C.Pcreate_stderr = C.int(stderr.Fd())
		if wd != "" {
			C.Pcreate_dir = C.CString(wd)
		}
		dbp.os.pr, dbp.os.path, err = Pcreate_helper(cmd[0], cmd)
		if err == nil {
			dbp.pid = int(C.Pstatus(dbp.os.pr).pr_pid)
		}
		C.free(unsafe.Pointer(C.Pcreate_dir))
		C.Pcreate_dir = (*C.char)(C.NULL)
	})
	closefn()
	if err != nil {
		return nil, err
	}
	dbp.childProcess = true
	tgt, err := dbp.initialize(dbp.os.path, debugInfoDirs)
	dbp.os.waitDone = make(chan struct{})
	go waitThread(dbp)
	if err != nil {
		dbp.Detach(true)
		return nil, err
	}
	return tgt, nil
}

// Attach to an existing process with the given PID. Once attached, if
// the DWARF information cannot be found in the binary, Delve will look
// for external debug files in the directories passed in.
func Attach(pid int, debugInfoDirs []string) (*proc.Target, error) {
	dbp := newProcess(pid)
	var code C.int
	dbp.execPtraceFunc(func() { dbp.os.pr = C.Pgrab(C.int(pid), 0, &code) })
	if dbp.os.pr == nil {
		return nil, errors.New(C.GoString(C.Pgrab_error(code)))
	}
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/path/a.out", pid))
	if err != nil {
		return nil, err
	}
	var tgt *proc.Target
	if tgt, err = dbp.initialize(exe, debugInfoDirs); err != nil {
		dbp.Detach(false)
		return nil, err
	}
	dbp.os.waitDone = make(chan struct{})
	go waitThread(dbp)
	return tgt, nil
}

func waitThread(dbp *nativeProcess) {
	if _, s, err := dbp.wait(dbp.pid, 0); err == nil {
		dbp.os.exitStatus = s.ExitStatus()
	}
	close(dbp.os.waitDone)
}

func initialize(dbp *nativeProcess) (err error) {
	dbp.execPtraceFunc(func() {
		// The process should already be stopped if we grabbed or created it,
		// but if we created it it will be stopped on the execve syscall and
		// for whatever reason trying to step it will cause it to exit.
		// Telling it to run and stop seems to workaround the issue.
		// NB: this does not as far as I know cause the process to advance,
		// it just causes the reason for the stop to change from PR_SYSEXIT
		// to PR_REQUESTED, and that seems to make a difference.
		if rc, err1 := C.Psetrun(dbp.os.pr, 0, C.PRSTOP); rc == -1 {
			err = err1
		} else if rc, err1 = C.Pfault(dbp.os.pr, C.FLTTRACE, 1); rc == -1 {
			err = err1
		} else if rc, err1 = C.Pfault(dbp.os.pr, C.FLTWATCH, 1); rc == -1 {
			err = err1
		} else if rc, err1 = C.Pfault(dbp.os.pr, C.FLTBPT, 1); rc == -1 {
			err = err1
		}
	})
	return
}

// kill kills the target process.
func (dbp *nativeProcess) kill() (err error) {
	if dbp.exited {
		return nil
	}
	dbp.execPtraceFunc(func() {
		if rc, err1 := C.Psetrun(dbp.os.pr, C.SIGKILL, 0); rc == -1 {
			err = err1
		}
	})
	if err != nil {
		return err
	}
	<-dbp.os.waitDone
	dbp.postExit()
	return nil
}

// Used by RequestManualStop
func (dbp *nativeProcess) requestManualStop() (err error) {
	dbp.execPtraceFunc(func() {
		if rc, err1 := C.Pdstop(dbp.os.pr); rc == -1 {
			err = err1
		}
	})
	return
}

// Attach to a newly created thread, and store that thread in our list of
// known threads.
func (dbp *nativeProcess) addThread(tid int, attach bool) (*nativeThread, error) {
	if thread, ok := dbp.threads[tid]; ok {
		return thread, nil
	}
	var lwp *C.struct_ps_lwphandle
	var code C.int
	dbp.execPtraceFunc(func() { lwp = C.Lgrab(dbp.os.pr, C.uint(tid), &code) })
	if lwp == nil {
		return nil, errors.New(C.GoString(C.Pgrab_error(code)))
	}
	dbp.threads[tid] = &nativeThread{
		ID:  tid,
		dbp: dbp,
		os:  &osSpecificDetails{lwp: lwp},
	}
	if dbp.memthread == nil {
		dbp.memthread = dbp.threads[tid]
	}
	return dbp.threads[tid], nil
}

// Used by initialize
func (dbp *nativeProcess) updateThreadList() error {
	var tids []int32
	dbp.execPtraceFunc(func() {
		files, err := ioutil.ReadDir(fmt.Sprintf("/proc/%d/lwp", dbp.pid))
		if err != nil {
			return
		}
		for _, file := range files {
			i, err := strconv.Atoi(file.Name())
			if err == nil {
				tids = append(tids, int32(i))
			}
		}
	})
	for _, tid := range tids {
		if _, err := dbp.addThread(int(tid), false); err != nil {
			return err
		}
	}
	return nil
}

func (dbp *nativeProcess) trapWait(pid int) (*nativeThread, error) {
	if pid != -1 {
		panic("not implemented")
	}
	for {
		// refresh the state stored in the proc handle
		var err error
		dbp.execPtraceFunc(func() {
			if rc, err1 := C.Pstopstatus(dbp.os.pr, C.PCNULL, 0); rc == -1 {
				err = err1
			}
		})
		if err != nil {
			if errors.Is(syscall.ENOENT, err) {
				<-dbp.os.waitDone
				dbp.postExit()
				return nil, proc.ErrProcessExited{Pid: dbp.pid, Status: dbp.os.exitStatus}
			}
			return nil, err
		}
		var state C.int
		dbp.execPtraceFunc(func() { state = C.Pstate(dbp.os.pr) })
		switch state {
		case C.PS_RUN, C.PS_UNDEAD:
			time.Sleep(time.Millisecond)
		case C.PS_STOP:
			var lwp C.struct_lwpstatus
			dbp.execPtraceFunc(func() { lwp = C.Pstatus(dbp.os.pr).pr_lwp })
			switch lwp.pr_why {
			case C.PR_REQUESTED:
				return dbp.addThread(int(lwp.pr_lwpid), false)
			case C.PR_FAULTED:
				switch lwp.pr_what {
				case C.FLTBPT, C.FLTTRACE, C.FLTWATCH:
					return dbp.addThread(int(lwp.pr_lwpid), false)
				default:
					panic(fmt.Sprintf("unexpected pr_what: %d", int(lwp.pr_what)))
				}
			default:
				panic(fmt.Sprintf("unexpected pr_why: %d", int(lwp.pr_why)))
			}
		case C.PS_LOST:
			return nil, errors.New("trapWait: lost control")
		default:
			panic(fmt.Sprintf("unexpected state: %d", state))
		}
		// check for new threads
		dbp.updateThreadList()
	}
}

// Only used in this file
func (dbp *nativeProcess) wait(pid, options int) (int, *sys.WaitStatus, error) {
	var s sys.WaitStatus
	wpid, err := sys.Wait4(pid, &s, options, nil)
	return wpid, &s, err
}

// Used by ContinueOnce
func (dbp *nativeProcess) resume() (err error) {
	// all threads stopped over a breakpoint are made to step over it
	for _, thread := range dbp.threads {
		if thread.CurrentBreakpoint.Breakpoint != nil {
			if err = thread.StepInstruction(); err != nil {
				return err
			}
			thread.CurrentBreakpoint.Clear()
		}
	}
	// all threads are resumed
	dbp.execPtraceFunc(func() {
		if rc, err1 := C.Psetrun(dbp.os.pr, 0, C.PRCFAULT); rc == -1 {
			err = err1
		}
	})
	return
}

// Used by ContinueOnce
// stop stops all running threads and sets breakpoints
func (dbp *nativeProcess) stop(trapthread *nativeThread) (*nativeThread, error) {
	if dbp.exited {
		return nil, &proc.ErrProcessExited{Pid: dbp.Pid()}
	}
	// set breakpoints on all threads
	for _, th := range dbp.threads {
		if th.CurrentBreakpoint.Breakpoint == nil {
			if err := th.SetCurrentBreakpoint(true); err != nil {
				return nil, err
			}
		}
	}
	return trapthread, nil
}

// Used by Detach
func (dbp *nativeProcess) detach(kill bool) error {
	// this function is called from execPtraceFunc
	C.Pclearfault(dbp.os.pr)
	C.Prelease(dbp.os.pr, C.PRELEASE_CLEAR)
	dbp.os.pr = (*C.struct_ps_prochandle)(C.NULL)
	return nil
}

// Used by PostInitializationSetup
// EntryPoint will return the process entry point address, useful for debugging PIEs.
func (dbp *nativeProcess) EntryPoint() (addr uint64, err error) {
	dbp.execPtraceFunc(func() {
		if rc, err1 := C.Pgetauxval(dbp.os.pr, C.AT_ENTRY); rc == -1 {
			err = err1
		} else {
			addr = uint64(rc)
		}
	})
	return
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

// Used by Detach
func killProcess(pid int) error {
	return sys.Kill(pid, sys.SIGINT)
}
