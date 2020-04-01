package native

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	sys "golang.org/x/sys/unix"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"

	isatty "github.com/mattn/go-isatty"
)

// Process statuses
const (
	statusSleeping  = 'S'
	statusRunning   = 'R'
	statusTraceStop = 't'
	statusZombie    = 'Z'

	// Kernel 2.6 has TraceStop as T
	// TODO(derekparker) Since this means something different based on the
	// version of the kernel ('T' is job control stop on modern 3.x+ kernels) we
	// may want to differentiate at some point.
	statusTraceStopT = 'T'
)

// osProcessDetails contains Linux specific
// process details.
type osProcessDetails struct {
	comm string
}

// Launch creates and begins debugging a new process. First entry in
// `cmd` is the program to run, and then rest are the arguments
// to be supplied to that process. `wd` is working directory of the program.
// If the DWARF information cannot be found in the binary, Delve will look
// for external debug files in the directories passed in.
func Launch(cmd []string, wd string, foreground bool, debugInfoDirs []string) (*proc.Target, error) {
	var (
		process *exec.Cmd
		err     error
	)

	if !isatty.IsTerminal(os.Stdin.Fd()) {
		// exec.(*Process).Start will fail if we try to send a process to
		// foreground but we are not attached to a terminal.
		foreground = false
	}

	dbp := newProcess(0)
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
	tgt, err := dbp.initialize(cmd[0], debugInfoDirs)
	if err != nil {
		return nil, err
	}
	return tgt, nil
}

// Attach to an existing process with the given PID. Once attached, if
// the DWARF information cannot be found in the binary, Delve will look
// for external debug files in the directories passed in.
func Attach(pid int, debugInfoDirs []string) (*proc.Target, error) {
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

	// ElfUpdateSharedObjects can only be done after we initialize because it
	// needs an initialized BinaryInfo object to work.
	err = linutil.ElfUpdateSharedObjects(dbp)
	if err != nil {
		return nil, err
	}
	return tgt, nil
}

func initialize(dbp *nativeProcess) error {
	comm, err := ioutil.ReadFile(fmt.Sprintf("/proc/%d/comm", dbp.pid))
	if err == nil {
		// removes newline character
		comm = bytes.TrimSuffix(comm, []byte("\n"))
	}

	if comm == nil || len(comm) <= 0 {
		stat, err := ioutil.ReadFile(fmt.Sprintf("/proc/%d/stat", dbp.pid))
		if err != nil {
			return fmt.Errorf("could not read proc stat: %v", err)
		}
		expr := fmt.Sprintf("%d\\s*\\((.*)\\)", dbp.pid)
		rexp, err := regexp.Compile(expr)
		if err != nil {
			return fmt.Errorf("regexp compile error: %v", err)
		}
		match := rexp.FindSubmatch(stat)
		if match == nil {
			return fmt.Errorf("no match found using regexp '%s' in /proc/%d/stat", expr, dbp.pid)
		}
		comm = match[1]
	}
	dbp.os.comm = strings.Replace(string(comm), "%", "%%", -1)
	return nil
}

// kill kills the target process.
func (dbp *nativeProcess) kill() (err error) {
	if dbp.exited {
		return nil
	}
	if !dbp.threads[dbp.pid].Stopped() {
		return errors.New("process must be stopped in order to kill it")
	}
	if err = sys.Kill(-dbp.pid, sys.SIGKILL); err != nil {
		return errors.New("could not deliver signal " + err.Error())
	}
	if _, _, err = dbp.wait(dbp.pid, 0); err != nil {
		return
	}
	dbp.postExit()
	return
}

func (dbp *nativeProcess) requestManualStop() (err error) {
	return sys.Kill(dbp.pid, sys.SIGTRAP)
}

// Attach to a newly created thread, and store that thread in our list of
// known threads.
func (dbp *nativeProcess) addThread(tid int, attach bool) (*nativeThread, error) {
	if thread, ok := dbp.threads[tid]; ok {
		return thread, nil
	}

	var err error
	if attach {
		dbp.execPtraceFunc(func() { err = sys.PtraceAttach(tid) })
		if err != nil && err != sys.EPERM {
			// Do not return err if err == EPERM,
			// we may already be tracing this thread due to
			// PTRACE_O_TRACECLONE. We will surely blow up later
			// if we truly don't have permissions.
			return nil, fmt.Errorf("could not attach to new thread %d %s", tid, err)
		}
		pid, status, err := dbp.waitFast(tid)
		if err != nil {
			return nil, err
		}
		if status.Exited() {
			return nil, fmt.Errorf("thread already exited %d", pid)
		}
	}

	dbp.execPtraceFunc(func() { err = syscall.PtraceSetOptions(tid, syscall.PTRACE_O_TRACECLONE) })
	if err == syscall.ESRCH {
		if _, _, err = dbp.waitFast(tid); err != nil {
			return nil, fmt.Errorf("error while waiting after adding thread: %d %s", tid, err)
		}
		dbp.execPtraceFunc(func() { err = syscall.PtraceSetOptions(tid, syscall.PTRACE_O_TRACECLONE) })
		if err == syscall.ESRCH {
			return nil, err
		}
		if err != nil {
			return nil, fmt.Errorf("could not set options for new traced thread %d %s", tid, err)
		}
	}

	dbp.threads[tid] = &nativeThread{
		ID:  tid,
		dbp: dbp,
		os:  new(osSpecificDetails),
	}
	if dbp.currentThread == nil {
		dbp.currentThread = dbp.threads[tid]
	}
	return dbp.threads[tid], nil
}

func (dbp *nativeProcess) updateThreadList() error {
	tids, _ := filepath.Glob(fmt.Sprintf("/proc/%d/task/*", dbp.pid))
	for _, tidpath := range tids {
		tidstr := filepath.Base(tidpath)
		tid, err := strconv.Atoi(tidstr)
		if err != nil {
			return err
		}
		if _, err := dbp.addThread(tid, tid != dbp.pid); err != nil {
			return err
		}
	}
	return linutil.ElfUpdateSharedObjects(dbp)
}

func findExecutable(path string, pid int) string {
	if path == "" {
		path = fmt.Sprintf("/proc/%d/exe", pid)
	}
	return path
}

func (dbp *nativeProcess) trapWait(pid int) (*nativeThread, error) {
	return dbp.trapWaitInternal(pid, 0)
}

type trapWaitOptions uint8

const (
	trapWaitHalt trapWaitOptions = 1 << iota
	trapWaitNohang
)

func (dbp *nativeProcess) trapWaitInternal(pid int, options trapWaitOptions) (*nativeThread, error) {
	halt := options&trapWaitHalt != 0
	for {
		wopt := 0
		if options&trapWaitNohang != 0 {
			wopt = sys.WNOHANG
		}
		wpid, status, err := dbp.wait(pid, wopt)
		if err != nil {
			return nil, fmt.Errorf("wait err %s %d", err, pid)
		}
		if wpid == 0 {
			if options&trapWaitNohang != 0 {
				return nil, nil
			}
			continue
		}
		th, ok := dbp.threads[wpid]
		if ok {
			th.Status = (*waitStatus)(status)
		}
		if status.Exited() {
			if wpid == dbp.pid {
				dbp.postExit()
				return nil, proc.ErrProcessExited{Pid: wpid, Status: status.ExitStatus()}
			}
			delete(dbp.threads, wpid)
			continue
		}
		if status.StopSignal() == sys.SIGTRAP && status.TrapCause() == sys.PTRACE_EVENT_CLONE {
			// A traced thread has cloned a new thread, grab the pid and
			// add it to our list of traced threads.
			var cloned uint
			dbp.execPtraceFunc(func() { cloned, err = sys.PtraceGetEventMsg(wpid) })
			if err != nil {
				if err == sys.ESRCH {
					// thread died while we were adding it
					continue
				}
				return nil, fmt.Errorf("could not get event message: %s", err)
			}
			th, err = dbp.addThread(int(cloned), false)
			if err != nil {
				if err == sys.ESRCH {
					// thread died while we were adding it
					delete(dbp.threads, int(cloned))
					continue
				}
				return nil, err
			}
			if halt {
				th.os.running = false
				dbp.threads[int(wpid)].os.running = false
				return nil, nil
			}
			if err = th.Continue(); err != nil {
				if err == sys.ESRCH {
					// thread died while we were adding it
					delete(dbp.threads, th.ID)
					continue
				}
				return nil, fmt.Errorf("could not continue new thread %d %s", cloned, err)
			}
			if err = dbp.threads[int(wpid)].Continue(); err != nil {
				if err != sys.ESRCH {
					return nil, fmt.Errorf("could not continue existing thread %d %s", wpid, err)
				}
			}
			continue
		}
		if th == nil {
			// Sometimes we get an unknown thread, ignore it?
			continue
		}
		if (halt && status.StopSignal() == sys.SIGSTOP) || (status.StopSignal() == sys.SIGTRAP) {
			th.os.running = false
			if status.StopSignal() == sys.SIGTRAP {
				th.os.setbp = true
			}
			return th, nil
		}

		// TODO(dp) alert user about unexpected signals here.
		if halt && !th.os.running {
			// We are trying to stop the process, queue this signal to be delivered
			// to the thread when we resume.
			// Do not do this for threads that were running because we sent them a
			// STOP signal and we need to observe it so we don't mistakenly deliver
			// it later.
			th.os.delayedSignal = int(status.StopSignal())
			th.os.running = false
			return th, nil
		} else {
			if err := th.resumeWithSig(int(status.StopSignal())); err != nil {
				if err == sys.ESRCH {
					dbp.postExit()
					return nil, proc.ErrProcessExited{Pid: dbp.pid}
				}
				return nil, err
			}
		}
	}
}

func status(pid int, comm string) rune {
	f, err := os.Open(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return '\000'
	}
	defer f.Close()
	rd := bufio.NewReader(f)

	var (
		p     int
		state rune
	)

	// The second field of /proc/pid/stat is the name of the task in parenthesis.
	// The name of the task is the base name of the executable for this process limited to TASK_COMM_LEN characters
	// Since both parenthesis and spaces can appear inside the name of the task and no escaping happens we need to read the name of the executable first
	// See: include/linux/sched.c:315 and include/linux/sched.c:1510
	fmt.Fscanf(rd, "%d ("+comm+")  %c", &p, &state)
	return state
}

// waitFast is like wait but does not handle process-exit correctly
func (dbp *nativeProcess) waitFast(pid int) (int, *sys.WaitStatus, error) {
	var s sys.WaitStatus
	wpid, err := sys.Wait4(pid, &s, sys.WALL, nil)
	return wpid, &s, err
}

func (dbp *nativeProcess) wait(pid, options int) (int, *sys.WaitStatus, error) {
	var s sys.WaitStatus
	if (pid != dbp.pid) || (options != 0) {
		wpid, err := sys.Wait4(pid, &s, sys.WALL|options, nil)
		return wpid, &s, err
	}
	// If we call wait4/waitpid on a thread that is the leader of its group,
	// with options == 0, while ptracing and the thread leader has exited leaving
	// zombies of its own then waitpid hangs forever this is apparently intended
	// behaviour in the linux kernel because it's just so convenient.
	// Therefore we call wait4 in a loop with WNOHANG, sleeping a while between
	// calls and exiting when either wait4 succeeds or we find out that the thread
	// has become a zombie.
	// References:
	// https://sourceware.org/bugzilla/show_bug.cgi?id=12702
	// https://sourceware.org/bugzilla/show_bug.cgi?id=10095
	// https://sourceware.org/bugzilla/attachment.cgi?id=5685
	for {
		wpid, err := sys.Wait4(pid, &s, sys.WNOHANG|sys.WALL|options, nil)
		if err != nil {
			return 0, nil, err
		}
		if wpid != 0 {
			return wpid, &s, err
		}
		if status(pid, dbp.os.comm) == statusZombie {
			return pid, nil, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (dbp *nativeProcess) exitGuard(err error) error {
	if err != sys.ESRCH {
		return err
	}
	if status(dbp.pid, dbp.os.comm) == statusZombie {
		_, err := dbp.trapWaitInternal(-1, 0)
		return err
	}

	return err
}

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
	// everything is resumed
	for _, thread := range dbp.threads {
		if err := thread.resume(); err != nil && err != sys.ESRCH {
			return err
		}
	}
	return nil
}

// stop stops all running threads and sets breakpoints
func (dbp *nativeProcess) stop(trapthread *nativeThread) (err error) {
	if dbp.exited {
		return &proc.ErrProcessExited{Pid: dbp.Pid()}
	}

	for _, th := range dbp.threads {
		th.os.setbp = false
	}
	trapthread.os.setbp = true

	// check if any other thread simultaneously received a SIGTRAP
	for {
		th, _ := dbp.trapWaitInternal(-1, trapWaitNohang)
		if th == nil {
			break
		}
	}

	// stop all threads that are still running
	for _, th := range dbp.threads {
		if th.os.running {
			if err := th.stop(); err != nil {
				return dbp.exitGuard(err)
			}
		}
	}

	// wait for all threads to stop
	for {
		allstopped := true
		for _, th := range dbp.threads {
			if th.os.running {
				allstopped = false
				break
			}
		}
		if allstopped {
			break
		}
		_, err := dbp.trapWaitInternal(-1, trapWaitHalt)
		if err != nil {
			return err
		}
	}

	if err := linutil.ElfUpdateSharedObjects(dbp); err != nil {
		return err
	}

	// set breakpoints on SIGTRAP threads
	for _, th := range dbp.threads {
		if th.CurrentBreakpoint.Breakpoint == nil && th.os.setbp {
			if err := th.SetCurrentBreakpoint(true); err != nil {
				return err
			}
		}
	}
	return nil
}

func (dbp *nativeProcess) detach(kill bool) error {
	for threadID := range dbp.threads {
		err := ptraceDetach(threadID, 0)
		if err != nil {
			return err
		}
	}
	if kill {
		return nil
	}
	// For some reason the process will sometimes enter stopped state after a
	// detach, this doesn't happen immediately either.
	// We have to wait a bit here, then check if the main thread is stopped and
	// SIGCONT it if it is.
	time.Sleep(50 * time.Millisecond)
	if s := status(dbp.pid, dbp.os.comm); s == 'T' {
		sys.Kill(dbp.pid, sys.SIGCONT)
	}
	return nil
}

// EntryPoint will return the process entry point address, useful for
// debugging PIEs.
func (dbp *nativeProcess) EntryPoint() (uint64, error) {
	auxvbuf, err := ioutil.ReadFile(fmt.Sprintf("/proc/%d/auxv", dbp.pid))
	if err != nil {
		return 0, fmt.Errorf("could not read auxiliary vector: %v", err)
	}

	return linutil.EntryPointFromAuxv(auxvbuf, dbp.bi.Arch.PtrSize()), nil
}

func killProcess(pid int) error {
	return sys.Kill(pid, sys.SIGINT)
}
