// Layer 2 call-injection protocol fuzzing harness.
//
// This file drives the *real* call-injection protocol handler
// (funcCallStep/funcCallFinish in fncall.go, plus the evalStack.run opcode
// loop in eval.go) with a mock Thread whose protocol register (R12 on amd64,
// see runtime.debugCallV2) yields a caller-controlled sequence of values.
//
// Unlike a live target, no real process is stepped: the harness seeds state
// as if evalCallInjectionStart had already succeeded (a functionCallState is
// pushed with savedRegs/protocolReg/debugCallName filled in) and then feeds
// protocol register values into funcCallStep one step at a time. This lets us
// exercise the sequences that terminate *before* a successful
// CallInjectionSetTarget — the class of bugs behind issue #4085 — without
// needing runtime.debugCallV2 or a debuggee.
//
// Scope: the branches that do not require a realistic goroutine stack are
// fully supported and produce clean errors (or nil):
//   - debugCallRegRestoreRegisters (16): a premature RestoreRegisters finishes
//     the protocol via a no-op StepInstruction (stepInstructionOut exits because
//     the mock ThreadLocation has no function); without production guards this
//     empties fncalls and panics in CallInjectionSetTarget (#4085/#4363).
//     With the guards it yields a clean "terminated before target" error.
//   - debugCallRegCompleteCall (0) followed by CallInjectionSetTarget: callOP's
//     stack writes land in scratch memory (setPC/setSP are recorded no-ops).
//   - unknown/exhausted register values: funcCallStep's default no-op branch.
//
// The precheck-failure (8), read-return (1) and read-panic (2) branches call
// ThreadScope/readStackVariable, which unwind a real goroutine stack. The mock
// initializes BinaryInfo.Images, Target.scache, Function.cu, and PC/SP in the
// register slice so those paths fail cleanly (e.g. findType misses, unreadable
// stack memory, or empty return locals) instead of panicking on nil maps or
// incomplete register state. That is sufficient for panic hunting; a fully
// decodable stack is not required.
//
// Additional restriction: debugCallRegCompleteCall (0) may appear at most
// once per sequence. It legitimately signals "ready to call" only once per
// injection; the mock's fixed two-op stack (CallInjectionSetTarget then
// CallInjectionComplete, see setupPostStartCallInjection) has nothing left to
// execute for a second 0, so replaying it lands on a real, pre-existing
// sanity check in eval.go's evalStack.run ("eval program finished without
// error but N call injections still active") instead of a clean protocol
// error. decodeCallInjectionProtocolRegs and FuzzCallInjectionProtocol
// deliberately map a second 0 to an exhausted/unknown register value.

package proc

import (
	"debug/dwarf"
	"errors"
	"fmt"
	"go/constant"
	"strings"
	"testing"

	"github.com/go-delve/delve/pkg/dwarf/dwarfbuilder"
	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/internal/gosym"
	"github.com/go-delve/delve/pkg/internal/lru"
	"github.com/go-delve/delve/pkg/proc/evalop"
)

var errFuzzNoLiveTarget = errors.New("call injection terminated before target was set: no live target in fuzz harness")

const (
	fuzzProtocolFixedPC = 0x2000
	fuzzProtocolFixedSP = 0x1000
)

const fuzzProtocolExhaustedRegval = 0xdead

// ---------------------------------------------------------------------------
// Mock MemoryReadWriter: reads return zeroes, writes are discarded.
// ---------------------------------------------------------------------------

type fuzzProtocolMemory struct{}

func (*fuzzProtocolMemory) ReadMemory(b []byte, addr uint64) (int, error) {
	for i := range b {
		b[i] = 0
	}
	return len(b), nil
}

func (*fuzzProtocolMemory) WriteMemory(addr uint64, b []byte) (int, error) {
	return len(b), nil
}

// ---------------------------------------------------------------------------
// Mock Registers
// ---------------------------------------------------------------------------

type fuzzProtocolRegs struct {
	protocolReg uint64
	regval      uint64
}

func (r *fuzzProtocolRegs) PC() uint64            { return fuzzProtocolFixedPC }
func (r *fuzzProtocolRegs) SP() uint64            { return fuzzProtocolFixedSP }
func (r *fuzzProtocolRegs) BP() uint64            { return fuzzProtocolFixedSP }
func (r *fuzzProtocolRegs) LR() uint64            { return 0 }
func (r *fuzzProtocolRegs) TLS() uint64           { return 0 }
func (r *fuzzProtocolRegs) GAddr() (uint64, bool) { return 0, false }

func (r *fuzzProtocolRegs) Slice(floatingPoint bool) ([]Register, error) {
	var regs []Register
	regs = AppendUint64Register(regs, regnum.AMD64ToName(regnum.AMD64_Rip), fuzzProtocolFixedPC)
	regs = AppendUint64Register(regs, regnum.AMD64ToName(regnum.AMD64_Rsp), fuzzProtocolFixedSP)
	regs = AppendUint64Register(regs, regnum.AMD64ToName(r.protocolReg), r.regval)
	return regs, nil
}

func (r *fuzzProtocolRegs) Copy() (Registers, error) {
	cp := *r
	return &cp, nil
}

// ---------------------------------------------------------------------------
// Mock Thread
// ---------------------------------------------------------------------------

type fuzzProtocolThread struct {
	bi          *BinaryInfo
	mem         MemoryReadWriter
	protocolReg uint64
	regvals     []uint64
	step        int

	common      CommonThread
	setRegCalls int
}

func newFuzzProtocolThread(bi *BinaryInfo, regvals []uint64) *fuzzProtocolThread {
	return &fuzzProtocolThread{
		bi:          bi,
		mem:         &fuzzProtocolMemory{},
		protocolReg: regnum.AMD64_R12,
		regvals:     regvals,
	}
}

func (th *fuzzProtocolThread) currentRegval() uint64 {
	if th.step >= len(th.regvals) {
		return fuzzProtocolExhaustedRegval
	}
	return th.regvals[th.step]
}

func (th *fuzzProtocolThread) Breakpoint() *BreakpointState { return &BreakpointState{} }
func (th *fuzzProtocolThread) ThreadID() int                { return 1 }

func (th *fuzzProtocolThread) Registers() (Registers, error) {
	return &fuzzProtocolRegs{protocolReg: th.protocolReg, regval: th.currentRegval()}, nil
}

func (th *fuzzProtocolThread) RestoreRegisters(Registers) error { return nil }
func (th *fuzzProtocolThread) BinInfo() *BinaryInfo             { return th.bi }
func (th *fuzzProtocolThread) ProcessMemory() MemoryReadWriter  { return th.mem }
func (th *fuzzProtocolThread) SetCurrentBreakpoint(bool) error  { return nil }
func (th *fuzzProtocolThread) SoftExc() bool                    { return false }
func (th *fuzzProtocolThread) Common() *CommonThread            { return &th.common }

func (th *fuzzProtocolThread) SetReg(uint64, *op.DwarfRegister) error {
	th.setRegCalls++
	return nil
}

// ---------------------------------------------------------------------------
// Mock Process (implements proc.Process, embedded in Target)
// ---------------------------------------------------------------------------

type fuzzProtocolProcess struct {
	bi  *BinaryInfo
	mem MemoryReadWriter
}

func (p *fuzzProtocolProcess) BinInfo() *BinaryInfo          { return p.bi }
func (p *fuzzProtocolProcess) EntryPoint() (uint64, error)   { return 0, nil }
func (p *fuzzProtocolProcess) FindThread(int) (Thread, bool) { return nil, false }
func (p *fuzzProtocolProcess) ThreadList() []Thread          { return nil }
func (p *fuzzProtocolProcess) Breakpoints() *BreakpointMap   { return nil }
func (p *fuzzProtocolProcess) Memory() MemoryReadWriter      { return p.mem }

// ---------------------------------------------------------------------------
// Mock ProcessGroup (wired into TargetGroup)
// ---------------------------------------------------------------------------

type fuzzProtocolProcessGroup struct{}

func (fuzzProtocolProcessGroup) ContinueOnce(*ContinueOnceContext) (Thread, StopReason, error) {
	return nil, StopUnknown, errFuzzNoLiveTarget
}
func (fuzzProtocolProcessGroup) StepInstruction(int) error { return nil }
func (fuzzProtocolProcessGroup) Detach(int, bool) error    { return nil }
func (fuzzProtocolProcessGroup) Close() error              { return nil }

// ---------------------------------------------------------------------------
// Minimal DWARF image
// ---------------------------------------------------------------------------

func newFuzzProtocolImage() (*Image, dwarf.Offset) {
	dwb := dwarfbuilder.New()
	fnOff := dwb.AddSubprogram("fuzz.fn", fuzzProtocolFixedPC, fuzzProtocolFixedPC+1)
	dwb.TagClose()

	abbrev, _, _, info, _, _, _, _, _, err := dwb.Build()
	img := &Image{
		symTable:        &gosym.Table{},
		dwarfTreeCache:  lru.NewCache[dwarf.Offset, *godwarf.Tree](dwarfTreeCacheSize),
		workaroundCache: make(map[dwarf.Offset]*godwarf.Tree),
	}
	if err != nil {
		return img, fnOff
	}
	dw, err := dwarf.New(abbrev, nil, nil, info, nil, nil, nil, nil)
	if err != nil || dw == nil {
		return img, fnOff
	}
	img.dwarf = dw
	img.dwarfReader = dw.Reader()
	return img, fnOff
}

// ---------------------------------------------------------------------------
// Stack cache seeding
// ---------------------------------------------------------------------------

func seedFuzzProtocolStackCache(tgt *Target, bi *BinaryInfo, mem MemoryReadWriter, th *fuzzProtocolThread, g *G) {
	dregs := bi.Arch.addrAndStackRegsToDwarfRegisters(0, fuzzProtocolFixedPC, fuzzProtocolFixedSP, fuzzProtocolFixedSP, 0)
	dregs.CFA = int64(fuzzProtocolFixedSP + 0x80)

	loc := Location{PC: fuzzProtocolFixedPC, File: "?", Line: -1}
	top := Stackframe{
		Current: loc,
		Call:    loc,
		Regs:    dregs,
		Ret:     fuzzProtocolFixedPC + 1,
		stackHi: fuzzProtocolFixedSP + 0x1000,
		lastpc:  fuzzProtocolFixedPC,
	}
	bottom := Stackframe{Regs: dregs, Ret: 0, Err: NullAddrError{}}
	frames := []Stackframe{top, bottom}

	itThread := newStackIterator(tgt, bi, mem, dregs, 0, nil, 0)
	tgt.scache.put(0, th.ThreadID(), itThread, frames)

	if g != nil {
		itG := newStackIterator(tgt, bi, mem, dregs, g.stack.hi, g, 0)
		threadID := 0
		if g.Thread != nil {
			threadID = g.Thread.ThreadID()
		}
		tgt.scache.put(g.ID, threadID, itG, frames)
	}
}

// ---------------------------------------------------------------------------
// Setup: post-Start call injection state
// ---------------------------------------------------------------------------

func setupPostStartCallInjection(th *fuzzProtocolThread) (*evalStack, *EvalScope) {
	bi := th.bi
	mem := th.mem

	fuzzImage, fnOff := newFuzzProtocolImage()
	bi.Images = []*Image{fuzzImage}

	var savedRegs Registers = &fuzzProtocolRegs{protocolReg: th.protocolReg, regval: 0}

	tgt := &Target{
		Process:    &fuzzProtocolProcess{bi: bi, mem: mem},
		fncallForG: map[int64]*callInjection{},
	}
	tgt.scache.init()
	grp := &TargetGroup{procgrp: fuzzProtocolProcessGroup{}}

	g := &G{
		ID: 1,
		PC: fuzzProtocolFixedPC,
		SP: fuzzProtocolFixedSP,
		BP: fuzzProtocolFixedSP,
		stack: stack{
			lo: fuzzProtocolFixedSP - 0x1000,
			hi: fuzzProtocolFixedSP + 0x1000,
		},
		variable: &Variable{bi: bi, mem: mem},
	}
	th.common.g = g
	seedFuzzProtocolStackCache(tgt, bi, mem, th, g)
	tgt.fncallForG[g.ID] = &callInjection{startThreadID: th.ThreadID()}

	scope := &EvalScope{
		Mem:     mem,
		BinInfo: bi,
		g:       g,
		target:  tgt,
		callCtx: &callContext{
			grp: grp,
			p:   tgt,
		},
	}

	fncall := &functionCallState{
		protocolReg:    th.protocolReg,
		debugCallName:  "runtime.debugCallV2",
		savedRegs:      savedRegs,
		hasDebugPinner: true,
		fn: &Function{
			Entry:  fuzzProtocolFixedPC,
			offset: fnOff,
			cu:     &compileUnit{image: fuzzImage},
		},
	}

	stack := &evalStack{
		scope:     scope,
		curthread: th,
	}
	stack.fncallPush(fncall)

	stack.push(newConstant(constant.MakeBool(false), bi, mem))

	stack.ops = []evalop.Op{
		&evalop.CallInjectionSetTarget{},
		&evalop.CallInjectionComplete{DoPinning: false},
	}
	stack.opidx = 0

	return stack, scope
}

// ---------------------------------------------------------------------------
// Driver
// ---------------------------------------------------------------------------

func runCallInjectionProtocolFuzz(regvals []uint64) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("internal debugger error: panic: %v", r)
		}
	}()

	bi := NewBinaryInfo("linux", "amd64")
	th := newFuzzProtocolThread(bi, regvals)
	stack, scope := setupPostStartCallInjection(th)

	// len(regvals)+2: allow one funcCallStep per seeded register value, then
	// enough iterations for stack.run to flush CallInjectionSetTarget /
	// CallInjectionComplete after RestoreRegisters finishes the protocol.
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

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestCallInjectionProtocol(t *testing.T) {
	t.Run("PrematureRestore", func(t *testing.T) {
		err := runCallInjectionProtocolFuzz([]uint64{16})
		if err == nil {
			t.Fatal("expected error for premature RestoreRegisters before SetTarget")
		}
		if strings.Contains(err.Error(), "no live target") {
			t.Fatalf("stepInstructionOut should succeed in the mock; got harness short-circuit: %v", err)
		}
		failIfInternalDebuggerError(t, err)
		if !strings.Contains(err.Error(), "terminated before target") {
			t.Fatalf("expected clean terminated-before-target error, got: %v", err)
		}
	})

	t.Run("UnknownThenRestore", func(t *testing.T) {
		failIfInternalDebuggerError(t, runCallInjectionProtocolFuzz([]uint64{0x42, 16}))
	})

	seeds := [][]uint64{
		{0, 1, 16},
		{8, 16},
		{16},
		{0x42, 16},
		{1, 16},
		{2, 16},
		{8},
		{1},
		{2},
	}
	for _, s := range seeds {
		t.Run(fmt.Sprintf("seed/%#v", s), func(t *testing.T) {
			failIfInternalDebuggerError(t, runCallInjectionProtocolFuzz(s))
		})
	}
}

func decodeCallInjectionProtocolRegs(buf []byte) []uint64 {
	if len(buf) > 8 {
		buf = buf[:8]
	}
	regs := make([]uint64, len(buf))
	seenCompleteCall := false
	for i, b := range buf {
		switch b % 6 {
		case 0:
			if seenCompleteCall {
				regs[i] = fuzzProtocolExhaustedRegval
				continue
			}
			seenCompleteCall = true
			regs[i] = 0
		case 1:
			regs[i] = 1
		case 2:
			regs[i] = 2
		case 3:
			regs[i] = 8
		case 4:
			regs[i] = 16
		default:
			regs[i] = fuzzProtocolExhaustedRegval
		}
	}
	return regs
}

func FuzzCallInjectionProtocol(f *testing.F) {
	// Byte values map through decodeCallInjectionProtocolRegs (b%6):
	// 0→CompleteCall, 1→read-return, 2→read-panic, 3→precheck(8),
	// 4→RestoreRegisters(16), 5→exhausted/unknown.
	for _, seed := range [][]byte{
		{4},       // {16}
		{3, 4},    // {8, 16}
		{0, 1, 4}, // {0, 1, 16}
		{5, 4},    // {0x42, 16}
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, buf []byte) {
		failIfInternalDebuggerError(t, runCallInjectionProtocolFuzz(decodeCallInjectionProtocolRegs(buf)))
	})
}
