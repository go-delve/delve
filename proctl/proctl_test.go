package proctl

import (
	"os/exec"
	"testing"
)

func StartTestProcess() (int, error) {
	cmd := exec.Command("../fixtures/testprog")

	err := cmd.Start()
	if err != nil {
		return 0, err
	}

	return cmd.Process.Pid, nil
}

func TestStep(t *testing.T) {
	pid, err := StartTestProcess()
	if err != nil {
		t.Fatal("Starting test process:", err)
	}

	p, err := NewDebugProcess(pid)
	if err != nil {
		t.Fatal("NewDebugProcess():", err)
	}

	regs, err := p.Registers()
	if err != nil {
		t.Fatal("Registers():", err)
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
}
