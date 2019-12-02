package gdbserial_test

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-delve/delve/pkg/debug"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

func TestMain(m *testing.M) {
	var logConf string
	flag.StringVar(&logConf, "log", "", "configures logging")
	flag.Parse()
	logflags.Setup(logConf != "", logConf, "")
	os.Exit(protest.RunTestsWithFixtures(m))
}

func withTestRecordedTarget(name string, t testing.TB, fn func(tgt *debug.Target, fixture protest.Fixture)) {
	fixture := protest.BuildFixture(name, 0)
	protest.MustHaveRecordingAllowed(t)
	if path, _ := exec.LookPath("rr"); path == "" {
		t.Skip("test skipped, rr not found")
	}
	t.Log("recording")
	tgt, err := debug.Launch([]string{fixture.Path}, ".", true, "rr", []string{})
	if err != nil {
		t.Fatal("Launch():", err)
	}

	defer func() {
		tgt.Detach(true)
	}()

	fn(tgt, fixture)
}

func assertNoError(err error, t testing.TB, s string) {
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		fname := filepath.Base(file)
		t.Fatalf("failed assertion at %s:%d: %s - %s\n", fname, line, s, err)
	}
}

func setFunctionBreakpoint(tgt *debug.Target, t *testing.T, fname string) *proc.Breakpoint {
	_, f, l, _ := runtime.Caller(1)
	f = filepath.Base(f)

	addrs, err := tgt.BinInfo().FindFunctionLocation(tgt, fname, 0)
	if err != nil {
		t.Fatalf("%s:%d: FindFunctionLocation(%s): %v", f, l, fname, err)
	}
	if len(addrs) != 1 {
		t.Fatalf("%s:%d: setFunctionBreakpoint(%s): too many results %v", f, l, fname, addrs)
	}
	bp, err := tgt.SetBreakpoint(addrs[0], proc.UserBreakpoint, nil)
	if err != nil {
		t.Fatalf("%s:%d: FindFunctionLocation(%s): %v", f, l, fname, err)
	}
	return bp
}

func TestRestartAfterExit(t *testing.T) {
	protest.AllowRecording(t)
	withTestRecordedTarget("testnextprog", t, func(tgt *debug.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(tgt, t, "main.main")
		assertNoError(tgt.Continue(), t, "Continue")
		loc, err := tgt.CurrentThread().Location()
		assertNoError(err, t, "CurrentThread().Location()")
		err = tgt.Continue()
		if _, isexited := err.(proc.ErrProcessExited); err == nil || !isexited {
			t.Fatalf("program did not exit: %v", err)
		}

		assertNoError(tgt.Restart(""), t, "Restart")

		assertNoError(tgt.Continue(), t, "Continue (after restart)")
		loc2, err := tgt.CurrentThread().Location()
		assertNoError(err, t, "CurrentThread().Location() (after restart)")
		if loc2.Line != loc.Line {
			t.Fatalf("stopped at %d (expected %d)", loc2.Line, loc.Line)
		}
		err = tgt.Continue()
		if _, isexited := err.(proc.ErrProcessExited); err == nil || !isexited {
			t.Fatalf("program did not exit (after exit): %v", err)
		}
	})
}

func TestRestartDuringStop(t *testing.T) {
	protest.AllowRecording(t)
	withTestRecordedTarget("testnextprog", t, func(tgt *debug.Target, fixture protest.Fixture) {
		setFunctionBreakpoint(tgt, t, "main.main")
		assertNoError(tgt.Continue(), t, "Continue")
		loc, err := tgt.CurrentThread().Location()
		assertNoError(err, t, "CurrentThread().Location()")

		assertNoError(tgt.Restart(""), t, "Restart")

		assertNoError(tgt.Continue(), t, "Continue (after restart)")
		loc2, err := tgt.CurrentThread().Location()
		assertNoError(err, t, "CurrentThread().Location() (after restart)")
		if loc2.Line != loc.Line {
			t.Fatalf("stopped at %d (expected %d)", loc2.Line, loc.Line)
		}
		err = tgt.Continue()
		if _, isexited := err.(proc.ErrProcessExited); err == nil || !isexited {
			t.Fatalf("program did not exit (after exit): %v", err)
		}
	})
}

func setFileBreakpoint(tgt *debug.Target, t *testing.T, fixture protest.Fixture, lineno int) *proc.Breakpoint {
	_, f, l, _ := runtime.Caller(1)
	f = filepath.Base(f)

	addrs, err := tgt.BinInfo().FindFileLocation(tgt, fixture.Source, lineno)
	if err != nil {
		t.Fatalf("%s:%d: FindFileLocation(%s, %d): %v", f, l, fixture.Source, lineno, err)
	}
	if len(addrs) != 1 {
		t.Fatalf("%s:%d: setFileLineBreakpoint(%s, %d): too many results %v", f, l, fixture.Source, lineno, addrs)
	}
	bp, err := tgt.SetBreakpoint(addrs[0], proc.UserBreakpoint, nil)
	if err != nil {
		t.Fatalf("%s:%d: SetBreakpoint: %v", f, l, err)
	}
	return bp
}

func TestReverseBreakpointCounts(t *testing.T) {
	protest.AllowRecording(t)
	withTestRecordedTarget("bpcountstest", t, func(tgt *debug.Target, fixture protest.Fixture) {
		endbp := setFileBreakpoint(tgt, t, fixture, 28)
		assertNoError(tgt.Continue(), t, "Continue()")
		loc, _ := tgt.CurrentThread().Location()
		if loc.PC != endbp.Addr {
			t.Fatalf("did not reach end of main.main function: %s:%d (%#x)", loc.File, loc.Line, loc.PC)
		}

		tgt.ClearBreakpoint(endbp.Addr)
		assertNoError(tgt.Direction(proc.Backward), t, "Switching to backward direction")
		bp := setFileBreakpoint(tgt, t, fixture, 12)
		startbp := setFileBreakpoint(tgt, t, fixture, 20)

	countLoop:
		for {
			assertNoError(tgt.Continue(), t, "Continue()")
			loc, _ := tgt.CurrentThread().Location()
			switch loc.PC {
			case startbp.Addr:
				break countLoop
			case bp.Addr:
				// ok
			default:
				t.Fatalf("unexpected stop location %s:%d %#x", loc.File, loc.Line, loc.PC)
			}
		}

		t.Logf("TotalHitCount: %d", bp.TotalHitCount)
		if bp.TotalHitCount != 200 {
			t.Fatalf("Wrong TotalHitCount for the breakpoint (%d)", bp.TotalHitCount)
		}

		if len(bp.HitCount) != 2 {
			t.Fatalf("Wrong number of goroutines for breakpoint (%d)", len(bp.HitCount))
		}

		for _, v := range bp.HitCount {
			if v != 100 {
				t.Fatalf("Wrong HitCount for breakpoint (%v)", bp.HitCount)
			}
		}
	})
}

func getPosition(tgt *debug.Target, t *testing.T) (when string, loc *proc.Location) {
	var err error
	when, err = tgt.When()
	assertNoError(err, t, "When")
	loc, err = tgt.CurrentThread().Location()
	assertNoError(err, t, "Location")
	return
}

func TestCheckpoints(t *testing.T) {
	protest.AllowRecording(t)
	withTestRecordedTarget("continuetestprog", t, func(tgt *debug.Target, fixture protest.Fixture) {
		// Continues until start of main.main, record output of 'when'
		bp := setFunctionBreakpoint(tgt, t, "main.main")
		assertNoError(tgt.Continue(), t, "Continue")
		when0, loc0 := getPosition(tgt, t)
		t.Logf("when0: %q (%#x)", when0, loc0.PC)

		// Create a checkpoint and check that the list of checkpoints reflects this
		cpid, err := tgt.Checkpoint("checkpoint1")
		if cpid != 1 {
			t.Errorf("unexpected checkpoint id %d", cpid)
		}
		assertNoError(err, t, "Checkpoint")
		checkpoints, err := tgt.Checkpoints()
		assertNoError(err, t, "Checkpoints")
		if len(checkpoints) != 1 {
			t.Fatalf("wrong number of checkpoints %v (one expected)", checkpoints)
		}

		// Move forward with next, check that the output of 'when' changes
		assertNoError(tgt.Next(), t, "First Next")
		assertNoError(tgt.Next(), t, "Second Next")
		when1, loc1 := getPosition(tgt, t)
		t.Logf("when1: %q (%#x)", when1, loc1.PC)
		if loc0.PC == loc1.PC {
			t.Fatalf("next did not move process %#x", loc0.PC)
		}
		if when0 == when1 {
			t.Fatalf("output of when did not change after next: %q", when0)
		}

		// Move back to checkpoint, check that the output of 'when' is the same as
		// what it was when we set the breakpoint
		tgt.Restart(fmt.Sprintf("c%d", cpid))
		when2, loc2 := getPosition(tgt, t)
		t.Logf("when2: %q (%#x)", when2, loc2.PC)
		if loc2.PC != loc0.PC {
			t.Fatalf("PC address mismatch %#x != %#x", loc0.PC, loc2.PC)
		}
		if when0 != when2 {
			t.Fatalf("output of when mismatched %q != %q", when0, when2)
		}

		// Move forward with next again, check that the output of 'when' matches
		assertNoError(tgt.Next(), t, "First Next")
		assertNoError(tgt.Next(), t, "Second Next")
		when3, loc3 := getPosition(tgt, t)
		t.Logf("when3: %q (%#x)", when3, loc3.PC)
		if loc3.PC != loc1.PC {
			t.Fatalf("PC address mismatch %#x != %#x", loc1.PC, loc3.PC)
		}
		if when3 != when1 {
			t.Fatalf("when output mismatch %q != %q", when1, when3)
		}

		// Delete breakpoint, move back to checkpoint then next twice and check
		// output of 'when' again
		_, err = tgt.ClearBreakpoint(bp.Addr)
		assertNoError(err, t, "ClearBreakpoint")
		tgt.Restart(fmt.Sprintf("c%d", cpid))
		assertNoError(tgt.Next(), t, "First Next")
		assertNoError(tgt.Next(), t, "Second Next")
		when4, loc4 := getPosition(tgt, t)
		t.Logf("when4: %q (%#x)", when4, loc4.PC)
		if loc4.PC != loc1.PC {
			t.Fatalf("PC address mismatch %#x != %#x", loc1.PC, loc4.PC)
		}
		if when4 != when1 {
			t.Fatalf("when output mismatch %q != %q", when1, when4)
		}

		// Delete checkpoint, check that the list of checkpoints is updated
		assertNoError(tgt.ClearCheckpoint(cpid), t, "ClearCheckpoint")
		checkpoints, err = tgt.Checkpoints()
		assertNoError(err, t, "Checkpoints")
		if len(checkpoints) != 0 {
			t.Fatalf("wrong number of checkpoints %v (zero expected)", checkpoints)
		}
	})
}

func TestIssue1376(t *testing.T) {
	// Backward Continue should terminate when it encounters the start of the process.
	protest.AllowRecording(t)
	withTestRecordedTarget("continuetestprog", t, func(tgt *debug.Target, fixture protest.Fixture) {
		bp := setFunctionBreakpoint(tgt, t, "main.main")
		assertNoError(tgt.Continue(), t, "Continue (forward)")
		_, err := tgt.ClearBreakpoint(bp.Addr)
		assertNoError(err, t, "ClearBreakpoint")
		assertNoError(tgt.Direction(proc.Backward), t, "Switching to backward direction")
		assertNoError(tgt.Continue(), t, "Continue (backward)")
	})
}
