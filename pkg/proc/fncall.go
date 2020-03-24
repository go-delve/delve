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
	"github.com/go-delve/delve/pkg/goversion"
	"github.com/go-delve/delve/pkg/logflags"
	"golang.org/x/arch/x86/x86asm"
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
	debugCallFunctionName        = "runtime.debugCallV1"
	maxArgFrameSize              = 65535
)

var (
	errFuncCallUnsupported        = errors.New("function calls not supported by this version of Go")
	errFuncCallUnsupportedBackend = errors.New("backend does not support function calls")
	errFuncCallInProgress         = errors.New("cannot call function while another function call is already in progress")
	errNotACallExpr               = errors.New("not a function call")
	errNoGoroutine                = errors.New("no goroutine selected")
	errGoroutineNotRunning        = errors.New("selected goroutine not running")
	errNotEnoughStack             = errors.New("not enough stack space")
	errTooManyArguments           = errors.New("too many arguments")
	errNotEnoughArguments         = errors.New("not enough arguments")
	errNoAddrUnsupported          = errors.New("arguments to a function call must have an address")
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

	dbgcallfn := bi.LookupFunc[debugCallFunctionName]
	if dbgcallfn == nil {
		return errFuncCallUnsupported
	}

	scope, err := GoroutineScope(g.Thread)
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
		continueCompleted,
		continueRequest,
	}

	go scope.EvalExpression(expr, retLoadCfg)

	contReq, ok := <-continueRequest
	if contReq.cont {
		return t.Continue()
	}

	return finishEvalExpressionWithCalls(t, g, contReq, ok)
}

func finishEvalExpressionWithCalls(t *Target, g *G, contReq continueRequest, ok bool) error {
	fncallLog("stashing return values for %d in thread=%d\n", g.ID, g.Thread.ThreadID())
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

	p := scope.callCtx.p
	bi := scope.BinInfo
	if !p.SupportsFunctionCalls() {
		return nil, errFuncCallUnsupportedBackend
	}

	dbgcallfn := bi.LookupFunc[debugCallFunctionName]
	if dbgcallfn == nil {
		return nil, errFuncCallUnsupported
	}

	// check that there are at least 256 bytes free on the stack
	regs, err := scope.g.Thread.Registers(true)
	if err != nil {
		return nil, err
	}
	regs = regs.Copy()
	if regs.SP()-256 <= scope.g.stacklo {
		return nil, errNotEnoughStack
	}
	_, err = regs.Get(int(x86asm.RAX))
	if err != nil {
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

	if err := callOP(bi, scope.g.Thread, regs, dbgcallfn.Entry); err != nil {
		return nil, err
	}
	// write the desired argument frame size at SP-(2*pointer_size) (the extra pointer is the saved PC)
	if err := writePointer(bi, scope.g.Thread, regs.SP()-3*uint64(bi.Arch.PtrSize()), uint64(fncall.argFrameSize)); err != nil {
		return nil, err
	}

	fncallLog("function call initiated %v frame size %d", fncall.fn, fncall.argFrameSize)

	spoff := int64(scope.Regs.Uint64Val(scope.Regs.SPRegNum)) - int64(scope.g.stackhi)
	bpoff := int64(scope.Regs.Uint64Val(scope.Regs.BPRegNum)) - int64(scope.g.stackhi)
	fboff := scope.Regs.FrameBase - int64(scope.g.stackhi)

	for {
		scope.g = scope.callCtx.doContinue()

		// adjust the value of registers inside scope
		for regnum := range scope.Regs.Regs {
			switch uint64(regnum) {
			case scope.Regs.PCRegNum, scope.Regs.SPRegNum, scope.Regs.BPRegNum:
				// leave these alone
			default:
				// every other register is dirty and unrecoverable
				scope.Regs.Regs[regnum] = nil
			}
		}

		scope.Regs.Regs[scope.Regs.SPRegNum].Uint64Val = uint64(spoff + int64(scope.g.stackhi))
		scope.Regs.Regs[scope.Regs.BPRegNum].Uint64Val = uint64(bpoff + int64(scope.g.stackhi))
		scope.Regs.FrameBase = fboff + int64(scope.g.stackhi)
		scope.Regs.CFA = scope.frameOffset + int64(scope.g.stackhi)

		finished := funcCallStep(scope, &fncall)
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
	return fmt.Sprintf("panic calling a function")
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
	_, err := mem.WriteMemory(uintptr(addr), ptrbuf)
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
	if err := thread.SetSP(sp); err != nil {
		return err
	}
	if err := writePointer(bi, thread, sp, regs.PC()); err != nil {
		return err
	}
	return thread.SetPC(callAddr)
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
	name  string
	typ   godwarf.Type
	off   int64
	isret bool
}

// funcCallEvalArgs evaluates the arguments of the function call, copying
// the into the argument frame starting at argFrameAddr.
func funcCallEvalArgs(scope *EvalScope, fncall *functionCallState, argFrameAddr uint64) error {
	if scope.g == nil {
		// this should never happen
		return errNoGoroutine
	}

	if fncall.receiver != nil {
		err := funcCallCopyOneArg(scope, fncall, fncall.receiver, &fncall.formalArgs[0], argFrameAddr)
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

		err = funcCallCopyOneArg(scope, fncall, actualArg, formalArg, argFrameAddr)
		if err != nil {
			return err
		}
	}

	return nil
}

func funcCallCopyOneArg(scope *EvalScope, fncall *functionCallState, actualArg *Variable, formalArg *funcCallArg, argFrameAddr uint64) error {
	if scope.callCtx.checkEscape {
		//TODO(aarzilli): only apply the escapeCheck to leaking parameters.
		if err := escapeCheck(actualArg, formalArg.name, scope.g); err != nil {
			return fmt.Errorf("cannot use %s as argument %s in function %s: %v", actualArg.Name, formalArg.name, fncall.fn.Name, err)
		}
	}

	//TODO(aarzilli): autmoatic wrapping in interfaces for cases not handled
	// by convertToEface.

	formalArgVar := newVariable(formalArg.name, uintptr(formalArg.off+int64(argFrameAddr)), formalArg.typ, scope.BinInfo, scope.Mem)
	if err := scope.setValue(formalArgVar, actualArg, actualArg.Name); err != nil {
		return err
	}

	return nil
}

func funcCallArgs(fn *Function, bi *BinaryInfo, includeRet bool) (argFrameSize int64, formalArgs []funcCallArg, err error) {
	const CFA = 0x1000

	dwarfTree, err := fn.cu.image.getDwarfTree(fn.offset)
	if err != nil {
		return 0, nil, fmt.Errorf("DWARF read error: %v", err)
	}

	varEntries := reader.Variables(dwarfTree, fn.Entry, int(^uint(0)>>1), false, true)

	trustArgOrder := bi.Producer() != "" && goversion.ProducerAfterOrEqual(bi.Producer(), 1, 12)

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
				return 0, nil, err
			}

			// With Go version 1.12 or later we can trust that the arguments appear
			// in the same order as declared, which means we can calculate their
			// address automatically.
			// With this we can call optimized functions (which sometimes do not have
			// an argument address, due to a compiler bug) as well as runtime
			// functions (which are always optimized).
			off = argFrameSize
			off = alignAddr(off, typ.Align())
		}

		if e := off + typ.Size(); e > argFrameSize {
			argFrameSize = e
		}

		if isret, _ := entry.Val(dwarf.AttrVarParam).(bool); !isret || includeRet {
			formalArgs = append(formalArgs, funcCallArg{name: argname, typ: typ, off: off, isret: isret})
		}
	}

	sort.Slice(formalArgs, func(i, j int) bool {
		return formalArgs[i].off < formalArgs[j].off
	})

	return argFrameSize, formalArgs, nil
}

// alignAddr rounds up addr to a multiple of align. Align must be a power of 2.
func alignAddr(addr, align int64) int64 {
	return (addr + int64(align-1)) &^ int64(align-1)
}

func escapeCheck(v *Variable, name string, g *G) error {
	switch v.Kind {
	case reflect.Ptr:
		var w *Variable
		if len(v.Children) == 1 {
			// this branch is here to support pointers constructed with typecasts from ints or the '&' operator
			w = &v.Children[0]
		} else {
			w = v.maybeDereference()
		}
		return escapeCheckPointer(w.Addr, name, g)
	case reflect.Chan, reflect.String, reflect.Slice:
		return escapeCheckPointer(v.Base, name, g)
	case reflect.Map:
		sv := v.clone()
		sv.RealType = resolveTypedef(&(v.RealType.(*godwarf.MapType).TypedefType))
		sv = sv.maybeDereference()
		return escapeCheckPointer(sv.Addr, name, g)
	case reflect.Struct:
		t := v.RealType.(*godwarf.StructType)
		for _, field := range t.Field {
			fv, _ := v.toField(field)
			if err := escapeCheck(fv, fmt.Sprintf("%s.%s", name, field.Name), g); err != nil {
				return err
			}
		}
	case reflect.Array:
		for i := int64(0); i < v.Len; i++ {
			sv, _ := v.sliceAccess(int(i))
			if err := escapeCheck(sv, fmt.Sprintf("%s[%d]", name, i), g); err != nil {
				return err
			}
		}
	case reflect.Func:
		if err := escapeCheckPointer(uintptr(v.funcvalAddr()), name, g); err != nil {
			return err
		}
	}

	return nil
}

func escapeCheckPointer(addr uintptr, name string, g *G) error {
	if uint64(addr) >= g.stacklo && uint64(addr) < g.stackhi {
		return fmt.Errorf("stack object passed to escaping pointer: %s", name)
	}
	return nil
}

const (
	debugCallAXPrecheckFailed   = 8
	debugCallAXCompleteCall     = 0
	debugCallAXReadReturn       = 1
	debugCallAXReadPanic        = 2
	debugCallAXRestoreRegisters = 16
)

// funcCallStep executes one step of the function call injection protocol.
func funcCallStep(callScope *EvalScope, fncall *functionCallState) bool {
	p := callScope.callCtx.p
	bi := p.BinInfo()

	thread := callScope.g.Thread
	regs, err := thread.Registers(false)
	if err != nil {
		fncall.err = err
		return true
	}
	regs = regs.Copy()

	rax, _ := regs.Get(int(x86asm.RAX))

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
		fncallLog("function call interrupt gid=%d thread=%d rax=%#x (PC=%#x in %s)", callScope.g.ID, thread.ThreadID(), rax, pc, fnname)
	}

	switch rax {
	case debugCallAXPrecheckFailed:
		// get error from top of the stack and return it to user
		errvar, err := readTopstackVariable(thread, regs, "string", loadFullValue)
		if err != nil {
			fncall.err = fmt.Errorf("could not get precheck error reason: %v", err)
			break
		}
		errvar.Name = "err"
		fncall.err = fmt.Errorf("%v", constant.StringVal(errvar.Value))

	case debugCallAXCompleteCall:
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
			thread.SetDX(fncall.closureAddr)
		}
		callOP(bi, thread, regs, fncall.fn.Entry)

		err := funcCallEvalArgs(callScope, fncall, regs.SP())
		if err != nil {
			// rolling back the call, note: this works because we called regs.Copy() above
			thread.SetSP(regs.SP())
			thread.SetPC(regs.PC())
			fncall.err = err
			fncall.lateCallFailure = true
			break
		}

	case debugCallAXRestoreRegisters:
		// runtime requests that we restore the registers (all except pc and sp),
		// this is also the last step of the function call protocol.
		pc, sp := regs.PC(), regs.SP()
		if err := thread.RestoreRegisters(fncall.savedRegs); err != nil {
			fncall.err = fmt.Errorf("could not restore registers: %v", err)
		}
		if err := thread.SetPC(pc); err != nil {
			fncall.err = fmt.Errorf("could not restore PC: %v", err)
		}
		if err := thread.SetSP(sp); err != nil {
			fncall.err = fmt.Errorf("could not restore SP: %v", err)
		}
		if err := stepInstructionOut(p, thread, debugCallFunctionName, debugCallFunctionName); err != nil {
			fncall.err = fmt.Errorf("could not step out of %s: %v", debugCallFunctionName, err)
		}
		return true

	case debugCallAXReadReturn:
		// read return arguments from stack
		if fncall.panicvar != nil || fncall.lateCallFailure {
			break
		}
		retScope, err := ThreadScope(thread)
		if err != nil {
			fncall.err = fmt.Errorf("could not get return values: %v", err)
			break
		}

		// pretend we are still inside the function we called
		fakeFunctionEntryScope(retScope, fncall.fn, int64(regs.SP()), regs.SP()-uint64(bi.Arch.PtrSize()))

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

	case debugCallAXReadPanic:
		// read panic value from stack
		fncall.panicvar, err = readTopstackVariable(thread, regs, "interface {}", callScope.callCtx.retLoadCfg)
		if err != nil {
			fncall.err = fmt.Errorf("could not get panic: %v", err)
			break
		}
		fncall.panicvar.Name = "~panic"

	default:
		// Got an unknown AX value, this is probably bad but the safest thing
		// possible is to ignore it and hope it didn't matter.
		fncallLog("unknown value of AX %#x", rax)
	}

	return false
}

func readTopstackVariable(thread Thread, regs Registers, typename string, loadCfg LoadConfig) (*Variable, error) {
	bi := thread.BinInfo()
	scope, err := ThreadScope(thread)
	if err != nil {
		return nil, err
	}
	typ, err := bi.findType(typename)
	if err != nil {
		return nil, err
	}
	v := newVariable("", uintptr(regs.SP()), typ, scope.BinInfo, scope.Mem)
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
	scope.Regs.Regs[scope.Regs.SPRegNum].Uint64Val = sp

	fn.cu.image.dwarfReader.Seek(fn.offset)
	e, err := fn.cu.image.dwarfReader.Next()
	if err != nil {
		return err
	}
	scope.Regs.FrameBase, _, _, _ = scope.BinInfo.Location(e, dwarf.AttrFrameBase, scope.PC, scope.Regs)
	return nil
}

func (fncall *functionCallState) returnValues() []*Variable {
	if fncall.panicvar != nil {
		return []*Variable{fncall.panicvar}
	}
	return fncall.retvars
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
	v.Base = uintptr(mallocv.Children[0].Addr)
	_, err = scope.Mem.WriteMemory(v.Base, []byte(constant.StringVal(v.Value)))
	return err
}

func isCallInjectionStop(loc *Location) bool {
	if loc.Fn == nil {
		return false
	}
	return strings.HasPrefix(loc.Fn.Name, debugCallFunctionNamePrefix1) || strings.HasPrefix(loc.Fn.Name, debugCallFunctionNamePrefix2)
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
		if !isCallInjectionStop(loc) {
			continue
		}

		g, err := GetG(thread)
		if err != nil {
			return done, fmt.Errorf("could not determine running goroutine for thread %#x currently executing the function call injection protocol: %v", thread.ThreadID(), err)
		}
		callinj := t.fncallForG[g.ID]
		if callinj == nil || callinj.continueCompleted == nil {
			return false, fmt.Errorf("could not recover call injection state for goroutine %d", g.ID)
		}
		fncallLog("step for injection on goroutine %d thread=%d (location %s)", g.ID, thread.ThreadID(), loc.Fn.Name)
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
