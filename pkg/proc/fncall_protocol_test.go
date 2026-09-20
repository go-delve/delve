// Post-Start call-injection protocol harness for issues #4085 / #4363.
//
// Seeds a functionCallState as if CallInjectionStart succeeded, then feeds
// protocol-register values into funcCallStep / resume. Premature
// RestoreRegisters (16) finishes the call before SetTarget and must not panic.
//
// Broader opcode / full-register fuzzing lives on fix/eval-stack-fuzz.

package proc

import (
	"errors"
	"fmt"
	"go/constant"
	"strings"
	"testing"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/proc/evalop"
)

const (
	fncallProtoPC = 0x2000
	fncallProtoSP = 0x1000
)

type fncallProtoMem struct{}

func (*fncallProtoMem) ReadMemory(b []byte, _ uint64) (int, error) {
	clear(b)
	return len(b), nil
}
func (*fncallProtoMem) WriteMemory(_ uint64, b []byte) (int, error) { return len(b), nil }

type fncallProtoRegs struct {
	protocolReg, regval uint64
}

func (r *fncallProtoRegs) PC() uint64            { return fncallProtoPC }
func (r *fncallProtoRegs) SP() uint64            { return fncallProtoSP }
func (r *fncallProtoRegs) BP() uint64            { return fncallProtoSP }
func (r *fncallProtoRegs) LR() uint64            { return 0 }
func (r *fncallProtoRegs) TLS() uint64           { return 0 }
func (r *fncallProtoRegs) GAddr() (uint64, bool) { return 0, false }
func (r *fncallProtoRegs) Slice(bool) ([]Register, error) {
	return AppendUint64Register(
		AppendUint64Register(
			AppendUint64Register(nil, regnum.AMD64ToName(regnum.AMD64_Rip), fncallProtoPC),
			regnum.AMD64ToName(regnum.AMD64_Rsp), fncallProtoSP),
		regnum.AMD64ToName(r.protocolReg), r.regval), nil
}
func (r *fncallProtoRegs) Copy() (Registers, error) { cp := *r; return &cp, nil }

type fncallProtoThread struct {
	bi          *BinaryInfo
	mem         MemoryReadWriter
	protocolReg uint64
	regvals     []uint64
	step        int
	common      CommonThread
}

func (th *fncallProtoThread) regval() uint64 {
	if th.step >= len(th.regvals) {
		return 0xdead
	}
	return th.regvals[th.step]
}
func (th *fncallProtoThread) Breakpoint() *BreakpointState { return &BreakpointState{} }
func (th *fncallProtoThread) ThreadID() int                { return 1 }
func (th *fncallProtoThread) Registers() (Registers, error) {
	return &fncallProtoRegs{protocolReg: th.protocolReg, regval: th.regval()}, nil
}
func (th *fncallProtoThread) RestoreRegisters(Registers) error { return nil }
func (th *fncallProtoThread) BinInfo() *BinaryInfo             { return th.bi }
func (th *fncallProtoThread) ProcessMemory() MemoryReadWriter  { return th.mem }
func (th *fncallProtoThread) SetCurrentBreakpoint(bool) error  { return nil }
func (th *fncallProtoThread) SoftExc() bool                    { return false }
func (th *fncallProtoThread) Common() *CommonThread            { return &th.common }
func (th *fncallProtoThread) SetReg(uint64, *op.DwarfRegister) error { return nil }

type fncallProtoProcess struct {
	bi  *BinaryInfo
	mem MemoryReadWriter
}

func (p *fncallProtoProcess) BinInfo() *BinaryInfo          { return p.bi }
func (p *fncallProtoProcess) EntryPoint() (uint64, error)   { return 0, nil }
func (p *fncallProtoProcess) FindThread(int) (Thread, bool) { return nil, false }
func (p *fncallProtoProcess) ThreadList() []Thread          { return nil }
func (p *fncallProtoProcess) Breakpoints() *BreakpointMap   { return nil }
func (p *fncallProtoProcess) Memory() MemoryReadWriter      { return p.mem }

type fncallProtoProcessGroup struct{}

func (fncallProtoProcessGroup) ContinueOnce(*ContinueOnceContext) (Thread, StopReason, error) {
	return nil, StopUnknown, errors.New("no live target in protocol harness")
}
func (fncallProtoProcessGroup) StepInstruction(int) error { return nil }
func (fncallProtoProcessGroup) Detach(int, bool) error    { return nil }
func (fncallProtoProcessGroup) Close() error              { return nil }

func runCallInjectionProtocol(regvals []uint64) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal debugger error: panic: %v", r)
		}
	}()

	bi := NewBinaryInfo("linux", "amd64")
	th := &fncallProtoThread{
		bi: bi, mem: &fncallProtoMem{}, protocolReg: regnum.AMD64_R12, regvals: regvals,
	}
	mem := th.mem
	tgt := &Target{
		Process:    &fncallProtoProcess{bi: bi, mem: mem},
		fncallForG: map[int64]*callInjection{},
	}
	g := &G{ID: 1}
	tgt.fncallForG[g.ID] = &callInjection{startThreadID: th.ThreadID()}
	scope := &EvalScope{
		Mem: mem, BinInfo: bi, g: g, target: tgt,
		callCtx: &callContext{grp: &TargetGroup{procgrp: fncallProtoProcessGroup{}}, p: tgt},
	}
	stack := &evalStack{scope: scope, curthread: th}
	stack.fncallPush(&functionCallState{
		protocolReg:    th.protocolReg,
		debugCallName:  "runtime.debugCallV2",
		savedRegs:      &fncallProtoRegs{protocolReg: th.protocolReg},
		hasDebugPinner: true,
		fn:             &Function{Entry: fncallProtoPC},
	})
	stack.push(newConstant(constant.MakeBool(false), bi, mem))
	stack.ops = []evalop.Op{
		&evalop.CallInjectionSetTarget{},
		&evalop.CallInjectionComplete{DoPinning: false},
	}

	for step := 0; step < len(regvals)+2; step++ {
		th.step = step
		if finished := funcCallStep(scope, stack, th); finished {
			funcCallFinish(scope, stack)
		}
		if stack.err == nil && len(stack.fncalls) > 0 {
			if fncall := stack.fncallPeek(); fncall.err != nil {
				stack.err = fncall.err
			}
		}
		if stack.callInjectionContinue {
			stack.callInjectionContinue = false
			continue
		}
		if stack.err != nil {
			break
		}
		// Like resume: continue opcode execution after the protocol step.
		stack.run()
		if !stack.callInjectionContinue {
			break
		}
		stack.callInjectionContinue = false
	}
	return stack.err
}

func failIfInternalDebuggerError(t testing.TB, err error) {
	t.Helper()
	if err != nil && strings.Contains(err.Error(), "internal debugger error") {
		t.Fatalf("unexpected internal debugger error: %v", err)
	}
}

func TestCallInjectionProtocol(t *testing.T) {
	tests := []struct {
		regs       []uint64
		wantSubstr string // if non-empty, err must contain this
	}{
		{[]uint64{16}, "terminated before target"}, // premature restore (#4085/#4363)
		{[]uint64{0x42, 16}, ""},                   // unknown then restore
		{[]uint64{0, 16}, ""},                      // complete-call then restore
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%v", tt.regs), func(t *testing.T) {
			err := runCallInjectionProtocol(tt.regs)
			failIfInternalDebuggerError(t, err)
			if tt.wantSubstr == "" {
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Fatalf("expected %q in error, got: %v", tt.wantSubstr, err)
			}
		})
	}
}
