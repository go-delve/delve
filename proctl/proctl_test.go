package proctl

import (
	"os/exec"
	"syscall"
	"testing"
)

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

	_, err = p.Break("main.sleepytime")
	if err != nil {
		t.Fatal("Break():", err)
	}

	sleepytimefunc := p.GoSymTable.LookupFunc("main.sleepytime")
	sleepyaddr := sleepytimefunc.LineTable.PC

	err = p.Continue()
	if err != nil {
		t.Fatal("Continue():", err)
	}

	regs, err := p.Registers()
	if err != nil {
		t.Fatal("Registers():", err)
	}

	pc := regs.PC()
	if pc != sleepyaddr {
		t.Fatal("Break not respected:\nPC:%d\nFN:%d\n", pc, sleepyaddr)
	}

	cmd.Process.Kill()
}

func TestBreakPointIsSetOnlyOnce(t *testing.T) {
	cmd, err := StartTestProcess("testprog")
	if err != nil {
		t.Fatal("Starting test process:", err)
	}

	pid := cmd.Process.Pid
	p, err := NewDebugProcess(pid)
	if err != nil {
		t.Fatal("NewDebugProcess():", err)
	}

	_, err = p.Break("main.sleepytime")
	if err != nil {
		t.Fatal("Break():", err)
	}

	_, err = p.Break("main.sleepytime")
	if err == nil {
		t.Fatal("Should not be able to add breakpoint twice")
	}
}
