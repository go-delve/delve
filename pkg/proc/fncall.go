package proc

import (
	"debug/dwarf"
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"reflect"
	"sort"

	"github.com/derekparker/delve/pkg/dwarf/godwarf"
	"github.com/derekparker/delve/pkg/dwarf/op"
	"github.com/derekparker/delve/pkg/dwarf/reader"
	"github.com/derekparker/delve/pkg/logflags"
	"github.com/sirupsen/logrus"
	"golang.org/x/arch/x86/x86asm"
)

// This file implements the function call injection introduced in go1.11.
//
// The protocol is described in $GOROOT/src/runtime/asm_amd64.s in the
// comments for function runtime·debugCallV1.
//
// There are two main entry points here. The first one is CallFunction which
// evaluates a function call expression, sets up the function call on the
// selected goroutine and resumes execution of the process.
//
// The second one is (*FunctionCallState).step() which is called every time
// the process stops at a breakpoint inside one of the debug injcetion
// functions.

const (
	debugCallFunctionNamePrefix1 = "debugCall"
	debugCallFunctionNamePrefix2 = "runtime.debugCall"
	debugCallFunctionName        = "runtime.debugCallV1"
)

var (
	ErrFuncCallUnsupported        = errors.New("function calls not supported by this version of Go")
	ErrFuncCallUnsupportedBackend = errors.New("backend does not support function calls")
	ErrFuncCallInProgress         = errors.New("cannot call function while another function call is already in progress")
	ErrNotACallExpr               = errors.New("not a function call")
	ErrNoGoroutine                = errors.New("no goroutine selected")
	ErrGoroutineNotRunning        = errors.New("selected goroutine not running")
	ErrNotEnoughStack             = errors.New("not enough stack space")
	ErrTooManyArguments           = errors.New("too many arguments")
	ErrNotEnoughArguments         = errors.New("not enough arguments")
	ErrNoAddrUnsupported          = errors.New("arguments to a function call must have an address")
	ErrNotAGoFunction             = errors.New("not a Go function")
)

type functionCallState struct {
	// inProgress is true if a function call is in progress
	inProgress bool
	// finished is true if the function call terminated
	finished bool
	// savedRegs contains the saved registers
	savedRegs SavedRegisters
	// expr contains an expression describing the current function call
	expr string
	// err contains a saved error
	err error
	// fn is the function that is being called
	fn *Function
	// argmem contains the argument frame of this function call
	argmem []byte
	// retvars contains the return variables after the function call terminates without panic'ing
	retvars []*Variable
	// retLoadCfg is the load configuration used to load return values
	retLoadCfg *LoadConfig
	// panicvar is a variable used to store the value of the panic, if the
	// called function panics.
	panicvar *Variable
}

// CallFunction starts a debugger injected function call on the current thread of p.
// See runtime.debugCallV1 in $GOROOT/src/runtime/asm_amd64.s for a
// description of the protocol.
func CallFunction(p Process, expr string, retLoadCfg *LoadConfig) error {
	bi := p.BinInfo()
	if !p.Common().fncallEnabled {
		return ErrFuncCallUnsupportedBackend
	}
	fncall := &p.Common().fncallState
	if fncall.inProgress {
		return ErrFuncCallInProgress
	}

	*fncall = functionCallState{}

	dbgcallfn := bi.LookupFunc[debugCallFunctionName]
	if dbgcallfn == nil {
		return ErrFuncCallUnsupported
	}

	// check that the selected goroutine is running
	g := p.SelectedGoroutine()
	if g == nil {
		return ErrNoGoroutine
	}
	if g.Status != Grunning || g.Thread == nil {
		return ErrGoroutineNotRunning
	}

	// check that there are at least 256 bytes free on the stack
	regs, err := g.Thread.Registers(true)
	if err != nil {
		return err
	}
	if regs.SP()-256 <= g.stacklo {
		return ErrNotEnoughStack
	}
	_, err = regs.Get(int(x86asm.RAX))
	if err != nil {
		return ErrFuncCallUnsupportedBackend
	}

	fn, argvars, err := funcCallEvalExpr(p, expr)
	if err != nil {
		return err
	}

	argmem, err := funcCallArgFrame(fn, argvars, g, bi)
	if err != nil {
		return err
	}

	if err := callOP(bi, g.Thread, regs, dbgcallfn.Entry); err != nil {
		return err
	}
	// write the desired argument frame size at SP-(2*pointer_size) (the extra pointer is the saved PC)
	if err := writePointer(bi, g.Thread, regs.SP()-3*uint64(bi.Arch.PtrSize()), uint64(len(argmem))); err != nil {
		return err
	}

	fncall.inProgress = true
	fncall.savedRegs = regs.Save()
	fncall.expr = expr
	fncall.fn = fn
	fncall.argmem = argmem
	fncall.retLoadCfg = retLoadCfg

	fncallLog("function call initiated %v frame size %d\n", fn, len(argmem))

	return Continue(p)
}

func fncallLog(fmtstr string, args ...interface{}) {
	if !logflags.FnCall() {
		return
	}
	logrus.WithFields(logrus.Fields{"layer": "proc", "kind": "fncall"}).Debugf(fmtstr, args...)
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

// funcCallEvalExpr evaluates expr, which must be a function call, returns
// the function being called and its arguments.
func funcCallEvalExpr(p Process, expr string) (fn *Function, argvars []*Variable, err error) {
	bi := p.BinInfo()
	scope, err := GoroutineScope(p.CurrentThread())
	if err != nil {
		return nil, nil, err
	}

	t, err := parser.ParseExpr(expr)
	if err != nil {
		return nil, nil, err
	}
	callexpr, iscall := t.(*ast.CallExpr)
	if !iscall {
		return nil, nil, ErrNotACallExpr
	}

	//TODO(aarzilli): must evaluate <var>.<method> and treat them appropriately
	fnvar, err := scope.evalAST(callexpr.Fun)
	if err != nil {
		return nil, nil, err
	}
	if fnvar.Kind != reflect.Func {
		return nil, nil, fmt.Errorf("expression %q is not a function", exprToString(callexpr.Fun))
	}
	fn = bi.PCToFunc(uint64(fnvar.Base))
	if fn == nil {
		return nil, nil, fmt.Errorf("could not find DIE for function %q", exprToString(callexpr.Fun))
	}
	if !fn.cu.isgo {
		return nil, nil, ErrNotAGoFunction
	}

	argvars = make([]*Variable, len(callexpr.Args))
	for i := range callexpr.Args {
		argvars[i], err = scope.evalAST(callexpr.Args[i])
		if err != nil {
			return nil, nil, err
		}
		argvars[i].Name = exprToString(callexpr.Args[i])
	}

	return fn, argvars, nil
}

type funcCallArg struct {
	name string
	typ  godwarf.Type
	off  int64
}

// funcCallArgFrame checks type and pointer escaping for the arguments and
// returns the argument frame.
func funcCallArgFrame(fn *Function, actualArgs []*Variable, g *G, bi *BinaryInfo) (argmem []byte, err error) {
	const CFA = 0x1000
	vrdr := reader.Variables(bi.dwarf, fn.offset, fn.Entry, int(^uint(0)>>1), false)
	formalArgs := []funcCallArg{}

	// typechecks arguments, calculates argument frame size
	argFrameSize := int64(0)
	for vrdr.Next() {
		e := vrdr.Entry()
		if e.Tag != dwarf.TagFormalParameter {
			continue
		}
		entry, argname, typ, err := readVarEntry(e, bi)
		if err != nil {
			return nil, err
		}
		typ = resolveTypedef(typ)
		locprog, ok := entry.Val(dwarf.AttrLocation).([]byte)
		if !ok {
			return nil, fmt.Errorf("unsupported location expression for argument %s", argname)
		}
		off, _, err := op.ExecuteStackProgram(op.DwarfRegisters{CFA: CFA, FrameBase: CFA}, locprog)
		if err != nil {
			return nil, fmt.Errorf("unsupported location expression for argument %s: %v", argname, err)
		}

		off -= CFA

		if e := off + typ.Size(); e > argFrameSize {
			argFrameSize = e
		}

		if isret, _ := entry.Val(dwarf.AttrVarParam).(bool); !isret {
			formalArgs = append(formalArgs, funcCallArg{name: argname, typ: typ, off: off})
		}
	}
	if err := vrdr.Err(); err != nil {
		return nil, fmt.Errorf("DWARF read error: %v", err)
	}
	if len(actualArgs) > len(formalArgs) {
		return nil, ErrTooManyArguments
	}
	if len(actualArgs) < len(formalArgs) {
		return nil, ErrNotEnoughArguments
	}

	sort.Slice(formalArgs, func(i, j int) bool {
		return formalArgs[i].off < formalArgs[j].off
	})

	// constructs arguments frame
	argmem = make([]byte, argFrameSize)
	for i := range formalArgs {
		formalArg := &formalArgs[i]
		actualArg := actualArgs[i]

		if actualArg.Addr == 0 {
			//TODO(aarzilli): at least some of this needs to be supported
			return nil, ErrNoAddrUnsupported
		}

		if actualArg.RealType != formalArg.typ {
			return nil, fmt.Errorf("cannot use %s (type %s) as type %s in argument to %s", actualArg.Name, actualArg.DwarfType.String(), formalArg.typ.String(), fn.Name)
		}

		//TODO(aarzilli): only apply the escapeCheck to leaking parameters.
		if err := escapeCheck(actualArg, formalArg.name, g); err != nil {
			return nil, fmt.Errorf("can not pass %s to %s: %v", actualArg.Name, formalArg.name, err)
		}

		//TODO(aarzilli): automatic type conversions
		//TODO(aarzilli): automatic wrapping in interfaces?

		buf := make([]byte, actualArg.RealType.Size())
		sz, err := actualArg.mem.ReadMemory(buf, actualArg.Addr)
		if err != nil {
			return nil, fmt.Errorf("could not read argument %s: %v", actualArg.Name, err)
		}
		if int64(sz) != actualArg.RealType.Size() {
			return nil, fmt.Errorf("short read for argument %s: %d != %d %x", actualArg.Name, sz, actualArg.RealType.Size(), buf)
		}

		copy(argmem[formalArg.off:], buf)
	}

	return argmem, nil
}

func escapeCheck(v *Variable, name string, g *G) error {
	switch v.Kind {
	case reflect.Ptr:
		w := v.maybeDereference()
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
		//TODO(aarzilli): check closure argument?
	}

	return nil
}

func escapeCheckPointer(addr uintptr, name string, g *G) error {
	if uint64(addr) >= g.stacklo && uint64(addr) < g.stackhi {
		return fmt.Errorf("stack object passed to escaping pointer %s", name)
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

func (fncall *functionCallState) step(p Process) {
	bi := p.BinInfo()

	thread := p.CurrentThread()
	regs, err := thread.Registers(false)
	if err != nil {
		fncall.err = err
		fncall.finished = true
		fncall.inProgress = false
		return
	}

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
		fncallLog("function call interrupt rax=%#x (PC=%#x in %s)\n", rax, pc, fnname)
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
		// write arguments to the stack, call final function
		n, err := thread.WriteMemory(uintptr(regs.SP()), fncall.argmem)
		if err != nil {
			fncall.err = fmt.Errorf("could not write arguments: %v", err)
		}
		if n != len(fncall.argmem) {
			fncall.err = fmt.Errorf("short argument write: %d %d", n, len(fncall.argmem))
		}
		//TODO(aarzilli): if fncall.fn is a function closure CX needs to be set here
		callOP(bi, thread, regs, fncall.fn.Entry)

	case debugCallAXRestoreRegisters:
		// runtime requests that we restore the registers (all except pc and sp),
		// this is also the last step of the function call protocol.
		fncall.finished = true
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

	case debugCallAXReadReturn:
		// read return arguments from stack
		if fncall.retLoadCfg == nil || fncall.panicvar != nil {
			break
		}
		scope, err := ThreadScope(thread)
		if err != nil {
			fncall.err = fmt.Errorf("could not get return values: %v", err)
			break
		}

		// pretend we are still inside the function we called
		fakeFunctionEntryScope(scope, fncall.fn, int64(regs.SP()), regs.SP()-uint64(bi.Arch.PtrSize()))

		fncall.retvars, err = scope.Locals()
		if err != nil {
			fncall.err = fmt.Errorf("could not get return values: %v", err)
			break
		}
		fncall.retvars = filterVariables(fncall.retvars, func(v *Variable) bool {
			return (v.Flags & VariableReturnArgument) != 0
		})

		loadValues(fncall.retvars, *fncall.retLoadCfg)

	case debugCallAXReadPanic:
		// read panic value from stack
		if fncall.retLoadCfg == nil {
			return
		}
		fncall.panicvar, err = readTopstackVariable(thread, regs, "interface {}", *fncall.retLoadCfg)
		if err != nil {
			fncall.err = fmt.Errorf("could not get panic: %v", err)
			break
		}
		fncall.panicvar.Name = "~panic"
		fncall.panicvar.loadValue(*fncall.retLoadCfg)
		if fncall.panicvar.Unreadable != nil {
			fncall.err = fmt.Errorf("could not get panic: %v", fncall.panicvar.Unreadable)
			break
		}

	default:
		// Got an unknown AX value, this is probably bad but the safest thing
		// possible is to ignore it and hope it didn't matter.
		fncallLog("unknown value of AX %#x", rax)
	}
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
	v := scope.newVariable("", uintptr(regs.SP()), typ, scope.Mem)
	v.loadValue(loadCfg)
	if v.Unreadable != nil {
		return nil, v.Unreadable
	}
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

	scope.BinInfo.dwarfReader.Seek(fn.offset)
	e, err := scope.BinInfo.dwarfReader.Next()
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
