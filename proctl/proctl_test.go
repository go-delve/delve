package proctl_test

import (
	"bytes"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/derekparker/dbg/_helper"
	"github.com/derekparker/dbg/proctl"
)

func dataAtAddr(pid int, addr uint64) ([]byte, error) {
	data := make([]byte, 1)
	_, err := syscall.PtracePeekData(pid, uintptr(addr), data)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func assertNoError(err error, t *testing.T, s string) {
	if err != nil {
		t.Fatal(s, ":", err)
	}
}

func currentPC(p *proctl.DebuggedProcess, t *testing.T) uint64 {
	pc, err := p.CurrentPC()
	if err != nil {
		t.Fatal(err)
	}

	return pc
}

func currentLineNumber(p *proctl.DebuggedProcess, t *testing.T) int {
	pc := currentPC(p, t)
	_, l, _ := p.GoSymTable.PCToLine(pc)

	return l
}

func TestAttachProcess(t *testing.T) {
	helper.WithTestProcess("../_fixtures/testprog", t, func(p *proctl.DebuggedProcess) {
		if !p.ProcessState.Sys().(syscall.WaitStatus).Stopped() {
			t.Errorf("Process was not stopped correctly")
		}
	})
}

func TestStep(t *testing.T) {
	helper.WithTestProcess("../_fixtures/testprog", t, func(p *proctl.DebuggedProcess) {
		if p.ProcessState.Exited() {
			t.Fatal("Process already exited")
		}

		regs := helper.GetRegisters(p, t)
		rip := regs.PC()

		err := p.Step()
		if err != nil {
			t.Fatal("Step():", err)
		}

		regs = helper.GetRegisters(p, t)

		if rip >= regs.PC() {
			t.Errorf("Expected %#v to be greater than %#v", regs.PC(), rip)
		}
	})
}

func TestContinue(t *testing.T) {
	helper.WithTestProcess("../_fixtures/continuetestprog", t, func(p *proctl.DebuggedProcess) {
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
	helper.WithTestProcess("../_fixtures/testprog", t, func(p *proctl.DebuggedProcess) {
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

		regs := helper.GetRegisters(p, t)

		pc := regs.PC()
		if pc != breakpc {
			t.Fatalf("Break not respected:\nPC:%d\nFN:%d\n", pc, breakpc)
		}

		err = p.Step()
		if err != nil {
			t.Fatal(err)
		}

		regs = helper.GetRegisters(p, t)

		pc = regs.PC()
		if pc == breakpc {
			t.Fatalf("Step not respected:\nPC:%d\nFN:%d\n", pc, breakpc)
		}
	})
}

func TestBreakPointWithNonExistantFunction(t *testing.T) {
	helper.WithTestProcess("../_fixtures/testprog", t, func(p *proctl.DebuggedProcess) {
		_, err := p.Break(uintptr(0))
		if err == nil {
			t.Fatal("Should not be able to break at non existant function")
		}
	})
}

func TestClearBreakPoint(t *testing.T) {
	helper.WithTestProcess("../_fixtures/testprog", t, func(p *proctl.DebuggedProcess) {
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

func TestNext(t *testing.T) {
	var (
		ln  int
		err error
	)

	testcases := []struct {
		begin, end int
	}{
		{20, 22},
		{22, 23},
		{23, 25},
		{25, 20},
	}

	fp, err := filepath.Abs("../_fixtures/testnextprog.go")
	if err != nil {
		t.Fatal(err)
	}

	helper.WithTestProcess("../_fixtures/testnextprog", t, func(p *proctl.DebuggedProcess) {
		pc, _, _ := p.GoSymTable.LineToPC(fp, testcases[0].begin)
		_, err := p.Break(uintptr(pc))
		assertNoError(err, t, "Break() returned an error")
		assertNoError(p.Continue(), t, "Continue() returned an error")

		for _, tc := range testcases {
			ln = currentLineNumber(p, t)
			if ln != tc.begin {
				t.Fatalf("Program not stopped at correct spot expected %d was %d", tc.begin, ln)
			}

			assertNoError(p.Next(), t, "Next() returned an error")

			ln = currentLineNumber(p, t)
			if ln != tc.end {
				t.Fatalf("Program did not continue to correct next location expected %d was %d", tc.end, ln)
			}
		}
	})
}
