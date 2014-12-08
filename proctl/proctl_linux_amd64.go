package proctl

import (
	"debug/gosym"
	"fmt"
	"os"
	"sync"
	"syscall"

	"github.com/derekparker/delve/dwarf/frame"
	"github.com/derekparker/delve/vendor/elf"
)

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

	debugFrame, err := exe.Section(".debug_frame").Data()
	if err != nil {
		fmt.Println("could not get .debug_frame section", err)
		os.Exit(1)
	}

	dbp.FrameEntries = frame.Parse(debugFrame)
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
