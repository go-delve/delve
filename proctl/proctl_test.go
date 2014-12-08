package proctl_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/derekparker/delve/helper"
	"github.com/derekparker/delve/proctl"
)

func dataAtAddr(pid int, addr uint64) ([]byte, error) {
	data := make([]byte, 1)
	_, err := proctl.ReadMemory(pid, uintptr(addr), data)
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

func currentLineNumber(p *proctl.DebuggedProcess, t *testing.T) (string, int) {
	pc := currentPC(p, t)
	f, l, _ := p.GoSymTable.PCToLine(pc)

	return f, l
}

func TestStep(t *testing.T) {
	helper.WithTestProcess("../_fixtures/testprog", t, func(p *proctl.DebuggedProcess) {
		helloworldfunc := p.GoSymTable.LookupFunc("main.helloworld")
		helloworldaddr := helloworldfunc.Entry

		_, err := p.Break(uintptr(helloworldaddr))
		assertNoError(err, t, "Break()")
		assertNoError(p.Continue(), t, "Continue()")

		regs := helper.GetRegisters(p, t)
		rip := regs.PC()

		err = p.Step()
		assertNoError(err, t, "Step()")

		regs = helper.GetRegisters(p, t)
		if rip >= regs.PC() {
			t.Errorf("Expected %#v to be greater than %#v", regs.PC(), rip)
		}
	})
}

func TestContinue(t *testing.T) {
	helper.WithTestProcess("../_fixtures/continuetestprog", t, func(p *proctl.DebuggedProcess) {
		err := p.Continue()
		if err != nil {
			if _, ok := err.(proctl.ProcessExitedError); !ok {
				t.Fatal(err)
			}
		}

		if p.Status().ExitStatus() != 0 {
			t.Fatal("Process did not exit successfully", p.Status().ExitStatus())
		}
	})
}

func TestBreakPoint(t *testing.T) {
	helper.WithTestProcess("../_fixtures/testprog", t, func(p *proctl.DebuggedProcess) {
		sleepytimefunc := p.GoSymTable.LookupFunc("main.helloworld")
		sleepyaddr := sleepytimefunc.Entry

		bp, err := p.Break(uintptr(sleepyaddr))
		assertNoError(err, t, "Break()")

		breakpc := bp.Addr + 1
		err = p.Continue()
		assertNoError(err, t, "Continue()")

		pc, err := p.CurrentPC()
		if err != nil {
			t.Fatal(err)
		}

		if pc != breakpc {
			f, l, _ := p.GoSymTable.PCToLine(pc)
			t.Fatalf("Break not respected:\nPC:%#v %s:%d\nFN:%#v \n", pc, f, l, breakpc)
		}

		err = p.Step()
		assertNoError(err, t, "Step()")

		pc, err = p.CurrentPC()
		if err != nil {
			t.Fatal(err)
		}

		if pc == breakpc {
			t.Fatalf("Step not respected:\nPC:%d\nFN:%d\n", pc, breakpc)
		}
	})
}

func TestBreakPointInSeperateGoRoutine(t *testing.T) {
	helper.WithTestProcess("../_fixtures/testthreads", t, func(p *proctl.DebuggedProcess) {
		fn := p.GoSymTable.LookupFunc("main.anotherthread")
		if fn == nil {
			t.Fatal("No fn exists")
		}

		_, err := p.Break(uintptr(fn.Entry))
		if err != nil {
			t.Fatal(err)
		}

		err = p.Continue()
		if err != nil {
			t.Fatal(err)
		}

		pc, err := p.CurrentPC()
		if err != nil {
			t.Fatal(err)
		}

		f, l, _ := p.GoSymTable.PCToLine(pc)
		if f != "testthreads.go" && l != 8 {
			t.Fatal("Program did not hit breakpoint")
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
		assertNoError(err, t, "Break()")

		int3, err := dataAtAddr(p.Pid, bp.Addr)
		if err != nil {
			t.Fatal(err)
		}

		bp, err = p.Clear(fn.Entry)
		assertNoError(err, t, "Clear()")

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
		err            error
		executablePath = "../_fixtures/testnextprog"
	)

	testcases := []struct {
		begin, end int
	}{
		{19, 20},
		{20, 23},
		{23, 24},
		{24, 26},
		{26, 31},
		{31, 23},
		{23, 24},
		{24, 26},
		{26, 31},
		{31, 23},
		{23, 24},
		{24, 26},
		{26, 27},
		{27, 34},
		{34, 35},
		{35, 41},
		{41, 40},
		{40, 41},
	}

	fp, err := filepath.Abs("../_fixtures/testnextprog.go")
	if err != nil {
		t.Fatal(err)
	}

	helper.WithTestProcess(executablePath, t, func(p *proctl.DebuggedProcess) {
		pc, _, _ := p.GoSymTable.LineToPC(fp, testcases[0].begin)
		_, err := p.Break(uintptr(pc))
		assertNoError(err, t, "Break()")
		assertNoError(p.Continue(), t, "Continue()")

		for _, tc := range testcases {
			f, ln := currentLineNumber(p, t)
			if ln != tc.begin {
				t.Fatalf("Program not stopped at correct spot expected %d was %s:%d", tc.begin, f, ln)
			}

			assertNoError(p.Next(), t, "Next() returned an error")

			f, ln = currentLineNumber(p, t)
			if ln != tc.end {
				t.Fatalf("Program did not continue to correct next location expected %d was %s:%d", tc.end, f, ln)
			}
		}

		if len(p.BreakPoints) != 1 {
			t.Fatal("Not all breakpoints were cleaned up", len(p.BreakPoints))
		}
	})
}
