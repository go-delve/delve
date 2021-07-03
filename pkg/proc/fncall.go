package proc

import (
	"debug/dwarf"
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/dwarf/reader"
	"github.com/go-delve/delve/pkg/dwarf/regnum"
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/logflags"
)

// This file implements the function call injection introduced in go1.11.
//
// The protocol is described in $GOROOT/src/runtime/asm_amd64.s in the
// comments for function runtime·debugCallV1.
//
// The main entry point is EvalExpressionWithCalls which will start a goroutine to
// evaluate the provided expression.
// This goroutine can either return immediately, if no function calls were
// needed, or write a continue request to the scope.callCtx.continueRequest
// channel. When this happens EvalExpressionWithCalls will call Continue and
// return.
//
// The Continue loop will write to scope.callCtx.continueCompleted when it
// hits a breakpoint in the call injection protocol.
//
// The work of setting up the function call and executing the protocol is
// done by evalFunctionCall and funcCallStep.

const (
	debugCallFunctionNamePrefix1 = "debugCall"
	debugCallFunctionNamePrefix2 = "runtime.debugCall"
	maxDebugCallVersion          = 2
	maxArgFrameSize              = 65535
	maxRegArgBytes               = 9*8 + 15*8 // TODO: Make this generic for other platforms.
)

var (
	errFuncCallUnsupported        = errors.New("function calls not supported by this version of Go")
	errFuncCallUnsupportedBackend = errors.New("backend does not support function calls")
	errFuncCallInProgress         = errors.New("cannot call function while another function call is already in progress")
	errNoGoroutine                = errors.New("no goroutine selected")
	errGoroutineNotRunning        = errors.New("selected goroutine not running")
	errNotEnoughStack             = errors.New("not enough stack space")
	errTooManyArguments           = errors.New("too many arguments")
	errNotEnoughArguments         = errors.New("not enough arguments")
	errNotAGoFunction             = errors.New("not a Go function")
	errFuncCallNotAllowed         = errors.New("function calls not allowed without using 'call'")
	errFuncCallNotAllowedStrAlloc = errors.New("literal string can not be allocated because function calls are not allowed without using 'call'")
)

type functionCallState struct {
	// savedRegs contains the saved registers
	savedRegs Registers
	// err contains a saved error
	err error
	// expr is the expression being evaluated
	expr *ast.CallExpr
	// fn is the function that is being called
	fn *Function
	// receiver is the receiver argument for the function
	receiver *Variable
	// closureAddr is the address of the closure being called
	closureAddr uint64
	// formalArgs are the formal arguments of fn
	formalArgs []funcCallArg
	// argFrameSize contains the size of the arguments
	argFrameSize int64
	// retvars contains the return variables after the function call terminates without panic'ing
	retvars []*Variable
	// panicvar is a variable used to store the value of the panic, if the
	// called function panics.
	panicvar *Variable
	// lateCallFailure is set to true if the function call could not be
	// completed after we started evaluating the arguments.
	lateCallFailure bool
}

type callContext struct {
	p *Target

	// checkEscape is true if the escape check should be performed.
	// See service/api.DebuggerCommand.UnsafeCall in service/api/types.go.
	checkEscape bool

	// retLoadCfg is the load configuration used to load return values
	retLoadCfg LoadConfig

	// Write to continueRequest to request a call to Continue from the
	// debugger's main goroutine.
	// Read from continueCompleted to wait for the target process to stop at
	// one of the interaction point of the function call protocol.
	// To signal that evaluation is completed a value will be written to
	// continueRequest having cont == false and the return values in ret.
	continueRequest   chan<- continueRequest
	continueCompleted <-chan *G

	// injectionThread is the thread to use for nested call injections if the
	// original injection goroutine isn't running (because we are in Go 1.15)
	injectionThread Thread

	// stacks is a slice of known goroutine stacks used to check for
	// inappropriate escapes
	stacks []stack
}

type continueRequest struct {
	cont bool
	err  error
	ret  *Variable
}

type callInjection struct {
	// if continueCompleted is not nil it means we are in the process of
	// executing an injected function call, see comments throughout
	// pkg/proc/fncall.go for a description of how this works.
	continueCompleted chan<- *G
	continueRequest   <-chan continueRequest
	startThreadID     int
}

func (callCtx *callContext) doContinue() *G {
	callCtx.continueRequest <- continueRequest{cont: true}
	return <-callCtx.continueCompleted
}

func (callCtx *callContext) doReturn(ret *Variable, err error) {
	if callCtx == nil {
		return
	}
	callCtx.continueRequest <- continueRequest{cont: false, ret: ret, err: err}
}

// EvalExpressionWithCalls is like EvalExpression but allows function calls in 'expr'.
// Because this can only be done in the current goroutine, unlike
// EvalExpression, EvalExpressionWithCalls is not a method of EvalScope.
func EvalExpressionWithCalls(t *Target, g *G, expr string, retLoadCfg LoadConfig, checkEscape bool) error {
	bi := t.BinInfo()
	if !t.SupportsFunctionCalls() {
		return errFuncCallUnsupportedBackend
	}

	// check that the target goroutine is running
	if g == nil {
		return errNoGoroutine
	}
	if g.Status != Grunning || g.Thread == nil {
		return errGoroutineNotRunning
	}

	if callinj := t.fncallForG[g.ID]; callinj != nil && callinj.continueCompleted != nil {
		return errFuncCallInProgress
	}

	dbgcallfn, _ := debugCallFunction(bi)
	if dbgcallfn == nil {
		return errFuncCallUnsupported
	}

	scope, err := GoroutineScope(t, g.Thread)
	if err != nil {
		return err
	}

	continueRequest := make(chan continueRequest)
	continueCompleted := make(chan *G)

	scope.callCtx = &callContext{
		p:                 t,
		checkEscape:       checkEscape,
		retLoadCfg:        retLoadCfg,
		continueRequest:   continueRequest,
		continueCompleted: continueCompleted,
	}

	t.fncallForG[g.ID] = &callInjection{
		continueCompleted: continueCompleted,
		continueRequest:   continueRequest,
		startThreadID:     0,
	}

	go scope.EvalExpression(expr, retLoadCfg)

	contReq, ok := <-continueRequest
	if contReq.cont {
		return t.Continue()
	}

	return finishEvalExpressionWithCalls(t, g, contReq, ok)
}

func finishEvalExpressionWithCalls(t *Target, g *G, contReq continueRequest, ok bool) error {
	fncallLog("stashing return values for %d in thread=%d", g.ID, g.Thread.ThreadID())
	g.Thread.Common().CallReturn = true
	var err error
	if !ok {
		err = errors.New("internal error EvalExpressionWithCalls didn't return anything")
	} else if contReq.err != nil {
		if fpe, ispanic := contReq.err.(fncallPanicErr); ispanic {
			g.Thread.Common().returnValues = []*Variable{fpe.panicVar}
		} else {
			err = contReq.err
		}
	} else if contReq.ret == nil {
		g.Thread.Common().returnValues = nil
	} else if contReq.ret.Addr == 0 && contReq.ret.DwarfType == nil && contReq.ret.Kind == reflect.Invalid {
		// this is a variable returned by a function call with multiple return values
		r := make([]*Variable, len(contReq.ret.Children))
		for i := range contReq.ret.Children {
			r[i] = &contReq.ret.Children[i]
		}
		g.Thread.Common().returnValues = r
	} else {
		g.Thread.Common().returnValues = []*Variable{contReq.ret}
	}

	close(t.fncallForG[g.ID].continueCompleted)
	delete(t.fncallForG, g.ID)
	return err
}

// evalFunctionCall evaluates a function call.
// If this is a built-in function it's evaluated directly.
// Otherwise this will start the function call injection protocol and
// request that the target process resumes.
// See the comment describing the field EvalScope.callCtx for a description
// of the preconditions that make starting the function call protocol
// possible.
// See runtime.debugCallV1 in $GOROOT/src/runtime/asm_amd64.s for a
// description of the protocol.
func evalFunctionCall(scope *EvalScope, node *ast.CallExpr) (*Variable, error) {
	r, err := scope.evalBuiltinCall(node)
	if r != nil || err != nil {
		// it was a builtin call
		return r, err
	}
	if scope.callCtx == nil {
		return nil, errFuncCallNotAllowed
	}
	thread := scope.g.Thread
	stacklo := scope.g.stack.lo
	if thread == nil {
		// We are doing a nested function call and using Go 1.15, the original
		// injection goroutine was suspended and now we are using a different
		// goroutine, evaluation still happend on the original goroutine but we
		// need to use a different thread to do the nested call injection.
		thread = scope.callCtx.injectionThread
		g2, err := GetG(thread)
		if err != nil {
			return nil, err
		}
		stacklo = g2.stack.lo
	}
	if thread == nil {
		return nil, errGoroutineNotRunning
	}

	p := scope.callCtx.p
	bi := scope.BinInfo
	if !p.SupportsFunctionCalls() {
		return nil, errFuncCallUnsupportedBackend
	}

	dbgcallfn, dbgcallversion := debugCallFunction(bi)
	if dbgcallfn == nil {
		return nil, errFuncCallUnsupported
	}

	// check that there are at least 256 bytes free on the stack
	regs, err := thread.Registers()
	if err != nil {
		return nil, err
	}
	regs, err = regs.Copy()
	if err != nil {
		return nil, err
	}
	if regs.SP()-256 <= stacklo {
		return nil, errNotEnoughStack
	}
	protocolReg, ok := debugCallProtocolReg(dbgcallversion)
	if !ok {
		return nil, errFuncCallUnsupported
	}
	if bi.Arch.RegistersToDwarfRegisters(0, regs).Reg(protocolReg) == nil {
		return nil, errFuncCallUnsupportedBackend
	}

	fncall := functionCallState{
		expr:      node,
		savedRegs: regs,
	}

	err = funcCallEvalFuncExpr(scope, &fncall, false)
	if err != nil {
		return nil, err
	}

	if err := callOP(bi, thread, regs, dbgcallfn.Entry); err != nil {
		return nil, err
	}
	// write the desired argument frame size at SP-(2*pointer_size) (the extra pointer is the saved PC)
	if err := writePointer(bi, scope.Mem, regs.SP()-3*uint64(bi.Arch.PtrSize()), uint64(fncall.argFrameSize)); err != nil {
		return nil, err
	}

	fncallLog("function call initiated %v frame size %d goroutine %d (thread %d)", fncall.fn, fncall.argFrameSize, scope.g.ID, thread.ThreadID())

	thread.Breakpoint().Clear() // since we moved address in PC the thread is no longer stopped at a breakpoint, leaving the breakpoint set will confuse Continue
	p.fncallForG[scope.g.ID].startThreadID = thread.ThreadID()

	spoff := int64(scope.Regs.Uint64Val(scope.Regs.SPRegNum)) - int64(scope.g.stack.hi)
	bpoff := int64(scope.Regs.Uint64Val(scope.Regs.BPRegNum)) - int64(scope.g.stack.hi)
	fboff := scope.Regs.FrameBase - int64(scope.g.stack.hi)

	for {
		scope.callCtx.injectionThread = nil
		g := scope.callCtx.doContinue()
		// Go 1.15 will move call injection execution to a different goroutine,
		// but we want to keep evaluation on the original goroutine.
		if g.ID == scope.g.ID {
			scope.g = g
		} else {
			// We are in Go 1.15 and we switched to a new goroutine, the original
			// goroutine is now parked and therefore does not have a thread
			// associated.
			scope.g.Thread = nil
			scope.g.Status = Gwaiting
			scope.callCtx.injectionThread = g.Thread
		}

		// adjust the value of registers inside scope
		pcreg, bpreg, spreg := scope.Regs.Reg(scope.Regs.PCRegNum), scope.Regs.Reg(scope.Regs.BPRegNum), scope.Regs.Reg(scope.Regs.SPRegNum)
		scope.Regs.ClearRegisters()
		scope.Regs.AddReg(scope.Regs.PCRegNum, pcreg)
		scope.Regs.AddReg(scope.Regs.BPRegNum, bpreg)
		scope.Regs.AddReg(scope.Regs.SPRegNum, spreg)
		scope.Regs.Reg(scope.Regs.SPRegNum).Uint64Val = uint64(spoff + int64(scope.g.stack.hi))
		scope.Regs.Reg(scope.Regs.BPRegNum).Uint64Val = uint64(bpoff + int64(scope.g.stack.hi))
		scope.Regs.FrameBase = fboff + int64(scope.g.stack.hi)
		scope.Regs.CFA = scope.frameOffset + int64(scope.g.stack.hi)

		finished := funcCallStep(scope, &fncall, g.Thread, protocolReg, dbgcallfn.Name)
		if finished {
			break
		}
	}

	if fncall.err != nil {
		return nil, fncall.err
	}

	if fncall.panicvar != nil {
		return nil, fncallPanicErr{fncall.panicvar}
	}
	switch len(fncall.retvars) {
	case 0:
		r := newVariable("", 0, nil, scope.BinInfo, nil)
		r.loaded = true
		r.Unreadable = errors.New("no return values")
		return r, nil
	case 1:
		return fncall.retvars[0], nil
	default:
		// create a fake variable without address or type to return multiple values
		r := newVariable("", 0, nil, scope.BinInfo, nil)
		r.loaded = true
		r.Children = make([]Variable, len(fncall.retvars))
		for i := range fncall.retvars {
			r.Children[i] = *fncall.retvars[i]
		}
		return r, nil
	}
}

// fncallPanicErr is the error returned if a called function panics
type fncallPanicErr struct {
	panicVar *Variable
}

func (err fncallPanicErr) Error() string {
	return "panic calling a function"
}

func fncallLog(fmtstr string, args ...interface{}) {
	logflags.FnCallLogger().Infof(fmtstr, args...)
}

// writePointer writes val as an architecture pointer at addr in mem.
func writePointer(bi *BinaryInfo, mem MemoryReadWriter, addr, val uint64) error {
	ptrbuf := make([]byte, bi.Arch.PtrSize())

	// TODO: use target architecture endianness instead of LittleEndian
	switch len(ptrbuf) {
	case 4:
		binary.LittleEndian.PutUint32(ptrbuf, uint32(val))
	case 8:
		binary.LittleEndian.PutUint64(ptrbuf, val)
	default:
		panic(fmt.Errorf("unsupported pointer size %d", len(ptrbuf)))
	}
	_, err := mem.WriteMemory(addr, ptrbuf)
	return err
}

// callOP simulates a call instruction on the given thread:
// * pushes the current value of PC on the stack (adjusting SP)
// * changes the value of PC to callAddr
// Note: regs are NOT updated!
func callOP(bi *BinaryInfo, thread Thread, regs Registers, callAddr uint64) error {
	sp := regs.SP()
	// push PC on the stack
	sp -= uint64(bi.Arch.PtrSize())
	if err := setSP(thread, sp); err != nil {
		return err
	}
	if err := writePointer(bi, thread.ProcessMemory(), sp, regs.PC()); err != nil {
		return err
	}
	return setPC(thread, callAddr)
}

// funcCallEvalFuncExpr evaluates expr.Fun and returns the function that we're trying to call.
// If allowCalls is false function calls will be disabled even if scope.callCtx != nil
func funcCallEvalFuncExpr(scope *EvalScope, fncall *functionCallState, allowCalls bool) error {
	bi := scope.BinInfo

	if !allowCalls {
		callCtx := scope.callCtx
		scope.callCtx = nil
		defer func() {
			scope.callCtx = callCtx
		}()
	}

	fnvar, err := scope.evalAST(fncall.expr.Fun)
	if err == errFuncCallNotAllowed {
		// we can't determine the frame size because callexpr.Fun can't be
		// evaluated without enabling function calls, just set up an argument
		// frame for the maximum possible argument size.
		fncall.argFrameSize = maxArgFrameSize
		return nil
	} else if err != nil {
		return err
	}
	if fnvar.Kind != reflect.Func {
		return fmt.Errorf("expression %q is not a function", exprToString(fncall.expr.Fun))
	}
	fnvar.loadValue(LoadConfig{false, 0, 0, 0, 0, 0})
	if fnvar.Unreadable != nil {
		return fnvar.Unreadable
	}
	if fnvar.Base == 0 {
		return errors.New("nil pointer dereference")
	}
	fncall.fn = bi.PCToFunc(uint64(fnvar.Base))
	if fncall.fn == nil {
		return fmt.Errorf("could not find DIE for function %q", exprToString(fncall.expr.Fun))
	}
	if !fncall.fn.cu.isgo {
		return errNotAGoFunction
	}
	fncall.closureAddr = fnvar.closureAddr

	fncall.argFrameSize, fncall.formalArgs, err = funcCallArgs(fncall.fn, bi, false)
	if err != nil {
		return err
	}

	argnum := len(fncall.expr.Args)

	// If the function variable has a child then that child is the method
	// receiver. However, if the method receiver is not being used (e.g.
	// func (_ X) Foo()) then it will not actually be listed as a formal
	// argument. Ensure that we are really off by 1 to add the receiver to
	// the function call.
	if len(fnvar.Children) > 0 && argnum == (len(fncall.formalArgs)-1) {
		argnum++
		fncall.receiver = &fnvar.Children[0]
		fncall.receiver.Name = exprToString(fncall.expr.Fun)
	}

	if argnum > len(fncall.formalArgs) {
		return errTooManyArguments
	}
	if argnum < len(fncall.formalArgs) {
		return errNotEnoughArguments
	}

	return nil
}

type funcCallArg struct {
	name       string
	typ        godwarf.Type
	off        int64
	dwarfEntry *godwarf.Tree // non-nil if Go 1.17+
	isret      bool
}

// funcCallEvalArgs evaluates the arguments of the function call, copying
// them into the argument frame starting at argFrameAddr.
func funcCallEvalArgs(scope *EvalScope, fncall *functionCallState, formalScope *EvalScope) error {
	if scope.g == nil {
		// this should never happen
		return errNoGoroutine
	}

	if fncall.receiver != nil {
		err := funcCallCopyOneArg(scope, fncall, fncall.receiver, &fncall.formalArgs[0], formalScope)
		if err != nil {
			return err
		}
		fncall.formalArgs = fncall.formalArgs[1:]
	}

	for i := range fncall.formalArgs {
		formalArg := &fncall.formalArgs[i]

		actualArg, err := scope.evalAST(fncall.expr.Args[i])
		if err != nil {
			return fmt.Errorf("error evaluating %q as argument %s in function %s: %v", exprToString(fncall.expr.Args[i]), formalArg.name, fncall.fn.Name, err)
		}
		actualArg.Name = exprToString(fncall.expr.Args[i])

		err = funcCallCopyOneArg(scope, fncall, actualArg, formalArg, formalScope)
		if err != nil {
			return err
		}
	}

	return nil
}

func funcCallCopyOneArg(scope *EvalScope, fncall *functionCallState, actualArg *Variable, formalArg *funcCallArg, formalScope *EvalScope) error {
	if scope.callCtx.checkEscape {
		//TODO(aarzilli): only apply the escapeCheck to leaking parameters.
		if err := escapeCheck(actualArg, formalArg.name, scope.g.stack); err != nil {
			return fmt.Errorf("cannot use %s as argument %s in function %s: %v", actualArg.Name, formalArg.name, fncall.fn.Name, err)
		}
		for _, stack := range scope.callCtx.stacks {
			if err := escapeCheck(actualArg, formalArg.name, stack); err != nil {
				return fmt.Errorf("cannot use %s as argument %s in function %s: %v", actualArg.Name, formalArg.name, fncall.fn.Name, err)
			}
		}
	}

	//TODO(aarzilli): autmoatic wrapping in interfaces for cases not handled
	// by convertToEface.

	var formalArgVar *Variable
	if formalArg.dwarfEntry != nil {
		var err error
		formalArgVar, err = extractVarInfoFromEntry(scope.target, formalScope.BinInfo, formalScope.image(), formalScope.Regs, formalScope.Mem, formalArg.dwarfEntry)
		if err != nil {
			return err
		}
	} else {
		formalArgVar = newVariable(formalArg.name, uint64(formalArg.off+int64(formalScope.Regs.CFA)), formalArg.typ, scope.BinInfo, scope.Mem)
	}
	if err := scope.setValue(formalArgVar, actualArg, actualArg.Name); err != nil {
		return err
	}

	return nil
}

func funcCallArgs(fn *Function, bi *BinaryInfo, includeRet bool) (argFrameSize int64, formalArgs []funcCallArg, err error) {
	dwarfTree, err := fn.cu.image.getDwarfTree(fn.offset)
	if err != nil {
		return 0, nil, fmt.Errorf("DWARF read error: %v", err)
	}

	producer := bi.Producer()
	trustArgOrder := producer != "" && goversion.ProducerAfterOrEqual(bi.Producer(), 1, 12)

	if bi.regabi && fn.cu.optimized && fn.Name != "runtime.mallocgc" {
		// Debug info for function arguments on optimized functions is currently
		// too incomplete to attempt injecting calls to arbitrary optimized
		// functions.
		// Prior to regabi we could do this because the ABI was simple enough to
		// manually encode it in Delve.
		// Runtime.mallocgc is an exception, we specifically patch it's DIE to be
		// correct for call injection purposes.
		return 0, nil, fmt.Errorf("can not call optimized function %s when regabi is in use", fn.Name)
	}

	varEntries := reader.Variables(dwarfTree, fn.Entry, int(^uint(0)>>1), reader.VariablesSkipInlinedSubroutines)

	// typechecks arguments, calculates argument frame size
	for _, entry := range varEntries {
		if entry.Tag != dwarf.TagFormalParameter {
			continue
		}
		argname, typ, err := readVarEntry(entry.Tree, fn.cu.image)
		if err != nil {
			return 0, nil, err
		}
		typ = resolveTypedef(typ)

		var formalArg *funcCallArg
		if bi.regabi {
			formalArg, err = funcCallArgRegABI(fn, bi, entry, argname, typ, &argFrameSize)
		} else {
			formalArg, err = funcCallArgOldABI(fn, bi, entry, argname, typ, trustArgOrder, &argFrameSize)
		}
		if err != nil {
			return 0, nil, err
		}
		if !formalArg.isret || includeRet {
			formalArgs = append(formalArgs, *formalArg)
		}
	}

	if bi.regabi {
		// The argument frame size is computed conservatively, assuming that
		// there's space for each argument on the stack even if its passed in
		// registers. Unfortunately this isn't quite enough because the register
		// assignment algorithm Go uses can result in an almost-unbounded amount of
		// additional space used due to alignment requirements (bounded by the
		// number of argument registers). Because we currently don't have an easy
		// way to obtain the frame size, let's be even more conservative.
		// A safe lower-bound on the size of the argument frame includes space for
		// each argument plus the total bytes of register arguments.
		// This is derived from worst-case alignment padding of up to
		// (pointer-word-bytes - 1) per argument passed in registers.
		// TODO: Make this generic for other platforms.
		argFrameSize = alignAddr(argFrameSize, 8)
		argFrameSize += maxRegArgBytes
	}

	sort.Slice(formalArgs, func(i, j int) bool {
		return formalArgs[i].off < formalArgs[j].off
	})

	return argFrameSize, formalArgs, nil
}

func funcCallArgOldABI(fn *Function, bi *BinaryInfo, entry reader.Variable, argname string, typ godwarf.Type, trustArgOrder bool, pargFrameSize *int64) (*funcCallArg, error) {
	const CFA = 0x1000
	var off int64

	locprog, _, err := bi.locationExpr(entry, dwarf.AttrLocation, fn.Entry)
	if err != nil {
		err = fmt.Errorf("could not get argument location of %s: %v", argname, err)
	} else {
		var pieces []op.Piece
		off, pieces, err = op.ExecuteStackProgram(op.DwarfRegisters{CFA: CFA, FrameBase: CFA}, locprog, bi.Arch.PtrSize())
		if err != nil {
			err = fmt.Errorf("unsupported location expression for argument %s: %v", argname, err)
		}
		if pieces != nil {
			err = fmt.Errorf("unsupported location expression for argument %s (uses DW_OP_piece)", argname)
		}
		off -= CFA
	}
	if err != nil {
		if !trustArgOrder {
			return nil, err
		}

		// With Go version 1.12 or later we can trust that the arguments appear
		// in the same order as declared, which means we can calculate their
		// address automatically.
		// With this we can call optimized functions (which sometimes do not have
		// an argument address, due to a compiler bug) as well as runtime
		// functions (which are always optimized).
		off = *pargFrameSize
		off = alignAddr(off, typ.Align())
	}

	if e := off + typ.Size(); e > *pargFrameSize {
		*pargFrameSize = e
	}

	isret, _ := entry.Val(dwarf.AttrVarParam).(bool)
	return &funcCallArg{name: argname, typ: typ, off: off, isret: isret}, nil
}

func funcCallArgRegABI(fn *Function, bi *BinaryInfo, entry reader.Variable, argname string, typ godwarf.Type, pargFrameSize *int64) (*funcCallArg, error) {
	// Conservatively calculate the full stack argument space for ABI0.
	*pargFrameSize = alignAddr(*pargFrameSize, typ.Align())
	*pargFrameSize += typ.Size()

	isret, _ := entry.Val(dwarf.AttrVarParam).(bool)
	return &funcCallArg{name: argname, typ: typ, dwarfEntry: entry.Tree, isret: isret}, nil
}

// alignAddr rounds up addr to a multiple of align. Align must be a power of 2.
func alignAddr(addr, align int64) int64 {
	return (addr + int64(align-1)) &^ int64(align-1)
}

func escapeCheck(v *Variable, name string, stack stack) error {
	switch v.Kind {
	case reflect.Ptr:
		var w *Variable
		if len(v.Children) == 1 {
			// this branch is here to support pointers constructed with typecasts from ints or the '&' operator
			w = &v.Children[0]
		} else {
			w = v.maybeDereference()
		}
		return escapeCheckPointer(w.Addr, name, stack)
	case reflect.Chan, reflect.String, reflect.Slice:
		return escapeCheckPointer(v.Base, name, stack)
	case reflect.Map:
		sv := v.clone()
		sv.RealType = resolveTypedef(&(v.RealType.(*godwarf.MapType).TypedefType))
		sv = sv.maybeDereference()
		return escapeCheckPointer(sv.Addr, name, stack)
	case reflect.Struct:
		t := v.RealType.(*godwarf.StructType)
		for _, field := range t.Field {
			fv, _ := v.toField(field)
			if err := escapeCheck(fv, fmt.Sprintf("%s.%s", name, field.Name), stack); err != nil {
				return err
			}
		}
	case reflect.Array:
		for i := int64(0); i < v.Len; i++ {
			sv, _ := v.sliceAccess(int(i))
			if err := escapeCheck(sv, fmt.Sprintf("%s[%d]", name, i), stack); err != nil {
				return err
			}
		}
	case reflect.Func:
		if err := escapeCheckPointer(v.funcvalAddr(), name, stack); err != nil {
			return err
		}
	}

	return nil
}

func escapeCheckPointer(addr uint64, name string, stack stack) error {
	if uint64(addr) >= stack.lo && uint64(addr) < stack.hi {
		return fmt.Errorf("stack object passed to escaping pointer: %s", name)
	}
	return nil
}

const (
	debugCallRegPrecheckFailed   = 8
	debugCallRegCompleteCall     = 0
	debugCallRegReadReturn       = 1
	debugCallRegReadPanic        = 2
	debugCallRegRestoreRegisters = 16
)

// funcCallStep executes one step of the function call injection protocol.
func funcCallStep(callScope *EvalScope, fncall *functionCallState, thread Thread, protocolReg uint64, debugCallName string) bool {
	p := callScope.callCtx.p
	bi := p.BinInfo()

	regs, err := thread.Registers()
	if err != nil {
		fncall.err = err
		return true
	}

	regval := bi.Arch.RegistersToDwarfRegisters(0, regs).Uint64Val(protocolReg)

	if logflags.FnCall() {
		loc, _ := thread.Location()
		var pc uint64
		var fnname string
		if loc != nil {
			pc = loc.PC
			if loc.Fn != nil {
				fnname = loc.Fn.Name
			}
		}
		fncallLog("function call interrupt gid=%d (original) thread=%d regval=%#x (PC=%#x in %s)", callScope.g.ID, thread.ThreadID(), regval, pc, fnname)
	}

	switch regval {
	case debugCallRegPrecheckFailed:
		// get error from top of the stack and return it to user
		errvar, err := readTopstackVariable(p, thread, regs, "string", loadFullValue)
		if err != nil {
			fncall.err = fmt.Errorf("could not get precheck error reason: %v", err)
			break
		}
		errvar.Name = "err"
		fncall.err = fmt.Errorf("%v", constant.StringVal(errvar.Value))

	case debugCallRegCompleteCall:
		p.fncallForG[callScope.g.ID].startThreadID = 0
		// evaluate arguments of the target function, copy them into its argument frame and call the function
		if fncall.fn == nil || fncall.receiver != nil || fncall.closureAddr != 0 {
			// if we couldn't figure out which function we are calling before
			// (because the function we are calling is the return value of a call to
			// another function) now we have to figure it out by recursively
			// evaluating the function calls.
			// This also needs to be done if the function call has a receiver
			// argument or a closure address (because those addresses could be on the stack
			// and have changed position between the start of the call and now).

			err := funcCallEvalFuncExpr(callScope, fncall, true)
			if err != nil {
				fncall.err = err
				fncall.lateCallFailure = true
				break
			}
			//TODO: double check that function call size isn't too big
		}

		// instead of evaluating the arguments we start first by pushing the call
		// on the stack, this is the opposite of what would happen normally but
		// it's necessary because otherwise the GC wouldn't be able to deal with
		// the argument frame.
		if fncall.closureAddr != 0 {
			// When calling a function pointer we must set the DX register to the
			// address of the function pointer itself.
			setClosureReg(thread, fncall.closureAddr)
		}
		cfa := regs.SP()
		oldpc := regs.PC()
		callOP(bi, thread, regs, fncall.fn.Entry)
		formalScope, err := GoroutineScope(callScope.target, thread)
		if formalScope != nil && formalScope.Regs.CFA != int64(cfa) {
			// This should never happen, checking just to avoid hard to figure out disasters.
			err = fmt.Errorf("mismatch in CFA %#x (calculated) %#x (expected)", formalScope.Regs.CFA, int64(cfa))
		}
		if err == nil {
			err = funcCallEvalArgs(callScope, fncall, formalScope)
		}

		if err != nil {
			// rolling back the call, note: this works because we called regs.Copy() above
			setSP(thread, cfa)
			setPC(thread, oldpc)
			fncall.err = err
			fncall.lateCallFailure = true
			break
		}

	case debugCallRegRestoreRegisters:
		// runtime requests that we restore the registers (all except pc and sp),
		// this is also the last step of the function call protocol.
		pc, sp := regs.PC(), regs.SP()
		if err := thread.RestoreRegisters(fncall.savedRegs); err != nil {
			fncall.err = fmt.Errorf("could not restore registers: %v", err)
		}
		if err := setPC(thread, pc); err != nil {
			fncall.err = fmt.Errorf("could not restore PC: %v", err)
		}
		if err := setSP(thread, sp); err != nil {
			fncall.err = fmt.Errorf("could not restore SP: %v", err)
		}
		if err := stepInstructionOut(p, thread, debugCallName, debugCallName); err != nil {
			fncall.err = fmt.Errorf("could not step out of %s: %v", debugCallName, err)
		}
		return true

	case debugCallRegReadReturn:
		// read return arguments from stack
		if fncall.panicvar != nil || fncall.lateCallFailure {
			break
		}
		retScope, err := ThreadScope(p, thread)
		if err != nil {
			fncall.err = fmt.Errorf("could not get return values: %v", err)
			break
		}

		// pretend we are still inside the function we called
		fakeFunctionEntryScope(retScope, fncall.fn, int64(regs.SP()), regs.SP()-uint64(bi.Arch.PtrSize()))
		retScope.trustArgOrder = !bi.regabi

		fncall.retvars, err = retScope.Locals()
		if err != nil {
			fncall.err = fmt.Errorf("could not get return values: %v", err)
			break
		}
		fncall.retvars = filterVariables(fncall.retvars, func(v *Variable) bool {
			return (v.Flags & VariableReturnArgument) != 0
		})

		loadValues(fncall.retvars, callScope.callCtx.retLoadCfg)
		for _, v := range fncall.retvars {
			v.Flags |= VariableFakeAddress
		}

		// Store the stack span of the currently running goroutine (which in Go >=
		// 1.15 might be different from the original injection goroutine) so that
		// later on we can use it to perform the escapeCheck
		if threadg, _ := GetG(thread); threadg != nil {
			callScope.callCtx.stacks = append(callScope.callCtx.stacks, threadg.stack)
		}

	case debugCallRegReadPanic:
		// read panic value from stack
		fncall.panicvar, err = readTopstackVariable(p, thread, regs, "interface {}", callScope.callCtx.retLoadCfg)
		if err != nil {
			fncall.err = fmt.Errorf("could not get panic: %v", err)
			break
		}
		fncall.panicvar.Name = "~panic"

	default:
		// Got an unknown protocol register value, this is probably bad but the safest thing
		// possible is to ignore it and hope it didn't matter.
		fncallLog("unknown value of protocol register %#x", regval)
	}

	return false
}

func readTopstackVariable(t *Target, thread Thread, regs Registers, typename string, loadCfg LoadConfig) (*Variable, error) {
	bi := thread.BinInfo()
	scope, err := ThreadScope(t, thread)
	if err != nil {
		return nil, err
	}
	typ, err := bi.findType(typename)
	if err != nil {
		return nil, err
	}
	v := newVariable("", regs.SP(), typ, scope.BinInfo, scope.Mem)
	v.loadValue(loadCfg)
	if v.Unreadable != nil {
		return nil, v.Unreadable
	}
	v.Flags |= VariableFakeAddress
	return v, nil
}

// fakeEntryScope alters scope to pretend that we are at the entry point of
// fn and CFA and SP are the ones passed as argument.
// This function is used to create a scope for a call frame that doesn't
// exist anymore, to read the return variables of an injected function call,
// or after a stepout command.
func fakeFunctionEntryScope(scope *EvalScope, fn *Function, cfa int64, sp uint64) error {
	scope.PC = fn.Entry
	scope.Fn = fn
	scope.File, scope.Line, _ = scope.BinInfo.PCToLine(fn.Entry)

	scope.Regs.CFA = cfa
	scope.Regs.Reg(scope.Regs.SPRegNum).Uint64Val = sp
	scope.Regs.Reg(scope.Regs.PCRegNum).Uint64Val = fn.Entry

	fn.cu.image.dwarfReader.Seek(fn.offset)
	e, err := fn.cu.image.dwarfReader.Next()
	if err != nil {
		return err
	}
	scope.Regs.FrameBase, _, _, _ = scope.BinInfo.Location(e, dwarf.AttrFrameBase, scope.PC, scope.Regs)
	return nil
}

// allocString allocates spaces for the contents of v if it needs to be allocated
func allocString(scope *EvalScope, v *Variable) error {
	if v.Base != 0 || v.Len == 0 {
		// already allocated
		return nil
	}

	if scope.callCtx == nil {
		return errFuncCallNotAllowedStrAlloc
	}
	savedLoadCfg := scope.callCtx.retLoadCfg
	scope.callCtx.retLoadCfg = loadFullValue
	defer func() {
		scope.callCtx.retLoadCfg = savedLoadCfg
	}()
	mallocv, err := evalFunctionCall(scope, &ast.CallExpr{
		Fun: &ast.SelectorExpr{
			X:   &ast.Ident{Name: "runtime"},
			Sel: &ast.Ident{Name: "mallocgc"},
		},
		Args: []ast.Expr{
			&ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(int(v.Len))},
			&ast.Ident{Name: "nil"},
			&ast.Ident{Name: "false"},
		},
	})
	if err != nil {
		return err
	}
	if mallocv.Unreadable != nil {
		return mallocv.Unreadable
	}
	if mallocv.DwarfType.String() != "*void" {
		return fmt.Errorf("unexpected return type for mallocgc call: %v", mallocv.DwarfType.String())
	}
	if len(mallocv.Children) != 1 {
		return errors.New("internal error, could not interpret return value of mallocgc call")
	}
	v.Base = mallocv.Children[0].Addr
	_, err = scope.Mem.WriteMemory(v.Base, []byte(constant.StringVal(v.Value)))
	return err
}

func isCallInjectionStop(t *Target, thread Thread, loc *Location) bool {
	if loc.Fn == nil {
		return false
	}
	if !strings.HasPrefix(loc.Fn.Name, debugCallFunctionNamePrefix1) && !strings.HasPrefix(loc.Fn.Name, debugCallFunctionNamePrefix2) {
		return false
	}
	if loc.PC == loc.Fn.Entry {
		// call injection just started, did not make any progress before being interrupted by a concurrent breakpoint.
		return false
	}
	text, err := disassembleCurrentInstruction(t, thread, -1)
	if err != nil || len(text) <= 0 {
		return false
	}
	return text[0].IsHardBreak()
}

// callInjectionProtocol is the function called from Continue to progress
// the injection protocol for all threads.
// Returns true if a call injection terminated
func callInjectionProtocol(t *Target, threads []Thread) (done bool, err error) {
	if len(t.fncallForG) == 0 {
		// we aren't injecting any calls, no need to check the threads.
		return false, nil
	}
	for _, thread := range threads {
		loc, err := thread.Location()
		if err != nil {
			continue
		}
		if !isCallInjectionStop(t, thread, loc) {
			continue
		}

		g, callinj, err := findCallInjectionStateForThread(t, thread)
		if err != nil {
			return false, err
		}

		fncallLog("step for injection on goroutine %d (current) thread=%d (location %s)", g.ID, thread.ThreadID(), loc.Fn.Name)
		callinj.continueCompleted <- g
		contReq, ok := <-callinj.continueRequest
		if !contReq.cont {
			err := finishEvalExpressionWithCalls(t, g, contReq, ok)
			if err != nil {
				return done, err
			}
			done = true
		}
	}
	return done, nil
}

func findCallInjectionStateForThread(t *Target, thread Thread) (*G, *callInjection, error) {
	g, err := GetG(thread)
	if err != nil {
		return nil, nil, fmt.Errorf("could not determine running goroutine for thread %#x currently executing the function call injection protocol: %v", thread.ThreadID(), err)
	}
	fncallLog("findCallInjectionStateForThread thread=%d goroutine=%d", thread.ThreadID(), g.ID)
	notfound := func() error {
		return fmt.Errorf("could not recover call injection state for goroutine %d (thread %d)", g.ID, thread.ThreadID())
	}
	callinj := t.fncallForG[g.ID]
	if callinj != nil {
		if callinj.continueCompleted == nil {
			return nil, nil, notfound()
		}
		return g, callinj, nil
	}

	// In Go 1.15 and later the call injection protocol will switch to a
	// different goroutine.
	// Here we try to recover the injection goroutine by checking the injection
	// thread.

	for goid, callinj := range t.fncallForG {
		if callinj != nil && callinj.continueCompleted != nil && callinj.startThreadID != 0 && callinj.startThreadID == thread.ThreadID() {
			t.fncallForG[g.ID] = callinj
			fncallLog("goroutine %d is the goroutine executing the call injection started in goroutine %d", g.ID, goid)
			return g, callinj, nil
		}
	}

	return nil, nil, notfound()
}

// debugCallFunction searches for the debug call function in the binary and
// uses this search to detect the debug call version.
// Returns the debug call function and its version as an integer (the lowest
// valid version is 1) or nil and zero.
func debugCallFunction(bi *BinaryInfo) (*Function, int) {
	for version := maxDebugCallVersion; version >= 1; version-- {
		name := debugCallFunctionNamePrefix2 + "V" + strconv.Itoa(version)
		fn, ok := bi.LookupFunc[name]
		if ok && fn != nil {
			return fn, version
		}
	}
	return nil, 0
}

// debugCallProtocolReg returns the register ID (as defined in pkg/dwarf/regnum)
// of the register used in the debug call protocol, given the debug call version.
// Also returns a bool indicating whether the version is supported.
func debugCallProtocolReg(version int) (uint64, bool) {
	// TODO(aarzilli): make this generic when call injection is supported on other architectures.
	var protocolReg uint64
	switch version {
	case 1:
		protocolReg = regnum.AMD64_Rax
	case 2:
		protocolReg = regnum.AMD64_R12
	default:
		return 0, false
	}
	return protocolReg, true
}

type fakeEntry map[dwarf.Attr]interface{}

func (e fakeEntry) Val(attr dwarf.Attr) interface{} {
	return e[attr]
}

func regabiMallocgcWorkaround(bi *BinaryInfo) ([]*godwarf.Tree, error) {
	var err1 error

	t := func(name string) godwarf.Type {
		if err1 != nil {
			return nil
		}
		typ, err := bi.findType(name)
		if err != nil {
			err1 = err
			return nil
		}
		return typ
	}

	m := func(name string, typ godwarf.Type, reg int, isret bool) *godwarf.Tree {
		if err1 != nil {
			return nil
		}
		var e fakeEntry = map[dwarf.Attr]interface{}{
			dwarf.AttrName:     name,
			dwarf.AttrType:     typ.Common().Offset,
			dwarf.AttrLocation: []byte{byte(op.DW_OP_reg0) + byte(reg)},
			dwarf.AttrVarParam: isret,
		}

		return &godwarf.Tree{
			Entry: e,
			Tag:   dwarf.TagFormalParameter,
		}
	}

	r := []*godwarf.Tree{
		m("size", t("uintptr"), regnum.AMD64_Rax, false),
		m("typ", t("*runtime._type"), regnum.AMD64_Rbx, false),
		m("needzero", t("bool"), regnum.AMD64_Rcx, false),
		m("~r1", t("unsafe.Pointer"), regnum.AMD64_Rax, true),
	}

	return r, err1
}
