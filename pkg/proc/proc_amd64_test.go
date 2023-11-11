package proc_test

import (
	"go/constant"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/proc"
	protest "github.com/go-delve/delve/pkg/proc/test"
)

func TestStepInstructionOnBreakpoint(t *testing.T) {
	// StepInstruction should step one instruction forward when
	// PC is on a 1 byte instruction with a software breakpoint.
	protest.AllowRecording(t)
	withTestProcess("break/", t, func(p *proc.Target, grp *proc.TargetGroup, fixture protest.Fixture) {
		setFileBreakpoint(p, t, filepath.ToSlash(filepath.Join(fixture.BuildDir, "break_amd64.s")), 4)

		assertNoError(grp.Continue(), t, "Continue()")

		pc := getRegisters(p, t).PC()
		assertNoError(grp.StepInstruction(), t, "StepInstruction()")
		if pc == getRegisters(p, t).PC() {
			t.Fatal("Could not step a single instruction")
		}
	})
}

func TestNextUnknownInstr(t *testing.T) {
	if !goversion.VersionAfterOrEqual(runtime.Version(), 1, 10) {
		t.Skip("versions of Go before 1.10 can't assemble the instruction VPUNPCKLWD")
	}
	withTestProcess("nodisasm/", t, func(p *proc.Target, grp *proc.TargetGroup, fixture protest.Fixture) {
		setFunctionBreakpoint(p, t, "main.asmFunc")
		assertNoError(grp.Continue(), t, "Continue()")
		assertNoError(grp.Next(), t, "Next()")
	})
}

func TestIssue1656(t *testing.T) {
	withTestProcess("issue1656/", t, func(p *proc.Target, grp *proc.TargetGroup, fixture protest.Fixture) {
		setFileBreakpoint(p, t, filepath.ToSlash(filepath.Join(fixture.BuildDir, "main.s")), 5)
		assertNoError(grp.Continue(), t, "Continue()")
		t.Logf("step1\n")
		assertNoError(grp.Step(), t, "Step()")
		assertLineNumber(p, t, 8, "wrong line number after first step")
		t.Logf("step2\n")
		assertNoError(grp.Step(), t, "Step()")
		assertLineNumber(p, t, 9, "wrong line number after second step")
	})
}

func TestBreakpointConfusionOnResume(t *testing.T) {
	// Checks that SetCurrentBreakpoint, (*Thread).StepInstruction and
	// native.(*Thread).singleStep all agree on which breakpoint the thread is
	// stopped at.
	// This test checks for a regression introduced when fixing Issue #1656
	withTestProcess("nopbreakpoint/", t, func(p *proc.Target, grp *proc.TargetGroup, fixture protest.Fixture) {
		maindots := filepath.ToSlash(filepath.Join(fixture.BuildDir, "main.s"))
		maindotgo := filepath.ToSlash(filepath.Join(fixture.BuildDir, "main.go"))
		setFileBreakpoint(p, t, maindots, 5) // line immediately after the NOP
		assertNoError(grp.Continue(), t, "First Continue")
		assertLineNumber(p, t, 5, "not on main.s:5")
		setFileBreakpoint(p, t, maindots, 4)   // sets a breakpoint on the NOP line, which will be one byte before the breakpoint we currently are stopped at.
		setFileBreakpoint(p, t, maindotgo, 18) // set one extra breakpoint so that we can recover execution and check the global variable g
		assertNoError(grp.Continue(), t, "Second Continue")
		gvar := evalVariable(p, t, "g")
		if n, _ := constant.Int64Val(gvar.Value); n != 1 {
			t.Fatalf("wrong value of global variable 'g': %v (expected 1)", gvar.Value)
		}
	})
}

func TestCallInjectionFlagCorruption(t *testing.T) {
	// debugCallV2 has a bug in amd64 where its tail corrupts the FLAGS register by running an ADD instruction.
	// Since this problem exists in many versions of Go, instead of fixing
	// debugCallV2, we work around this problem by restoring FLAGS, one extra
	// time, after stepping out of debugCallV2.
	// Fixes issue https://github.com/go-delve/delve/issues/2985
	protest.MustSupportFunctionCalls(t, testBackend)

	withTestProcessArgs("badflags", t, ".", []string{"0"}, 0, func(p *proc.Target, grp *proc.TargetGroup, fixture protest.Fixture) {
		mainfn := p.BinInfo().LookupFunc()["main.main"][0]

		// Find JNZ instruction on line :14
		var addr uint64
		text, err := proc.Disassemble(p.Memory(), nil, p.Breakpoints(), p.BinInfo(), mainfn.Entry, mainfn.End)
		assertNoError(err, t, "Disassemble")
		for _, instr := range text {
			if instr.Loc.Line != 14 {
				continue
			}
			if proc.IsJNZ(instr.Inst) {
				addr = instr.Loc.PC
			}
		}
		if addr == 0 {
			t.Fatalf("Could not find JNZ instruction at line :14")
		}

		// Create breakpoint
		_, err = p.SetBreakpoint(0, addr, proc.UserBreakpoint, nil)
		assertNoError(err, t, "SetBreakpoint")

		// Continue to breakpoint
		assertNoError(grp.Continue(), t, "Continue()")
		assertLineNumber(p, t, 14, "expected line :14")

		// Save RFLAGS register
		rflagsBeforeCall := p.BinInfo().Arch.RegistersToDwarfRegisters(0, getRegisters(p, t)).Uint64Val(regnum.AMD64_Rflags)
		t.Logf("rflags before = %#x", rflagsBeforeCall)

		// Inject call to main.g()
		assertNoError(proc.EvalExpressionWithCalls(grp, p.SelectedGoroutine(), "g()", normalLoadConfig, true), t, "Call")

		// Check RFLAGS register after the call
		rflagsAfterCall := p.BinInfo().Arch.RegistersToDwarfRegisters(0, getRegisters(p, t)).Uint64Val(regnum.AMD64_Rflags)
		t.Logf("rflags after = %#x", rflagsAfterCall)

		if rflagsBeforeCall != rflagsAfterCall {
			t.Errorf("mismatched rflags value")
		}

		// Single step and check where we end up
		assertNoError(grp.Step(), t, "Step()")
		assertLineNumber(p, t, 17, "expected line :17") // since we passed "0" as argument we should be going into the false branch at line :17
	})
}
