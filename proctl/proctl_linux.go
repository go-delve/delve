package proctl

import (
	"debug/elf"
	"debug/gosym"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/dwarf/frame"
)

const (
	STATUS_SLEEPING   = 'S'
	STATUS_RUNNING    = 'R'
	STATUS_TRACE_STOP = 't'
)

// Not actually needed for Linux.
type OSProcessDetails interface{}

func (dbp *DebuggedProcess) Halt() (err error) {
	for _, th := range dbp.Threads {
		err := th.Halt()
		if err != nil {
			return err
		}
	}
	return nil
}

// Finds the executable from /proc/<pid>/exe and then
// uses that to parse the following information:
// * Dwarf .debug_frame section
// * Dwarf .debug_line section
// * Go symbol table.
func (dbp *DebuggedProcess) LoadInformation() error {
	var (
		wg  sync.WaitGroup
		exe *elf.File
		err error
	)

	exe, err = dbp.findExecutable()
	if err != nil {
		return err
	}

	wg.Add(2)
	go dbp.parseDebugFrame(exe, &wg)
	go dbp.obtainGoSymbols(exe, &wg)
	wg.Wait()

	return nil
}

// Attach to a newly created thread, and store that thread in our list of
// known threads.
func (dbp *DebuggedProcess) addThread(tid int, attach bool) (*ThreadContext, error) {
	if thread, ok := dbp.Threads[tid]; ok {
		return thread, nil
	}

	if attach {
		err := sys.PtraceAttach(tid)
		if err != nil && err != sys.EPERM {
			// Do not return err if err == EPERM,
			// we may already be tracing this thread due to
			// PTRACE_O_TRACECLONE. We will surely blow up later
			// if we truly don't have permissions.
			return nil, fmt.Errorf("could not attach to new thread %d %s", tid, err)
		}

		pid, status, err := wait(tid, 0)
		if err != nil {
			return nil, err
		}

		if status.Exited() {
			return nil, fmt.Errorf("thread already exited %d", pid)
		}
	}

	err := syscall.PtraceSetOptions(tid, syscall.PTRACE_O_TRACECLONE)
	if err == syscall.ESRCH {
		_, _, err = wait(tid, 0)
		if err != nil {
			return nil, fmt.Errorf("error while waiting after adding thread: %d %s", tid, err)
		}

		err := syscall.PtraceSetOptions(tid, syscall.PTRACE_O_TRACECLONE)
		if err != nil {
			return nil, fmt.Errorf("could not set options for new traced thread %d %s", tid, err)
		}
	}

	dbp.Threads[tid] = &ThreadContext{
		Id:      tid,
		Process: dbp,
	}

	return dbp.Threads[tid], nil
}

func (dbp *DebuggedProcess) updateThreadList() error {
	allm, err := dbp.CurrentThread.AllM()
	if err != nil {
		return err
	}
	// TODO(dp) user /proc/<pid>/task to remove reliance on allm
	for _, m := range allm {
		if m.procid == 0 {
			continue
		}
		if _, err := dbp.addThread(m.procid, false); err != nil {
			return err
		}
	}
	return nil
}

func (dbp *DebuggedProcess) findExecutable() (*elf.File, error) {
	procpath := fmt.Sprintf("/proc/%d/exe", dbp.Pid)

	f, err := os.OpenFile(procpath, 0, os.ModePerm)
	if err != nil {
		return nil, err
	}

	elffile, err := elf.NewFile(f)
	if err != nil {
		return nil, err
	}

	data, err := elffile.DWARF()
	if err != nil {
		return nil, err
	}
	dbp.Dwarf = data

	return elffile, nil
}

func (dbp *DebuggedProcess) parseDebugFrame(exe *elf.File, wg *sync.WaitGroup) {
	defer wg.Done()

	if sec := exe.Section(".debug_frame"); sec != nil {
		debugFrame, err := exe.Section(".debug_frame").Data()
		if err != nil {
			fmt.Println("could not get .debug_frame section", err)
			os.Exit(1)
		}
		dbp.FrameEntries = frame.Parse(debugFrame)
	} else {
		fmt.Println("could not find .debug_frame section in binary")
		os.Exit(1)
	}
}

func (dbp *DebuggedProcess) obtainGoSymbols(exe *elf.File, wg *sync.WaitGroup) {
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

	dbp.GoSymTable = tab
}

// TODO(dp) seems like it could be unneccessary
func addNewThread(dbp *DebuggedProcess, cloner, cloned int) error {
	fmt.Println("new thread spawned", cloned)

	th, err := dbp.addThread(cloned, false)
	if err != nil {
		return err
	}

	err = th.Continue()
	if err != nil {
		return fmt.Errorf("could not continue new thread %d %s", cloned, err)
	}

	// Here we loop for a while to ensure that the once we continue
	// the newly created thread, we allow enough time for the runtime
	// to assign m->procid. This is important because we rely on
	// looping through runtime.allm in other parts of the code, so
	// we require that this is set before we do anything else.
	// TODO(dp): we might be able to eliminate this loop by telling
	// the CPU to emit a breakpoint exception on write to this location
	// in memory. That way we prevent having to loop, and can be
	// notified as soon as m->procid is set.
	// TODO(dp) get rid of this hack
	th = dbp.Threads[cloner]
	for {
		allm, _ := th.AllM()
		for _, m := range allm {
			if m.procid == cloned {
				// Continue the thread that cloned
				return th.Continue()
			}
		}
		time.Sleep(time.Millisecond)
	}
}

func stopped(pid int) bool {
	f, err := os.Open(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return false
	}
	defer f.Close()

	var (
		p     int
		comm  string
		state rune
	)
	fmt.Fscanf(f, "%d %s %c", &p, &comm, &state)
	if state == STATUS_TRACE_STOP {
		return true
	}
	return false
}

func trapWait(dbp *DebuggedProcess, pid int) (int, *sys.WaitStatus, error) {
	for {
		wpid, status, err := wait(pid, 0)
		if err != nil {
			return -1, nil, fmt.Errorf("wait err %s %d", err, pid)
		}
		if wpid == 0 {
			continue
		}
		if th, ok := dbp.Threads[wpid]; ok {
			th.Status = status
		}
		if status.Exited() && wpid == dbp.Pid {
			return -1, status, ProcessExitedError{wpid}
		}
		if status.StopSignal() == sys.SIGTRAP && status.TrapCause() == sys.PTRACE_EVENT_CLONE {
			// A traced thread has cloned a new thread, grab the pid and
			// add it to our list of traced threads.
			tid, err := sys.PtraceGetEventMsg(wpid)
			if err != nil {
				return -1, nil, fmt.Errorf("could not get event message: %s", err)
			}
			err = addNewThread(dbp, wpid, int(tid))
			if err != nil {
				return -1, nil, err
			}
			continue
		}
		if status.StopSignal() == sys.SIGTRAP {
			return wpid, status, nil
		}
		if status.StopSignal() == sys.SIGSTOP && dbp.halt {
			return -1, nil, ManualStopError{}
		}
	}
}

func wait(pid, options int) (int, *sys.WaitStatus, error) {
	var status sys.WaitStatus
	wpid, err := sys.Wait4(pid, &status, sys.WALL|options, nil)
	return wpid, &status, err
}
