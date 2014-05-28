package proctl

import (
	"bytes"
	"os/exec"
	"syscall"
	"testing"
)

func dataAtAddr(pid int, addr uint64) ([]byte, error) {
	data := make([]byte, 1)
	_, err := syscall.PtracePeekData(pid, uintptr(addr), data)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func StartTestProcess(name string) (*exec.Cmd, error) {
	cmd := exec.Command("../fixtures/" + name)

	err := cmd.Start()
	if err != nil {
		return nil, err
	}

	return cmd, nil
}

func TestAttachProcess(t *testing.T) {
	cmd, err := StartTestProcess("testprog")
	if err != nil {
		t.Fatal("Starting test process:", err)
	}

	pid := cmd.Process.Pid
	p, err := NewDebugProcess(pid)
	if err != nil {
		t.Fatal("NewDebugProcess():", err)
	}

	if !p.ProcessState.Sys().(syscall.WaitStatus).Stopped() {
		t.Errorf("Process was not stopped correctly")
	}

	cmd.Process.Kill()
}

func TestStep(t *testing.T) {
	cmd, err := StartTestProcess("testprog")
	if err != nil {
		t.Fatal("Starting test process:", err)
	}

	pid := cmd.Process.Pid
	p, err := NewDebugProcess(pid)
	if err != nil {
		t.Fatal("NewDebugProcess():", err)
	}

	regs, err := p.Registers()
	if err != nil {
		t.Fatal("Registers():", err, pid)
	}

	rip := regs.PC()

	err = p.Step()
	if err != nil {
		t.Fatal("Step():", err)
	}

	regs, err = p.Registers()
	if err != nil {
		t.Fatal("Registers():", err)
	}

	if rip >= regs.PC() {
		t.Errorf("Expected %#v to be greater than %#v", regs.PC(), rip)
	}

	cmd.Process.Kill()
}

func TestContinue(t *testing.T) {
	cmd, err := StartTestProcess("continuetestprog")
	if err != nil {
		t.Fatal("Starting test process:", err)
	}

	pid := cmd.Process.Pid
	p, err := NewDebugProcess(pid)
	if err != nil {
		t.Fatal("NewDebugProcess():", err)
	}

	if p.ProcessState.Exited() {
		t.Fatal("Process already exited")
	}

	err = p.Continue()
	if err != nil {
		t.Fatal("Continue():", err)
	}

	if !p.ProcessState.Success() {
		t.Fatal("Process did not exit successfully")
	}
}

func TestBreakPoint(t *testing.T) {
	cmd, err := StartTestProcess("testprog")
	if err != nil {
		t.Fatal("Starting test process:", err)
	}

	pid := cmd.Process.Pid
	p, err := NewDebugProcess(pid)
	if err != nil {
		t.Fatal("NewDebugProcess():", err)
	}

	sleepytimefunc := p.GoSymTable.LookupFunc("main.sleepytime")
	sleepyaddr := sleepytimefunc.Entry

	_, err = p.Break(uintptr(sleepyaddr))
	if err != nil {
		t.Fatal("Break():", err)
	}

	err = p.Continue()
	if err != nil {
		t.Fatal("Continue():", err)
	}

	regs, err := p.Registers()
	if err != nil {
		t.Fatal("Registers():", err)
	}

	pc := regs.PC()
	if pc != sleepyaddr+1 {
		t.Fatalf("Break not respected:\nPC:%d\nFN:%d\n", pc, sleepyaddr)
	}

	err = p.Step()
	if err != nil {
		t.Fatal(err)
	}

	regs, err = p.Registers()
	if err != nil {
		t.Fatal("Registers():", err)
	}

	pc = regs.PC()
	if pc == sleepyaddr {
		t.Fatalf("Step not respected:\nPC:%d\nFN:%d\n", pc, sleepyaddr)
	}

	cmd.Process.Kill()
}

func TestBreakPointWithNonExistantFunction(t *testing.T) {
	cmd, err := StartTestProcess("testprog")
	if err != nil {
		t.Fatal("Starting test process:", err)
	}

	pid := cmd.Process.Pid
	p, err := NewDebugProcess(pid)
	if err != nil {
		t.Fatal("NewDebugProcess():", err)
	}

	_, err = p.Break(uintptr(0))
	if err == nil {
		t.Fatal("Should not be able to break at non existant function")
	}
}

func TestClearBreakPoint(t *testing.T) {
	cmd, err := StartTestProcess("testprog")
	if err != nil {
		t.Fatal("Starting test process:", err)
	}

	pid := cmd.Process.Pid
	p, err := NewDebugProcess(pid)
	if err != nil {
		t.Fatal("NewDebugProcess():", err)
	}

	fn := p.GoSymTable.LookupFunc("main.sleepytime")
	bp, err := p.Break(uintptr(fn.Entry))
	if err != nil {
		t.Fatal("Break():", err)
	}

	int3, err := dataAtAddr(pid, bp.Addr)
	if err != nil {
		t.Fatal(err)
	}

	bp, err = p.Clear(fn.Entry)
	if err != nil {
		t.Fatal("Break():", err)
	}

	data, err := dataAtAddr(pid, bp.Addr)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Equal(data, int3) {
		t.Fatalf("Breakpoint was not cleared data: %#v, int3: %#v", data, int3)
	}
}
