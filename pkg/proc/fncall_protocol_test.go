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

// Tests the Start-before-SetTarget window from issue #4085: RestoreRegisters
// before CallInjectionComplete must abort the protocol, not panic in SetTarget.

const (
	protocolTestPC = 0x2000
	protocolTestSP = 0x1000
)

type protocolTestRegs struct {
	protocolReg uint64
	regval      uint64
}

func (r *protocolTestRegs) PC() uint64            { return protocolTestPC }
func (r *protocolTestRegs) SP() uint64            { return protocolTestSP }
func (r *protocolTestRegs) BP() uint64            { return protocolTestSP }
func (r *protocolTestRegs) LR() uint64            { return 0 }
func (r *protocolTestRegs) TLS() uint64           { return 0 }
func (r *protocolTestRegs) GAddr() (uint64, bool) { return 0, false }

func (r *protocolTestRegs) Slice(bool) ([]Register, error) {
	var regs []Register
	regs = AppendUint64Register(regs, regnum.AMD64ToName(regnum.AMD64_Rip), protocolTestPC)
	regs = AppendUint64Register(regs, regnum.AMD64ToName(regnum.AMD64_Rsp), protocolTestSP)
	regs = AppendUint64Register(regs, regnum.AMD64ToName(r.protocolReg), r.regval)
	return regs, nil
}

func (r *protocolTestRegs) Copy() (Registers, error) {
	cp := *r
	return &cp, nil
}

type protocolTestThread struct {
	bi          *BinaryInfo
	mem         MemoryReadWriter
	protocolReg uint64
	regvals     []uint64
	step        int
	common      CommonThread
}

func (th *protocolTestThread) currentRegval() uint64 {
	if th.step >= len(th.regvals) {
		return 0xdead
	}
	return th.regvals[th.step]
}

func (th *protocolTestThread) Breakpoint() *BreakpointState { return &BreakpointState{} }
func (th *protocolTestThread) ThreadID() int                { return 1 }
func (th *protocolTestThread) Registers() (Registers, error) {
	return &protocolTestRegs{protocolReg: th.protocolReg, regval: th.currentRegval()}, nil
}
func (th *protocolTestThread) RestoreRegisters(Registers) error { return nil }
func (th *protocolTestThread) BinInfo() *BinaryInfo             { return th.bi }
func (th *protocolTestThread) ProcessMemory() MemoryReadWriter  { return th.mem }
func (th *protocolTestThread) SetCurrentBreakpoint(bool) error  { return nil }
func (th *protocolTestThread) SoftExc() bool                    { return false }
func (th *protocolTestThread) Common() *CommonThread            { return &th.common }
func (th *protocolTestThread) SetReg(uint64, *op.DwarfRegister) error {
	return nil
}

type protocolTestProcess struct {
	bi  *BinaryInfo
	mem MemoryReadWriter
}

func (p *protocolTestProcess) BinInfo() *BinaryInfo          { return p.bi }
func (p *protocolTestProcess) EntryPoint() (uint64, error)   { return 0, nil }
func (p *protocolTestProcess) FindThread(int) (Thread, bool) { return nil, false }
func (p *protocolTestProcess) ThreadList() []Thread          { return nil }
func (p *protocolTestProcess) Breakpoints() *BreakpointMap   { return nil }
func (p *protocolTestProcess) Memory() MemoryReadWriter      { return p.mem }

type protocolTestProcessGroup struct{}

func (protocolTestProcessGroup) ContinueOnce(*ContinueOnceContext) (Thread, StopReason, error) {
	return nil, StopUnknown, errors.New("no live target in protocol test")
}
func (protocolTestProcessGroup) StepInstruction(int) error { return nil }
func (protocolTestProcessGroup) Detach(int, bool) error    { return nil }
func (protocolTestProcessGroup) Close() error              { return nil }

func setupPostStartCallInjection(th *protocolTestThread) (*evalStack, *EvalScope) {
	bi, mem := th.bi, th.mem
	tgt := &Target{
		Process:    &protocolTestProcess{bi: bi, mem: mem},
		fncallForG: map[int64]*callInjection{},
	}
	g := &G{
		ID: 1,
		PC: protocolTestPC,
		SP: protocolTestSP,
		BP: protocolTestSP,
		stack: stack{
			lo: protocolTestSP - 0x1000,
			hi: protocolTestSP + 0x1000,
		},
		variable: &Variable{bi: bi, mem: mem},
	}
	th.common.g = g
	tgt.fncallForG[g.ID] = &callInjection{startThreadID: th.ThreadID()}

	scope := &EvalScope{
		Mem:     mem,
		BinInfo: bi,
		g:       g,
		target:  tgt,
		callCtx: &callContext{
			grp: &TargetGroup{procgrp: protocolTestProcessGroup{}},
			p:   tgt,
		},
	}

	stack := &evalStack{scope: scope, curthread: th}
	stack.fncallPush(&functionCallState{
		protocolReg:    th.protocolReg,
		debugCallName:  "runtime.debugCallV2",
		savedRegs:      &protocolTestRegs{protocolReg: th.protocolReg},
		hasDebugPinner: true,
		fn:             &Function{Entry: protocolTestPC},
	})
	stack.push(newConstant(constant.MakeBool(false), bi, mem))
	stack.ops = []evalop.Op{
		&evalop.CallInjectionSetTarget{},
		&evalop.CallInjectionComplete{DoPinning: false},
	}
	return stack, scope
}

func runCallInjectionProtocol(regvals []uint64) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal debugger error: panic: %v", r)
		}
	}()

	th := &protocolTestThread{
		bi:          NewBinaryInfo("linux", "amd64"),
		mem:         &zeroFillMemory{},
		protocolReg: regnum.AMD64_R12,
		regvals:     regvals,
	}
	stack, scope := setupPostStartCallInjection(th)
	for step := 0; step < len(regvals)+2; step++ {
		th.step = step
		finished := funcCallStep(scope, stack, th)
		if finished {
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
		stack.run()
		if !stack.callInjectionContinue {
			break
		}
		stack.callInjectionContinue = false
	}
	return stack.err
}

func TestCallInjectionProtocol(t *testing.T) {
	t.Run("PrematureRestore", func(t *testing.T) {
		err := runCallInjectionProtocol([]uint64{16})
		if err == nil {
			t.Fatal("expected error for RestoreRegisters before CallInjectionComplete")
		}
		if strings.Contains(err.Error(), "no live target") {
			t.Fatalf("stepInstructionOut should succeed in the mock; got harness short-circuit: %v", err)
		}
		failIfInternalDebuggerError(t, err)
		if !strings.Contains(err.Error(), "terminated before the function call started") {
			t.Fatalf("expected protocol-abort error, got: %v", err)
		}
	})

	t.Run("RestoreAfterComplete", func(t *testing.T) {
		err := runCallInjectionProtocol([]uint64{0, 16})
		failIfInternalDebuggerError(t, err)
		if err != nil && strings.Contains(err.Error(), "terminated before the function call started") {
			t.Fatalf("RestoreRegisters after CompleteCall should not abort setup: %v", err)
		}
	})
}
