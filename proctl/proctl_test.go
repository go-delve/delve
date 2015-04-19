package proctl

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
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

func dataAtAddr(thread *ThreadContext, addr uint64) ([]byte, error) {
	data := make([]byte, 1)
	_, err := readMemory(thread, uintptr(addr), data)
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
	f, l, _ := p.goSymTable.PCToLine(pc)

	return f, l
}

func TestExit(t *testing.T) {
	withTestProcess("../_fixtures/continuetestprog", t, func(p *DebuggedProcess) {
		err := p.Continue()
		pe, ok := err.(ProcessExitedError)
		if !ok {
			t.Fatalf("Continue() returned unexpected error type")
		}
		if pe.Status != 0 {
			t.Errorf("Unexpected error status: %d", pe.Status)
		}
		if pe.Pid != p.Pid {
			t.Errorf("Unexpected process id: %d", pe.Pid)
		}
	})
}

func TestHalt(t *testing.T) {
	withTestProcess("../_fixtures/testprog", t, func(p *DebuggedProcess) {
		go func() {
			time.Sleep(10 * time.Millisecond)
			err := p.RequestManualStop()
			if err != nil {
				t.Fatal(err)
			}
		}()
		err := p.Continue()
		if err != nil {
			t.Fatal(err)
		}
		// Loop through threads and make sure they are all
		// actually stopped, err will not be nil if the process
		// is still running.
		for _, th := range p.Threads {
			_, err := th.Registers()
			if err != nil {
				t.Error(err)
			}
		}
	})
}

func TestStep(t *testing.T) {
	withTestProcess("../_fixtures/testprog", t, func(p *DebuggedProcess) {
		helloworldfunc := p.goSymTable.LookupFunc("main.helloworld")
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

func TestBreakPoint(t *testing.T) {
	withTestProcess("../_fixtures/testprog", t, func(p *DebuggedProcess) {
		helloworldfunc := p.goSymTable.LookupFunc("main.helloworld")
		helloworldaddr := helloworldfunc.Entry

		bp, err := p.Break(helloworldaddr)
		assertNoError(err, t, "Break()")
		assertNoError(p.Continue(), t, "Continue()")

		pc, err := p.CurrentPC()
		if err != nil {
			t.Fatal(err)
		}

		if pc-1 != bp.Addr && pc != bp.Addr {
			f, l, _ := p.goSymTable.PCToLine(pc)
			t.Fatalf("Break not respected:\nPC:%#v %s:%d\nFN:%#v \n", pc, f, l, bp.Addr)
		}
	})
}

func TestBreakPointInSeperateGoRoutine(t *testing.T) {
	withTestProcess("../_fixtures/testthreads", t, func(p *DebuggedProcess) {
		fn := p.goSymTable.LookupFunc("main.anotherthread")
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

		f, l, _ := p.goSymTable.PCToLine(pc)
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
		fn := p.goSymTable.LookupFunc("main.sleepytime")
		bp, err := p.Break(fn.Entry)
		assertNoError(err, t, "Break()")

		bp, err = p.Clear(fn.Entry)
		assertNoError(err, t, "Clear()")

		data, err := dataAtAddr(p.CurrentThread, bp.Addr)
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

type nextTest struct {
	begin, end int
}

func testnext(testcases []nextTest, initialLocation string, t *testing.T) {
	var executablePath = "../_fixtures/testnextprog"
	withTestProcess(executablePath, t, func(p *DebuggedProcess) {
		bp, err := p.BreakByLocation(initialLocation)
		assertNoError(err, t, "Break()")
		assertNoError(p.Continue(), t, "Continue()")
		p.Clear(bp.Addr)
		p.CurrentThread.SetPC(bp.Addr)

		f, ln := currentLineNumber(p, t)
		for _, tc := range testcases {
			if ln != tc.begin {
				t.Fatalf("Program not stopped at correct spot expected %d was %s:%d", tc.begin, filepath.Base(f), ln)
			}

			assertNoError(p.Next(), t, "Next() returned an error")

			f, ln = currentLineNumber(p, t)
			if ln != tc.end {
				t.Fatalf("Program did not continue to correct next location expected %d was %s:%d", tc.end, filepath.Base(f), ln)
			}
		}

		if len(p.BreakPoints) != 0 {
			t.Fatal("Not all breakpoints were cleaned up", len(p.BreakPoints))
		}
		for _, bp := range p.HWBreakPoints {
			if bp != nil {
				t.Fatal("Not all breakpoints were cleaned up", bp.Addr)
			}
		}
	})
}

func TestNextGeneral(t *testing.T) {
	testcases := []nextTest{
		{17, 19},
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
	}
	testnext(testcases, "main.testnext", t)
}

func TestNextGoroutine(t *testing.T) {
	testcases := []nextTest{
		{46, 47},
		{47, 42},
	}
	testnext(testcases, "main.testgoroutine", t)
}

func TestNextFunctionReturn(t *testing.T) {
	testcases := []nextTest{
		{13, 14},
		{14, 35},
	}
	testnext(testcases, "main.helloworld", t)
}

func TestFindReturnAddress(t *testing.T) {
	var testfile, _ = filepath.Abs("../_fixtures/testnextprog")

	withTestProcess(testfile, t, func(p *DebuggedProcess) {
		var (
			fdes = p.frameEntries
			gsd  = p.goSymTable
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

		readMemory(p.CurrentThread, uintptr(addr), data)
		addr = binary.LittleEndian.Uint64(data)

		_, l, _ := p.goSymTable.PCToLine(addr)
		if l != 40 {
			t.Fatalf("return address not found correctly, expected line 40")
		}
	})
}

func TestSwitchThread(t *testing.T) {
	var testfile, _ = filepath.Abs("../_fixtures/testnextprog")

	withTestProcess(testfile, t, func(p *DebuggedProcess) {
		// With invalid thread id
		err := p.SwitchThread(-1)
		if err == nil {
			t.Fatal("Expected error for invalid thread id")
		}
		pc, err := p.FindLocation("main.main")
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Break(pc)
		if err != nil {
			t.Fatal(err)
		}
		err = p.Continue()
		if err != nil {
			t.Fatal(err)
		}
		var nt int
		ct := p.CurrentThread.Id
		for tid, _ := range p.Threads {
			if tid != ct {
				nt = tid
				break
			}
		}
		if nt == 0 {
			t.Fatal("could not find thread to switch to")
		}
		// With valid thread id
		err = p.SwitchThread(nt)
		if err != nil {
			t.Fatal(err)
		}
		if p.CurrentThread.Id != nt {
			t.Fatal("Did not switch threads")
		}
	})
}

func TestFunctionCall(t *testing.T) {
	var testfile, _ = filepath.Abs("../_fixtures/testprog")

	withTestProcess(testfile, t, func(p *DebuggedProcess) {
		pc, err := p.FindLocation("main.main")
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Break(pc)
		if err != nil {
			t.Fatal(err)
		}
		err = p.Continue()
		if err != nil {
			t.Fatal(err)
		}
		pc, err = p.CurrentPC()
		if err != nil {
			t.Fatal(err)
		}
		fn := p.goSymTable.PCToFunc(pc)
		if fn == nil {
			t.Fatalf("Could not find func for PC: %#v", pc)
		}
		if fn.Name != "main.main" {
			t.Fatal("Program stopped at incorrect place")
		}
		if err = p.CallFn("runtime.getg", func() error {
			th := p.CurrentThread
			pc, err := th.CurrentPC()
			if err != nil {
				t.Fatal(err)
			}
			f := th.Process.goSymTable.LookupFunc("runtime.getg")
			if f == nil {
				t.Fatalf("could not find function %s", "runtime.getg")
			}
			if pc-1 != f.End-2 && pc != f.End-2 {
				t.Fatalf("wrong pc expected %#v got %#v", f.End-2, pc-1)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		pc, err = p.CurrentPC()
		if err != nil {
			t.Fatal(err)
		}
		fn = p.goSymTable.PCToFunc(pc)
		if fn == nil {
			t.Fatalf("Could not find func for PC: %#v", pc)
		}
		if fn.Name != "main.main" {
			t.Fatal("Program stopped at incorrect place")
		}
	})
}
