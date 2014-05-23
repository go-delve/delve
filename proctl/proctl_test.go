package proctl

import (
	"os/exec"
	"syscall"
	"testing"
)

func StartTestProcess() (*exec.Cmd, error) {
	cmd := exec.Command("../fixtures/testprog")

	err := cmd.Start()
	if err != nil {
		return nil, err
	}

	return cmd, nil
}

func TestAttachProcess(t *testing.T) {
	cmd, err := StartTestProcess()
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
}

func TestStep(t *testing.T) {
	cmd, err := StartTestProcess()
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
	cmd, err := StartTestProcess()
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
