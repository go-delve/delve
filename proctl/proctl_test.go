package proctl

import (
	"bytes"
	"os/exec"
	"syscall"
	"testing"
)

type testfunc func(p *DebuggedProcess)

func dataAtAddr(pid int, addr uint64) ([]byte, error) {
	data := make([]byte, 1)
	_, err := syscall.PtracePeekData(pid, uintptr(addr), data)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func getRegisters(p *DebuggedProcess, t *testing.T) *syscall.PtraceRegs {
	regs, err := p.Registers()
	if err != nil {
		t.Fatal("Registers():", err)
	}

	return regs
}

func withTestProcess(name string, t *testing.T, fn testfunc) {
	cmd, err := StartTestProcess(name)
	if err != nil {
		t.Fatal("Starting test process:", err)
	}

	pid := cmd.Process.Pid
	p, err := NewDebugProcess(pid)
	if err != nil {
		t.Fatal("NewDebugProcess():", err)
	}
	defer cmd.Process.Kill()

	fn(p)
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
	withTestProcess("testprog", t, func(p *DebuggedProcess) {
		if !p.ProcessState.Sys().(syscall.WaitStatus).Stopped() {
			t.Errorf("Process was not stopped correctly")
		}
	})
}

func TestStep(t *testing.T) {
	withTestProcess("testprog", t, func(p *DebuggedProcess) {
		regs := getRegisters(p, t)
		rip := regs.PC()

		err := p.Step()
		if err != nil {
			t.Fatal("Step():", err)
		}

		regs = getRegisters(p, t)

		if rip >= regs.PC() {
			t.Errorf("Expected %#v to be greater than %#v", regs.PC(), rip)
		}
	})
}

func TestContinue(t *testing.T) {
	withTestProcess("continuetestprog", t, func(p *DebuggedProcess) {
		if p.ProcessState.Exited() {
			t.Fatal("Process already exited")
		}

		err := p.Continue()
		if err != nil {
			t.Fatal("Continue():", err)
		}

		if !p.ProcessState.Success() {
			t.Fatal("Process did not exit successfully")
		}
	})
}

func TestBreakPoint(t *testing.T) {
	withTestProcess("testprog", t, func(p *DebuggedProcess) {
		sleepytimefunc := p.GoSymTable.LookupFunc("main.sleepytime")
		sleepyaddr := sleepytimefunc.Entry

		bp, err := p.Break(uintptr(sleepyaddr))
		if err != nil {
			t.Fatal("Break():", err)
		}

		breakpc := bp.Addr + 1
		err = p.Continue()
		if err != nil {
			t.Fatal("Continue():", err)
		}

		regs := getRegisters(p, t)

		pc := regs.PC()
		if pc != breakpc {
			t.Fatalf("Break not respected:\nPC:%d\nFN:%d\n", pc, breakpc)
		}

		err = p.Step()
		if err != nil {
			t.Fatal(err)
		}

		regs = getRegisters(p, t)

		pc = regs.PC()
		if pc == breakpc {
			t.Fatalf("Step not respected:\nPC:%d\nFN:%d\n", pc, breakpc)
		}
	})
}

func TestBreakPointWithNonExistantFunction(t *testing.T) {
	withTestProcess("testprog", t, func(p *DebuggedProcess) {
		_, err := p.Break(uintptr(0))
		if err == nil {
			t.Fatal("Should not be able to break at non existant function")
		}
	})
}

func TestClearBreakPoint(t *testing.T) {
	withTestProcess("testprog", t, func(p *DebuggedProcess) {
		fn := p.GoSymTable.LookupFunc("main.sleepytime")
		bp, err := p.Break(uintptr(fn.Entry))
		if err != nil {
			t.Fatal("Break():", err)
		}

		int3, err := dataAtAddr(p.Pid, bp.Addr)
		if err != nil {
			t.Fatal(err)
		}

		bp, err = p.Clear(fn.Entry)
		if err != nil {
			t.Fatal("Break():", err)
		}

		data, err := dataAtAddr(p.Pid, bp.Addr)
		if err != nil {
			t.Fatal(err)
		}

		if bytes.Equal(data, int3) {
			t.Fatalf("Breakpoint was not cleared data: %#v, int3: %#v", data, int3)
		}

		if len(p.BreakPoints) != 0 {
			t.Fatal("Breakpoint not removed internally")
		}
	})
}
