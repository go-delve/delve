package proctl

import (
	"debug/gosym"
	"fmt"
	"os"
	"syscall"

	"github.com/derekparker/delve/dwarf/frame"
	"github.com/derekparker/delve/vendor/elf"
)

// Struct representing a debugged process. Holds onto pid, register values,
// process struct and process state.
type DebuggedProcess struct {
	Pid           int
	Process       *os.Process
	Executable    *elf.File
	GoSymTable    *gosym.Table
	FrameEntries  *frame.FrameDescriptionEntries
	BreakPoints   map[uint64]*BreakPoint
	Threads       map[int]*ThreadContext
	CurrentThread *ThreadContext
}

func (dbp *DebuggedProcess) addThread(tid int) (*ThreadContext, error) {
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
		Regs:    new(syscall.PtraceRegs),
	}

	return dbp.Threads[tid], nil
}

func parseProcessStatus(pid int) (*ProcessStatus, error) {
	var ps ProcessStatus

	f, err := os.Open(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fmt.Fscanf(f, "%d %s %c %d", &ps.pid, &ps.comm, &ps.state, &ps.ppid)

	return &ps, nil
}

func (dbp *DebuggedProcess) findExecutable() error {
	procpath := fmt.Sprintf("/proc/%d/exe", dbp.Pid)

	f, err := os.OpenFile(procpath, 0, os.ModePerm)
	if err != nil {
		return err
	}

	elffile, err := elf.NewFile(f)
	if err != nil {
		return err
	}

	dbp.Executable = elffile

	return nil
}
