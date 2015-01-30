package proctl

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func withTestProcess(name string, t *testing.T, fn func(p *DebuggedProcess)) {
	runtime.LockOSThread()
	base := filepath.Base(name)
	if err := exec.Command("go", "build", "-gcflags=-N -l", "-o", base, name+".go").Run(); err != nil {
		t.Fatalf("Could not compile %s due to %s", name, err)
	}
	defer os.Remove("./" + base)

	p, err := Launch([]string{"./" + base})
	if err != nil {
		t.Fatal("Launch():", err)
	}

	defer p.Process.Kill()

	fn(p)
}

func getRegisters(p *DebuggedProcess, t *testing.T) Registers {
	regs, err := p.Registers()
	if err != nil {
		t.Fatal("Registers():", err)
	}

	return regs
}

func dataAtAddr(pid int, addr uint64) ([]byte, error) {
	data := make([]byte, 1)
	_, err := readMemory(pid, uintptr(addr), data)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func assertNoError(err error, t *testing.T, s string) {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		fname := filepath.Base(file)
		t.Fatalf("failed assertion at %s:%d: %s : %s\n", fname, line, s, err)
	}
}

func currentPC(p *DebuggedProcess, t *testing.T) uint64 {
	pc, err := p.CurrentPC()
	if err != nil {
		t.Fatal(err)
	}

	return pc
}

func currentLineNumber(p *DebuggedProcess, t *testing.T) (string, int) {
	pc := currentPC(p, t)
	f, l, _ := p.GoSymTable.PCToLine(pc)

	return f, l
}

func TestStep(t *testing.T) {
	withTestProcess("../_fixtures/testprog", t, func(p *DebuggedProcess) {
		helloworldfunc := p.GoSymTable.LookupFunc("main.helloworld")
		helloworldaddr := helloworldfunc.Entry

		_, err := p.Break(helloworldaddr)
		assertNoError(err, t, "Break()")
		assertNoError(p.Continue(), t, "Continue()")

		regs := getRegisters(p, t)
		rip := regs.PC()

		err = p.Step()
		assertNoError(err, t, "Step()")

		regs = getRegisters(p, t)
		if rip >= regs.PC() {
			t.Errorf("Expected %#v to be greater than %#v", regs.PC(), rip)
		}
	})
}

func TestContinue(t *testing.T) {
	withTestProcess("../_fixtures/continuetestprog", t, func(p *DebuggedProcess) {
		err := p.Continue()
		if err != nil {
			if _, ok := err.(ProcessExitedError); !ok {
				t.Fatal(err)
			}
		}

		if p.Status().ExitStatus() != 0 {
			t.Fatal("Process did not exit successfully", p.Status().ExitStatus())
		}
	})
}

func TestBreakPoint(t *testing.T) {
	withTestProcess("../_fixtures/testprog", t, func(p *DebuggedProcess) {
		sleepytimefunc := p.GoSymTable.LookupFunc("main.helloworld")
		sleepyaddr := sleepytimefunc.Entry

		bp, err := p.Break(sleepyaddr)
		assertNoError(err, t, "Break()")

		breakpc := bp.Addr
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
	withTestProcess("../_fixtures/testthreads", t, func(p *DebuggedProcess) {
		fn := p.GoSymTable.LookupFunc("main.anotherthread")
		if fn == nil {
			t.Fatal("No fn exists")
		}

		_, err := p.Break(fn.Entry)
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
	withTestProcess("../_fixtures/testprog", t, func(p *DebuggedProcess) {
		_, err := p.Break(0)
		if err == nil {
			t.Fatal("Should not be able to break at non existant function")
		}
	})
}

func TestClearBreakPoint(t *testing.T) {
	withTestProcess("../_fixtures/testprog", t, func(p *DebuggedProcess) {
		fn := p.GoSymTable.LookupFunc("main.sleepytime")
		bp, err := p.Break(fn.Entry)
		assertNoError(err, t, "Break()")

		bp, err = p.Clear(fn.Entry)
		assertNoError(err, t, "Clear()")

		data, err := dataAtAddr(p.Pid, bp.Addr)
		if err != nil {
			t.Fatal(err)
		}

		int3 := []byte{0xcc}
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

	withTestProcess(executablePath, t, func(p *DebuggedProcess) {
		pc, _, _ := p.GoSymTable.LineToPC(fp, testcases[0].begin)
		_, err := p.Break(pc)
		assertNoError(err, t, "Break()")
		assertNoError(p.Continue(), t, "Continue()")

		f, ln := currentLineNumber(p, t)
		for _, tc := range testcases {
			if ln != tc.begin {
				t.Fatalf("Program not stopped at correct spot expected %d was %s:%d", tc.begin, f, ln)
			}

			assertNoError(p.Next(), t, "Next() returned an error")

			f, ln = currentLineNumber(p, t)
			if ln != tc.end {
				t.Fatalf("Program did not continue to correct next location expected %d was %s:%d", tc.end, f, ln)
			}
		}

		p.Clear(pc)
		if len(p.BreakPoints) != 0 {
			t.Fatal("Not all breakpoints were cleaned up", len(p.HWBreakPoints))
		}
		for _, bp := range p.HWBreakPoints {
			if bp != nil {
				t.Fatal("Not all breakpoints were cleaned up", bp.Addr)
			}
		}
	})
}

func TestFindReturnAddress(t *testing.T) {
	var testfile, _ = filepath.Abs("../_fixtures/testnextprog")

	withTestProcess(testfile, t, func(p *DebuggedProcess) {
		var (
			fdes = p.FrameEntries
			gsd  = p.GoSymTable
		)

		testsourcefile := testfile + ".go"
		start, _, err := gsd.LineToPC(testsourcefile, 24)
		if err != nil {
			t.Fatal(err)
		}

		_, err = p.Break(start)
		if err != nil {
			t.Fatal(err)
		}

		err = p.Continue()
		if err != nil {
			t.Fatal(err)
		}

		regs, err := p.Registers()
		if err != nil {
			t.Fatal(err)
		}

		fde, err := fdes.FDEForPC(start)
		if err != nil {
			t.Fatal(err)
		}

		ret := fde.ReturnAddressOffset(start)
		if err != nil {
			t.Fatal(err)
		}

		addr := uint64(int64(regs.SP()) + ret)
		data := make([]byte, 8)

		readMemory(p.Pid, uintptr(addr), data)
		addr = binary.LittleEndian.Uint64(data)

		expected := uint64(0x400fbc)
		if addr != expected {
			t.Fatalf("return address not found correctly, expected %#v got %#v", expected, addr)
		}
	})
}
