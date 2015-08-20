package proc

import (
	"debug/elf"
	"debug/gosym"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/dwarf/frame"
	"github.com/derekparker/delve/dwarf/line"
)

// Process statuses
const (
	STATUS_SLEEPING   = 'S'
	STATUS_RUNNING    = 'R'
	STATUS_TRACE_STOP = 't'
	STATUS_ZOMBIE     = 'Z'
)

// Not actually needed for Linux.
type OSProcessDetails interface{}

// Create and begin debugging a new process. First entry in
// `cmd` is the program to run, and then rest are the arguments
// to be supplied to that process.
func Launch(cmd []string) (*Process, error) {
	var (
		proc *exec.Cmd
		err  error
	)
	dbp := New(0)
	dbp.execPtraceFunc(func() {
		proc = exec.Command(cmd[0])
		proc.Args = cmd
		proc.Stdout = os.Stdout
		proc.Stderr = os.Stderr
		proc.SysProcAttr = &syscall.SysProcAttr{Ptrace: true, Setpgid: true}
		err = proc.Start()
	})
	if err != nil {
		return nil, err
	}
	dbp.Pid = proc.Process.Pid
	_, _, err = wait(proc.Process.Pid, proc.Process.Pid, 0)
	if err != nil {
		return nil, fmt.Errorf("waiting for target execve failed: %s", err)
	}
	return initializeDebugProcess(dbp, proc.Path, false)
}

// Attach to an existing process with the given PID.
func Attach(pid int) (*Process, error) {
	return initializeDebugProcess(New(pid), "", true)
}

func (dbp *Process) Kill() (err error) {
	if dbp.exited {
		return nil
	}
	if !dbp.Threads[dbp.Pid].Stopped() {
		return errors.New("process must be stopped in order to kill it")
	}
	if err = sys.Kill(-dbp.Pid, sys.SIGKILL); err != nil {
		return errors.New("could not deliver signal " + err.Error())
	}
	if _, _, err = wait(dbp.Pid, dbp.Pid, 0); err != nil {
		return
	}
	dbp.exited = true
	return
}

func (dbp *Process) requestManualStop() (err error) {
	return sys.Kill(dbp.Pid, sys.SIGTRAP)
}

// Attach to a newly created thread, and store that thread in our list of
// known threads.
func (dbp *Process) addThread(tid int, attach bool) (*Thread, error) {
	if thread, ok := dbp.Threads[tid]; ok {
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
		pid, status, err := wait(tid, dbp.Pid, 0)
		if err != nil {
			return nil, err
		}
		if status.Exited() {
			return nil, fmt.Errorf("thread already exited %d", pid)
		}
	}

	dbp.execPtraceFunc(func() { err = syscall.PtraceSetOptions(tid, syscall.PTRACE_O_TRACECLONE) })
	if err == syscall.ESRCH {
		if _, _, err = wait(tid, dbp.Pid, 0); err != nil {
			return nil, fmt.Errorf("error while waiting after adding thread: %d %s", tid, err)
		}
		dbp.execPtraceFunc(func() { err = syscall.PtraceSetOptions(tid, syscall.PTRACE_O_TRACECLONE) })
		if err != nil {
			return nil, fmt.Errorf("could not set options for new traced thread %d %s", tid, err)
		}
	}

	dbp.Threads[tid] = &Thread{
		Id:  tid,
		dbp: dbp,
		os:  new(OSSpecificDetails),
	}
	if dbp.CurrentThread == nil {
		dbp.CurrentThread = dbp.Threads[tid]
	}
	return dbp.Threads[tid], nil
}

func (dbp *Process) updateThreadList() error {
	tids, _ := filepath.Glob(fmt.Sprintf("/proc/%d/task/*", dbp.Pid))
	for _, tidpath := range tids {
		tidstr := filepath.Base(tidpath)
		tid, err := strconv.Atoi(tidstr)
		if err != nil {
			return err
		}
		if _, err := dbp.addThread(tid, tid != dbp.Pid); err != nil {
			return err
		}
	}
	return nil
}

func (dbp *Process) findExecutable(path string) (*elf.File, error) {
	if path == "" {
		path = fmt.Sprintf("/proc/%d/exe", dbp.Pid)
	}
	f, err := os.OpenFile(path, 0, os.ModePerm)
	if err != nil {
		return nil, err
	}
	elfFile, err := elf.NewFile(f)
	if err != nil {
		return nil, err
	}
	data, err := elfFile.DWARF()
	if err != nil {
		return nil, err
	}
	dbp.dwarf = data
	return elfFile, nil
}

func (dbp *Process) parseDebugFrame(exe *elf.File, wg *sync.WaitGroup) {
	defer wg.Done()

	if sec := exe.Section(".debug_frame"); sec != nil {
		debugFrame, err := exe.Section(".debug_frame").Data()
		if err != nil {
			fmt.Println("could not get .debug_frame section", err)
			os.Exit(1)
		}
		dbp.frameEntries = frame.Parse(debugFrame)
	} else {
		fmt.Println("could not find .debug_frame section in binary")
		os.Exit(1)
	}
}

func (dbp *Process) obtainGoSymbols(exe *elf.File, wg *sync.WaitGroup) {
	defer wg.Done()

	var (
		symdat  []byte
		pclndat []byte
		err     error
	)

	if sec := exe.Section(".gosymtab"); sec != nil {
		symdat, err = sec.Data()
		if err != nil {
			fmt.Println("could not get .gosymtab section", err)
			os.Exit(1)
		}
	}

	if sec := exe.Section(".gopclntab"); sec != nil {
		pclndat, err = sec.Data()
		if err != nil {
			fmt.Println("could not get .gopclntab section", err)
			os.Exit(1)
		}
	}

	pcln := gosym.NewLineTable(pclndat, exe.Section(".text").Addr)
	tab, err := gosym.NewTable(symdat, pcln)
	if err != nil {
		fmt.Println("could not get initialize line table", err)
		os.Exit(1)
	}

	dbp.goSymTable = tab
}

func (dbp *Process) parseDebugLineInfo(exe *elf.File, wg *sync.WaitGroup) {
	defer wg.Done()

	if sec := exe.Section(".debug_line"); sec != nil {
		debugLine, err := exe.Section(".debug_line").Data()
		if err != nil {
			fmt.Println("could not get .debug_line section", err)
			os.Exit(1)
		}
		dbp.lineInfo = line.Parse(debugLine)
	} else {
		fmt.Println("could not find .debug_line section in binary")
		os.Exit(1)
	}
}

func (dbp *Process) trapWait(pid int) (*Thread, error) {
	for {
		wpid, status, err := wait(pid, dbp.Pid, 0)
		if err != nil {
			return nil, fmt.Errorf("wait err %s %d", err, pid)
		}
		if wpid == 0 {
			continue
		}
		th, ok := dbp.Threads[wpid]
		if ok {
			th.Status = status
		}
		if status.Exited() {
			if wpid == dbp.Pid {
				dbp.exited = true
				return nil, ProcessExitedError{Pid: wpid, Status: status.ExitStatus()}
			}
			delete(dbp.Threads, wpid)
			continue
		}
		if status.StopSignal() == sys.SIGTRAP && status.TrapCause() == sys.PTRACE_EVENT_CLONE {
			// A traced thread has cloned a new thread, grab the pid and
			// add it to our list of traced threads.
			var cloned uint
			dbp.execPtraceFunc(func() { cloned, err = sys.PtraceGetEventMsg(wpid) })
			if err != nil {
				return nil, fmt.Errorf("could not get event message: %s", err)
			}
			th, err = dbp.addThread(int(cloned), false)
			if err != nil {
				return nil, err
			}
			// Set all hardware breakpoints on the new thread.
			for _, bp := range dbp.Breakpoints {
				if !bp.hardware {
					continue
				}
				if err = dbp.setHardwareBreakpoint(bp.reg, th.Id, bp.Addr); err != nil {
					return nil, err
				}
			}
			if err = th.Continue(); err != nil {
				return nil, fmt.Errorf("could not continue new thread %d %s", cloned, err)
			}
			if err = dbp.Threads[int(wpid)].Continue(); err != nil {
				return nil, fmt.Errorf("could not continue existing thread %d %s", cloned, err)
			}
			continue
		}
		if th == nil {
			// Sometimes we get an unknown thread, ignore it?
			continue
		}
		if status.StopSignal() == sys.SIGTRAP && dbp.halt {
			th.running = false
			dbp.halt = false
			return th, nil
		}
		if status.StopSignal() == sys.SIGTRAP {
			th.running = false
			return dbp.handleBreakpointOnThread(wpid)
		}
		if th != nil {
			// TODO(dp) alert user about unexpected signals here.
			if err := th.Continue(); err != nil {
				return nil, err
			}
		}
	}
}

func status(pid int) rune {
	f, err := os.Open(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return '\000'
	}
	defer f.Close()

	var (
		p     int
		comm  string
		state rune
	)
	fmt.Fscanf(f, "%d %s %c", &p, &comm, &state)
	return state
}

func wait(pid, tgid, options int) (int, *sys.WaitStatus, error) {
	var s sys.WaitStatus
	if (pid != tgid) || (options != 0) {
		wpid, err := sys.Wait4(pid, &s, sys.WALL|options, nil)
		return wpid, &s, err
	} else {
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
			if status(pid) == STATUS_ZOMBIE {
				return pid, nil, nil
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}
