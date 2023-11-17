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
	"github.com/go-delve/delve/pkg/proc/evalop"
)

// This file implements the function call injection introduced in go1.11.
//
// The protocol is described in $GOROOT/src/runtime/asm_amd64.s in the
// comments for function runtime·debugCallVn.
//
// The main entry point is EvalExpressionWithCalls which will set up an
// evalStack object to evaluate the provided expression.
// This object can either finish immediately, if no function calls were
// needed, or return with callInjectionContinue set. When this happens
// EvalExpressionWithCalls will call Continue and return.
//
// The Continue loop will call evalStack.resume when it hits a breakpoint in
// the call injection protocol.
//
// The work of setting up the function call and executing the protocol is
// done by:
//
//  - evalop.CallInjectionStart
//  - evalop.CallInjectionSetTarget
//  - evalCallInjectionCopyArg
//  - evalCallInjectionComplete

const (
	debugCallFunctionNamePrefix1 = "debugCall"
	debugCallFunctionNamePrefix2 = "runtime.debugCall"
	maxDebugCallVersion          = 2
	maxArgFrameSize              = 65535
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
	// undoInjection is set after evalop.CallInjectionSetTarget runs and cleared by evalCallInjectionComplete
	// it contains information on how to undo a function call injection without running it
	undoInjection *undoInjection

	protocolReg   uint64
	debugCallName string
}

type undoInjection struct {
	oldpc, oldlr uint64
}

type callContext struct {
	grp *TargetGroup
	p   *Target

	// checkEscape is true if the escape check should be performed.
	// See service/api.DebuggerCommand.UnsafeCall in service/api/types.go.
	checkEscape bool

	// retLoadCfg is the load configuration used to load return values
	retLoadCfg LoadConfig

	// injectionThread is the thread to use for nested call injections if the
	// original injection goroutine isn't running (because we are in Go 1.15)
	injectionThread Thread

	// stacks is a slice of known goroutine stacks used to check for
	// inappropriate escapes
	stacks []stack
}

type callInjection struct {
	evalStack        *evalStack
	startThreadID    int
	endCallInjection func()
}

// EvalExpressionWithCalls is like EvalExpression but allows function calls in 'expr'.
// Because this can only be done in the current goroutine, unlike
// EvalExpression, EvalExpressionWithCalls is not a method of EvalScope.
func EvalExpressionWithCalls(grp *TargetGroup, g *G, expr string, retLoadCfg LoadConfig, checkEscape bool) error {
	t := grp.Selected
	bi := t.BinInfo()
	if !t.SupportsFunctionCalls() {
		return errFuncCallUnsupportedBackend
	}
	producer := bi.Producer()
	if producer == "" || !goversion.ProducerAfterOrEqual(bi.Producer(), 1, 12) {
		return errFuncCallUnsupported
	}

	// check that the target goroutine is running
	if g == nil {
		return errNoGoroutine
	}
	if g.Status != Grunning || g.Thread == nil {
		return errGoroutineNotRunning
	}

	if callinj := t.fncallForG[g.ID]; callinj != nil && callinj.evalStack != nil {
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

	scope.callCtx = &callContext{
		grp:         grp,
		p:           t,
		checkEscape: checkEscape,
		retLoadCfg:  retLoadCfg,
	}
	scope.loadCfg = &retLoadCfg

	endCallInjection, err := t.proc.StartCallInjection()
	if err != nil {
		return err
	}

	ops, err := evalop.Compile(scopeToEvalLookup{scope}, expr, true)
	if err != nil {
		return err
	}

	stack := &evalStack{}

	t.fncallForG[g.ID] = &callInjection{
		evalStack:        stack,
		startThreadID:    0,
		endCallInjection: endCallInjection,
	}

	stack.eval(scope, ops)
	if stack.callInjectionContinue {
		return grp.Continue()
	}

	return finishEvalExpressionWithCalls(t, g, stack)
}

func finishEvalExpressionWithCalls(t *Target, g *G, stack *evalStack) error {
	fncallLog("stashing return values for %d in thread=%d", g.ID, g.Thread.ThreadID())
	g.Thread.Common().CallReturn = true
	ret, err := stack.result(&stack.scope.callCtx.retLoadCfg)
	if err != nil {
		if fpe, ispanic := stack.err.(fncallPanicErr); ispanic {
			err = nil
			g.Thread.Common().returnValues = []*Variable{fpe.panicVar}
		}
	} else if ret == nil {
		g.Thread.Common().returnValues = nil
	} else if ret.Addr == 0 && ret.DwarfType == nil && ret.Kind == reflect.Invalid {
		// this is a variable returned by a function call with multiple return values
		r := make([]*Variable, len(ret.Children))
		for i := range ret.Children {
			r[i] = &ret.Children[i]
		}
		g.Thread.Common().returnValues = r
	} else {
		g.Thread.Common().returnValues = []*Variable{ret}
	}

	callinj := t.fncallForG[g.ID]
	for goid := range t.fncallForG {
		if t.fncallForG[goid] == callinj {
			delete(t.fncallForG, goid)
		}
	}
	callinj.evalStack = nil
	callinj.endCallInjection()
	return err
}

func (scope *EvalScope) evalCallInjectionStart(op *evalop.CallInjectionStart, stack *evalStack) {
	if scope.callCtx == nil {
		stack.err = evalop.ErrFuncCallNotAllowed
		return
	}
	thread := scope.g.Thread
	stacklo := scope.g.stack.lo
	if thread == nil {
		// We are doing a nested function call and using Go 1.15, the original
		// injection goroutine was suspended and now we are using a different
		// goroutine, evaluation still happened on the original goroutine but we
		// need to use a different thread to do the nested call injection.
		thread = scope.callCtx.injectionThread
		g2, err := GetG(thread)
		if err != nil {
			stack.err = err
			return
		}
		stacklo = g2.stack.lo
	}
	if thread == nil {
		stack.err = errGoroutineNotRunning
		return
	}

	p := scope.callCtx.p
	bi := scope.BinInfo
	if !p.SupportsFunctionCalls() {
		stack.err = errFuncCallUnsupportedBackend
		return
	}

	dbgcallfn, dbgcallversion := debugCallFunction(bi)
	if dbgcallfn == nil {
		stack.err = errFuncCallUnsupported
		return
	}

	// check that there are at least 256 bytes free on the stack
	regs, err := thread.Registers()
	if err != nil {
		stack.err = err
		return
	}
	regs, err = regs.Copy()
	if err != nil {
		stack.err = err
		return
	}
	if regs.SP()-bi.Arch.debugCallMinStackSize <= stacklo {
		stack.err = errNotEnoughStack
		return
	}
	protocolReg, ok := debugCallProtocolReg(bi.Arch.Name, dbgcallversion)
	if !ok {
		stack.err = errFuncCallUnsupported
		return
	}
	if bi.Arch.RegistersToDwarfRegisters(0, regs).Reg(protocolReg) == nil {
		stack.err = errFuncCallUnsupportedBackend
		return
	}

	fncall := functionCallState{
		expr:          op.Node,
		savedRegs:     regs,
		protocolReg:   protocolReg,
		debugCallName: dbgcallfn.Name,
	}

	if op.HasFunc {
		err = funcCallEvalFuncExpr(scope, stack, &fncall)
		if err != nil {
			stack.err = err
			return
		}
	}

	switch bi.Arch.Name {
	case "amd64":
		if err := callOP(bi, thread, regs, dbgcallfn.Entry); err != nil {
			stack.err = err
			return
		}
		// write the desired argument frame size at SP-(2*pointer_size) (the extra pointer is the saved PC)
		if err := writePointer(bi, scope.Mem, regs.SP()-3*uint64(bi.Arch.PtrSize()), uint64(fncall.argFrameSize)); err != nil {
			stack.err = err
			return
		}
	case "arm64", "ppc64le":
		// debugCallV2 on arm64 needs a special call sequence, callOP can not be used
		sp := regs.SP()
		var spOffset uint64
		if bi.Arch.Name == "arm64" {
			spOffset = 2 * uint64(bi.Arch.PtrSize())
		} else {
			spOffset = 4 * uint64(bi.Arch.PtrSize())
		}
		sp -= spOffset
		if err := setSP(thread, sp); err != nil {
			stack.err = err
			return
		}
		if err := writePointer(bi, scope.Mem, sp, regs.LR()); err != nil {
			stack.err = err
			return
		}
		if err := setLR(thread, regs.PC()); err != nil {
			stack.err = err
			return
		}
		if err := writePointer(bi, scope.Mem, sp-spOffset, uint64(fncall.argFrameSize)); err != nil {
			stack.err = err
			return
		}
		regs, err = thread.Registers()
		if err != nil {
			stack.err = err
			return
		}
		regs, err = regs.Copy()
		if err != nil {
			stack.err = err
			return
		}
		fncall.savedRegs = regs
		err = setPC(thread, dbgcallfn.Entry)
		if err != nil {
			stack.err = err
			return
		}
	}

	fncallLog("function call initiated %v frame size %d goroutine %d (thread %d)", fncall.fn, fncall.argFrameSize, scope.g.ID, thread.ThreadID())

	thread.Breakpoint().Clear() // since we moved address in PC the thread is no longer stopped at a breakpoint, leaving the breakpoint set will confuse Continue
	p.fncallForG[scope.g.ID].startThreadID = thread.ThreadID()

	stack.fncallPush(&fncall)
	stack.push(newConstant(constant.MakeBool(fncall.fn == nil || fncall.receiver != nil || fncall.closureAddr != 0), scope.Mem))
	stack.callInjectionContinue = true
}

func funcCallFinish(scope *EvalScope, stack *evalStack) {
	fncall := stack.fncallPop()
	if fncall.err != nil {
		if stack.err == nil {
			stack.err = fncall.err
		} else {
			fncallLog("additional fncall error: %v", fncall.err)
		}
		return
	}

	if fncall.panicvar != nil {
		if stack.err == nil {
			stack.err = fncallPanicErr{fncall.panicvar}
		} else {
			fncallLog("additional fncall panic: %v", fncall.panicvar)
		}
		return
	}
	switch len(fncall.retvars) {
	case 0:
		r := newVariable("", 0, nil, scope.BinInfo, nil)
		r.loaded = true
		r.Unreadable = errors.New("no return values")
		stack.push(r)
	case 1:
		stack.push(fncall.retvars[0])
	default:
		// create a fake variable without address or type to return multiple values
		r := newVariable("", 0, nil, scope.BinInfo, nil)
		r.loaded = true
		r.Children = make([]Variable, len(fncall.retvars))
		for i := range fncall.retvars {
			r.Children[i] = *fncall.retvars[i]
		}
		stack.push(r)
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
	switch bi.Arch.Name {
	case "amd64":
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
	case "arm64", "ppc64le":
		if err := setLR(thread, regs.PC()); err != nil {
			return err
		}
		return setPC(thread, callAddr)

	default:
		panic("not implemented")
	}
}

// funcCallEvalFuncExpr evaluates expr.Fun and returns the function that we're trying to call.
// If allowCalls is false function calls will be disabled even if scope.callCtx != nil
func funcCallEvalFuncExpr(scope *EvalScope, stack *evalStack, fncall *functionCallState) error {
	bi := scope.BinInfo

	fnvar := stack.peek()
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
	fncall.fn = bi.PCToFunc(fnvar.Base)
	if fncall.fn == nil {
		return fmt.Errorf("could not find DIE for function %q", exprToString(fncall.expr.Fun))
	}
	if !fncall.fn.cu.isgo {
		return errNotAGoFunction
	}
	fncall.closureAddr = fnvar.closureAddr

	var err error
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

func funcCallCopyOneArg(scope *EvalScope, fncall *functionCallState, actualArg *Variable, formalArg *funcCallArg, thread Thread) error {
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

	//TODO(aarzilli): automatic wrapping in interfaces for cases not handled
	// by convertToEface.

	formalScope, err := GoroutineScope(scope.target, thread)
	if err != nil {
		return err
	}

	var formalArgVar *Variable
	if formalArg.dwarfEntry != nil {
		var err error
		formalArgVar, err = extractVarInfoFromEntry(scope.target, formalScope.BinInfo, formalScope.image(), formalScope.Regs, formalScope.Mem, formalArg.dwarfEntry, 0)
		if err != nil {
			return err
		}
	} else {
		formalArgVar = newVariable(formalArg.name, uint64(formalArg.off+formalScope.Regs.CFA), formalArg.typ, scope.BinInfo, scope.Mem)
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

	if bi.regabi && fn.cu.optimized {
		if runtimeWhitelist[fn.Name] {
			runtimeOptimizedWorkaround(bi, fn.cu.image, dwarfTree)
		} else {
			// Debug info for function arguments on optimized functions is currently
			// too incomplete to attempt injecting calls to arbitrary optimized
			// functions.
			// Prior to regabi we could do this because the ABI was simple enough to
			// manually encode it in Delve.
			// Runtime.mallocgc is an exception, we specifically patch it's DIE to be
			// correct for call injection purposes.
			return 0, nil, fmt.Errorf("can not call optimized function %s when regabi is in use", fn.Name)
		}
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
			formalArg, err = funcCallArgOldABI(fn, bi, entry, argname, typ, &argFrameSize)
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
		// assignment algorithm Go uses can result in an amount of additional
		// space used due to alignment requirements, bounded by the number of argument registers.
		// Because we currently don't have an easy way to obtain the frame size,
		// let's be even more conservative.
		// A safe lower-bound on the size of the argument frame includes space for
		// each argument plus the total bytes of register arguments.
		// This is derived from worst-case alignment padding of up to
		// (pointer-word-bytes - 1) per argument passed in registers.
		// See: https://github.com/go-delve/delve/pull/2451#discussion_r665761531
		// TODO: Make this generic for other platforms.
		argFrameSize = alignAddr(argFrameSize, 8)
		argFrameSize += int64(bi.Arch.maxRegArgBytes)
	}

	sort.Slice(formalArgs, func(i, j int) bool {
		return formalArgs[i].off < formalArgs[j].off
	})

	return argFrameSize, formalArgs, nil
}

func funcCallArgOldABI(fn *Function, bi *BinaryInfo, entry reader.Variable, argname string, typ godwarf.Type, pargFrameSize *int64) (*funcCallArg, error) {
	const CFA = 0x1000
	var off int64

	locprog, _, err := bi.locationExpr(entry, dwarf.AttrLocation, fn.Entry)
	if err != nil {
		err = fmt.Errorf("could not get argument location of %s: %v", argname, err)
	} else {
		var pieces []op.Piece
		off, pieces, err = op.ExecuteStackProgram(op.DwarfRegisters{CFA: CFA, FrameBase: CFA}, locprog, bi.Arch.PtrSize(), nil)
		if err != nil {
			err = fmt.Errorf("unsupported location expression for argument %s: %v", argname, err)
		}
		if pieces != nil {
			err = fmt.Errorf("unsupported location expression for argument %s (uses DW_OP_piece)", argname)
		}
		off -= CFA
	}
	if err != nil {
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
	return (addr + align - 1) &^ (align - 1)
}

func escapeCheck(v *Variable, name string, stack stack) error {
	if v.Unreadable != nil {
		return fmt.Errorf("escape check for %s failed, variable unreadable: %v", name, v.Unreadable)
	}
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
	if addr >= stack.lo && addr < stack.hi {
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
func funcCallStep(callScope *EvalScope, stack *evalStack, thread Thread) bool {
	p := callScope.callCtx.p
	bi := p.BinInfo()
	fncall := stack.fncallPeek()

	regs, err := thread.Registers()
	if err != nil {
		fncall.err = err
		return true
	}

	regval := bi.Arch.RegistersToDwarfRegisters(0, regs).Uint64Val(fncall.protocolReg)

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
		fncallLog("function call interrupt gid=%d (original) thread=%d regval=%#x (PC=%#x in %s %s:%d)", callScope.g.ID, thread.ThreadID(), regval, pc, fnname, loc.File, loc.Line)
	}

	switch regval {
	case debugCallRegPrecheckFailed: // 8
		stack.callInjectionContinue = true
		archoff := uint64(0)
		if bi.Arch.Name == "arm64" {
			archoff = 8
		} else if bi.Arch.Name == "ppc64le" {
			archoff = 40
		}
		// get error from top of the stack and return it to user
		errvar, err := readStackVariable(p, thread, regs, archoff, "string", loadFullValue)
		if err != nil {
			fncall.err = fmt.Errorf("could not get precheck error reason: %v", err)
			break
		}
		errvar.Name = "err"
		fncall.err = fmt.Errorf("%v", constant.StringVal(errvar.Value))

	case debugCallRegCompleteCall: // 0
		p.fncallForG[callScope.g.ID].startThreadID = 0

	case debugCallRegRestoreRegisters: // 16
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
		fncallLog("stepping thread %d", thread.ThreadID())
		if err := stepInstructionOut(callScope.callCtx.grp, p, thread, fncall.debugCallName, fncall.debugCallName); err != nil {
			fncall.err = fmt.Errorf("could not step out of %s: %v", fncall.debugCallName, err)
		}
		if bi.Arch.Name == "amd64" {
			// The tail of debugCallV2 corrupts the state of RFLAGS, we must restore
			// it one extra time after stepping out of it.
			// See https://github.com/go-delve/delve/issues/2985 and
			// TestCallInjectionFlagCorruption
			rflags := bi.Arch.RegistersToDwarfRegisters(0, fncall.savedRegs).Uint64Val(regnum.AMD64_Rflags)
			err := thread.SetReg(regnum.AMD64_Rflags, op.DwarfRegisterFromUint64(rflags))
			if err != nil {
				fncall.err = fmt.Errorf("could not restore RFLAGS register: %v", err)
			}
		}
		return true

	case debugCallRegReadReturn: // 1
		// read return arguments from stack
		stack.callInjectionContinue = true
		if fncall.panicvar != nil || fncall.err != nil {
			break
		}
		retScope, err := ThreadScope(p, thread)
		if err != nil {
			fncall.err = fmt.Errorf("could not get return values: %v", err)
			break
		}

		// pretend we are still inside the function we called
		fakeFunctionEntryScope(retScope, fncall.fn, int64(regs.SP()), regs.SP()-uint64(bi.Arch.PtrSize()))
		var flags localsFlags
		flags |= localsNoDeclLineCheck // if the function we are calling is an autogenerated stub then declaration lines have no meaning
		if !bi.regabi {
			flags |= localsTrustArgOrder
		}

		fncall.retvars, err = retScope.Locals(flags)
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
		if bi.Arch.Name == "arm64" || bi.Arch.Name == "ppc64le" {
			oldlr, err := readUintRaw(thread.ProcessMemory(), regs.SP(), int64(bi.Arch.PtrSize()))
			if err != nil {
				fncall.err = fmt.Errorf("could not restore LR: %v", err)
				break
			}
			if err = setLR(thread, oldlr); err != nil {
				fncall.err = fmt.Errorf("could not restore LR: %v", err)
				break
			}
		}

	case debugCallRegReadPanic: // 2
		// read panic value from stack
		stack.callInjectionContinue = true
		archoff := uint64(0)
		if bi.Arch.Name == "arm64" {
			archoff = 8
		} else if bi.Arch.Name == "ppc64le" {
			archoff = 32
		}
		fncall.panicvar, err = readStackVariable(p, thread, regs, archoff, "interface {}", callScope.callCtx.retLoadCfg)
		if err != nil {
			fncall.err = fmt.Errorf("could not get panic: %v", err)
			break
		}
		fncall.panicvar.Name = "~panic"

	default:
		// Got an unknown protocol register value, this is probably bad but the safest thing
		// possible is to ignore it and hope it didn't matter.
		stack.callInjectionContinue = true
		fncallLog("unknown value of protocol register %#x", regval)
	}

	return false
}

func (scope *EvalScope) evalCallInjectionSetTarget(op *evalop.CallInjectionSetTarget, stack *evalStack, thread Thread) {
	fncall := stack.fncallPeek()
	if fncall.fn == nil || fncall.receiver != nil || fncall.closureAddr != 0 {
		funcCallEvalFuncExpr(scope, stack, fncall)
	}
	stack.pop() // target function, consumed by funcCallEvalFuncExpr either above or in evalop.CallInjectionStart

	regs, err := thread.Registers()
	if err != nil {
		stack.err = err
		return
	}

	if fncall.closureAddr != 0 {
		// When calling a function pointer we must set the DX register to the
		// address of the function pointer itself.
		setClosureReg(thread, fncall.closureAddr)
	}

	undo := new(undoInjection)
	undo.oldpc = regs.PC()
	if scope.BinInfo.Arch.Name == "arm64" || scope.BinInfo.Arch.Name == "ppc64le" {
		undo.oldlr = regs.LR()
	}
	callOP(scope.BinInfo, thread, regs, fncall.fn.Entry)

	fncall.undoInjection = undo

	if fncall.receiver != nil {
		err := funcCallCopyOneArg(scope, fncall, fncall.receiver, &fncall.formalArgs[0], thread)
		if err != nil {
			stack.err = err
			return
		}
		fncall.formalArgs = fncall.formalArgs[1:]
	}
}

func readStackVariable(t *Target, thread Thread, regs Registers, off uint64, typename string, loadCfg LoadConfig) (*Variable, error) {
	bi := thread.BinInfo()
	scope, err := ThreadScope(t, thread)
	if err != nil {
		return nil, err
	}
	typ, err := bi.findType(typename)
	if err != nil {
		return nil, err
	}
	v := newVariable("", regs.SP()+off, typ, scope.BinInfo, scope.Mem)
	v.loadValue(loadCfg)
	if v.Unreadable != nil {
		return nil, v.Unreadable
	}
	v.Flags |= VariableFakeAddress
	return v, nil
}

// fakeFunctionEntryScope alters scope to pretend that we are at the entry point of
// fn and CFA and SP are the ones passed as argument.
// This function is used to create a scope for a call frame that doesn't
// exist anymore, to read the return variables of an injected function call,
// or after a stepout command.
func fakeFunctionEntryScope(scope *EvalScope, fn *Function, cfa int64, sp uint64) error {
	scope.PC = fn.Entry
	scope.Fn = fn
	scope.File, scope.Line = scope.BinInfo.EntryLineForFunc(fn)
	scope.Regs.CFA = cfa
	scope.Regs.Reg(scope.Regs.SPRegNum).Uint64Val = sp
	scope.Regs.Reg(scope.Regs.PCRegNum).Uint64Val = fn.Entry

	fn.cu.image.dwarfReader.Seek(fn.offset)
	e, err := fn.cu.image.dwarfReader.Next()
	if err != nil {
		return err
	}
	scope.Regs.FrameBase, _, _, _ = scope.BinInfo.Location(e, dwarf.AttrFrameBase, scope.PC, scope.Regs, nil)
	return nil
}

func (scope *EvalScope) allocString(phase int, stack *evalStack, curthread Thread) bool {
	switch phase {
	case 0:
		x := stack.peek()
		if !(x.Kind == reflect.String && x.Addr == 0 && (x.Flags&VariableConstant) != 0 && x.Len > 0) {
			stack.opidx += 2 // skip the next two allocString phases, we don't need to do an allocation
			return false
		}
		if scope.callCtx == nil {
			// do not complain here, setValue will if no other errors happen
			stack.opidx += 2
			return false
		}
		mallocv, err := scope.findGlobal("runtime", "mallocgc")
		if mallocv == nil {
			stack.err = err
			return false
		}
		stack.push(mallocv)
		scope.evalCallInjectionStart(&evalop.CallInjectionStart{HasFunc: true, Node: &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X:   &ast.Ident{Name: "runtime"},
				Sel: &ast.Ident{Name: "mallocgc"},
			},
			Args: []ast.Expr{
				&ast.BasicLit{Kind: token.INT, Value: "0"},
				&ast.Ident{Name: "nil"},
				&ast.Ident{Name: "false"},
			},
		}}, stack)
		if stack.err == nil {
			stack.pop() // return value of evalop.CallInjectionStart
		}
		return true

	case 1:
		fncall := stack.fncallPeek()
		savedLoadCfg := scope.callCtx.retLoadCfg
		scope.callCtx.retLoadCfg = loadFullValue
		defer func() {
			scope.callCtx.retLoadCfg = savedLoadCfg
		}()

		scope.evalCallInjectionSetTarget(nil, stack, curthread)

		strvar := stack.peek()

		stack.err = funcCallCopyOneArg(scope, fncall, newConstant(constant.MakeInt64(strvar.Len), scope.Mem), &fncall.formalArgs[0], curthread)
		if stack.err != nil {
			return false
		}
		stack.err = funcCallCopyOneArg(scope, fncall, nilVariable, &fncall.formalArgs[1], curthread)
		if stack.err != nil {
			return false
		}
		stack.err = funcCallCopyOneArg(scope, fncall, newConstant(constant.MakeBool(false), scope.Mem), &fncall.formalArgs[2], curthread)
		if stack.err != nil {
			return false
		}
		return true

	case 2:
		mallocv := stack.pop()
		v := stack.pop()
		if mallocv.Unreadable != nil {
			stack.err = mallocv.Unreadable
			return false
		}

		if mallocv.DwarfType.String() != "*void" {
			stack.err = fmt.Errorf("unexpected return type for mallocgc call: %v", mallocv.DwarfType.String())
			return false
		}

		if len(mallocv.Children) != 1 {
			stack.err = errors.New("internal error, could not interpret return value of mallocgc call")
			return false
		}

		v.Base = mallocv.Children[0].Addr
		_, stack.err = scope.Mem.WriteMemory(v.Base, []byte(constant.StringVal(v.Value)))
		stack.push(v)
		return false
	}

	panic("unreachable")
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
	off := int64(0)
	if thread.BinInfo().Arch.breakInstrMovesPC {
		off = -int64(len(thread.BinInfo().Arch.breakpointInstruction))
	}
	text, err := disassembleCurrentInstruction(t, thread, off)
	if err != nil || len(text) == 0 {
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
	currentThread := t.currentThread
	defer func() {
		t.currentThread = currentThread
	}()
	for _, thread := range threads {
		loc, err := thread.Location()
		if err != nil {
			continue
		}
		if !isCallInjectionStop(t, thread, loc) {
			continue
		}

		regs, _ := thread.Registers()
		fncallLog("call injection found thread=%d %s %s:%d PC=%#x SP=%#x", thread.ThreadID(), loc.Fn.Name, loc.File, loc.Line, regs.PC(), regs.SP())

		g, callinj, err := findCallInjectionStateForThread(t, thread)
		if err != nil {
			return false, err
		}

		arch := thread.BinInfo().Arch
		if !arch.breakInstrMovesPC {
			setPC(thread, loc.PC+uint64(len(arch.breakpointInstruction)))
		}

		fncallLog("step for injection on goroutine %d (current) thread=%d (location %s)", g.ID, thread.ThreadID(), loc.Fn.Name)
		t.currentThread = thread
		callinj.evalStack.resume(g)
		if !callinj.evalStack.callInjectionContinue {
			err := finishEvalExpressionWithCalls(t, g, callinj.evalStack)
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
		if callinj.evalStack == nil {
			return nil, nil, notfound()
		}
		return g, callinj, nil
	}

	// In Go 1.15 and later the call injection protocol will switch to a
	// different goroutine.
	// Here we try to recover the injection goroutine by checking the injection
	// thread.

	for goid, callinj := range t.fncallForG {
		if callinj != nil && callinj.evalStack != nil && callinj.startThreadID != 0 && callinj.startThreadID == thread.ThreadID() {
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
		fn := bi.lookupOneFunc(name)
		if fn != nil {
			return fn, version
		}
	}
	return nil, 0
}

// debugCallProtocolReg returns the register ID (as defined in pkg/dwarf/regnum)
// of the register used in the debug call protocol, given the debug call version.
// Also returns a bool indicating whether the version is supported.
func debugCallProtocolReg(archName string, version int) (uint64, bool) {
	switch archName {
	case "amd64":
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
	case "arm64", "ppc64le":
		if version == 2 {
			return regnum.ARM64_X0 + 20, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// runtimeWhitelist is a list of functions in the runtime that we can call
// (through call injection) even if they are optimized.
var runtimeWhitelist = map[string]bool{
	"runtime.mallocgc": true,
}

// runtimeOptimizedWorkaround modifies the input DIE so that arguments and
// return variables have the appropriate registers for call injection.
// This function can not be called on arbitrary DIEs, it is only valid for
// the functions specified in runtimeWhitelist.
// In particular this will fail if any of the arguments of the function
// passed in input does not fit in an integer CPU register.
func runtimeOptimizedWorkaround(bi *BinaryInfo, image *Image, in *godwarf.Tree) {
	if image.workaroundCache == nil {
		image.workaroundCache = make(map[dwarf.Offset]*godwarf.Tree)
	}
	if image.workaroundCache[in.Offset] == in {
		return
	}
	image.workaroundCache[in.Offset] = in

	curArg, curRet := 0, 0
	for _, child := range in.Children {
		if child.Tag == dwarf.TagFormalParameter {
			childEntry, ok := child.Entry.(*dwarf.Entry)
			if !ok {
				panic("internal error: bad DIE for runtimeOptimizedWorkaround")
			}
			isret, _ := child.Entry.Val(dwarf.AttrVarParam).(bool)

			var reg int
			if isret {
				reg = bi.Arch.argumentRegs[curRet]
				curRet++
			} else {
				reg = bi.Arch.argumentRegs[curArg]
				curArg++
			}

			newlocfield := dwarf.Field{Attr: dwarf.AttrLocation, Val: []byte{byte(op.DW_OP_reg0) + byte(reg)}, Class: dwarf.ClassBlock}

			locfield := childEntry.AttrField(dwarf.AttrLocation)
			if locfield != nil {
				*locfield = newlocfield
			} else {
				childEntry.Field = append(childEntry.Field, newlocfield)
			}
		}
	}
}
