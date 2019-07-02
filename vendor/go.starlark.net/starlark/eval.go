// Copyright 2017 The Bazel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package starlark

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"math/big"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.starlark.net/internal/compile"
	"go.starlark.net/internal/spell"
	"go.starlark.net/resolve"
	"go.starlark.net/syntax"
)

// A Thread contains the state of a Starlark thread,
// such as its call stack and thread-local storage.
// The Thread is threaded throughout the evaluator.
type Thread struct {
	// Name is an optional name that describes the thread, for debugging.
	Name string

	// frame is the current Starlark execution frame.
	frame *Frame

	// Print is the client-supplied implementation of the Starlark
	// 'print' function. If nil, fmt.Fprintln(os.Stderr, msg) is
	// used instead.
	Print func(thread *Thread, msg string)

	// Load is the client-supplied implementation of module loading.
	// Repeated calls with the same module name must return the same
	// module environment or error.
	// The error message need not include the module name.
	//
	// See example_test.go for some example implementations of Load.
	Load func(thread *Thread, module string) (StringDict, error)

	// locals holds arbitrary "thread-local" Go values belonging to the client.
	// They are accessible to the client but not to any Starlark program.
	locals map[string]interface{}
}

// SetLocal sets the thread-local value associated with the specified key.
// It must not be called after execution begins.
func (thread *Thread) SetLocal(key string, value interface{}) {
	if thread.locals == nil {
		thread.locals = make(map[string]interface{})
	}
	thread.locals[key] = value
}

// Local returns the thread-local value associated with the specified key.
func (thread *Thread) Local(key string) interface{} {
	return thread.locals[key]
}

// Caller returns the frame of the caller of the current function.
// It should only be used in built-ins called from Starlark code.
func (thread *Thread) Caller() *Frame { return thread.frame.parent }

// TopFrame returns the topmost stack frame.
func (thread *Thread) TopFrame() *Frame { return thread.frame }

// A StringDict is a mapping from names to values, and represents
// an environment such as the global variables of a module.
// It is not a true starlark.Value.
type StringDict map[string]Value

// Keys returns a new sorted slice of d's keys.
func (d StringDict) Keys() []string {
	names := make([]string, 0, len(d))
	for name := range d {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (d StringDict) String() string {
	var buf bytes.Buffer
	buf.WriteByte('{')
	sep := ""
	for _, name := range d.Keys() {
		buf.WriteString(sep)
		buf.WriteString(name)
		buf.WriteString(": ")
		writeValue(&buf, d[name], nil)
		sep = ", "
	}
	buf.WriteByte('}')
	return buf.String()
}

func (d StringDict) Freeze() {
	for _, v := range d {
		v.Freeze()
	}
}

// Has reports whether the dictionary contains the specified key.
func (d StringDict) Has(key string) bool { _, ok := d[key]; return ok }

// A Frame records a call to a Starlark function (including module toplevel)
// or a built-in function or method.
type Frame struct {
	parent   *Frame          // caller's frame (or nil)
	callable Callable        // current function (or toplevel) or built-in
	posn     syntax.Position // source position of PC, set during error
	callpc   uint32          // PC of position of active call, set during call
	locals   []Value         // local variables, for debugger
}

// NewFrame returns a new frame with the specified parent and callable.
//
// It may be used to synthesize stack frames for error messages.
// Few clients should need to use this function.
func NewFrame(parent *Frame, callable Callable) *Frame {
	return &Frame{parent: parent, callable: callable}
}

// The Frames of a thread are structured as a spaghetti stack, not a
// slice, so that an EvalError can copy a stack efficiently and immutably.
// In hindsight using a slice would have led to a more convenient API.

func (fr *Frame) errorf(posn syntax.Position, format string, args ...interface{}) *EvalError {
	fr.posn = posn
	msg := fmt.Sprintf(format, args...)
	return &EvalError{Msg: msg, Frame: fr}
}

// Position returns the source position of the current point of execution in this frame.
func (fr *Frame) Position() syntax.Position {
	if fr.posn.IsValid() {
		return fr.posn // leaf frame only (the error)
	}
	switch c := fr.callable.(type) {
	case *Function:
		// Starlark function
		return c.funcode.Position(fr.callpc) // position of active call
	case interface{ Position() syntax.Position }:
		// If a built-in Callable defines
		// a Position method, use it.
		return c.Position()
	}
	return syntax.MakePosition(&builtinFilename, 1, 0)
}

var builtinFilename = "<builtin>"

// Function returns the frame's function or built-in.
func (fr *Frame) Callable() Callable { return fr.callable }

// Parent returns the frame of the enclosing function call, if any.
func (fr *Frame) Parent() *Frame { return fr.parent }

// An EvalError is a Starlark evaluation error and its associated call stack.
type EvalError struct {
	Msg   string
	Frame *Frame
}

func (e *EvalError) Error() string { return e.Msg }

// Backtrace returns a user-friendly error message describing the stack
// of calls that led to this error.
func (e *EvalError) Backtrace() string {
	var buf bytes.Buffer
	e.Frame.WriteBacktrace(&buf)
	fmt.Fprintf(&buf, "Error: %s", e.Msg)
	return buf.String()
}

// WriteBacktrace writes a user-friendly description of the stack to buf.
func (fr *Frame) WriteBacktrace(out *bytes.Buffer) {
	fmt.Fprintf(out, "Traceback (most recent call last):\n")
	var print func(fr *Frame)
	print = func(fr *Frame) {
		if fr != nil {
			print(fr.parent)
			fmt.Fprintf(out, "  %s: in %s\n", fr.Position(), fr.Callable().Name())
		}
	}
	print(fr)
}

// Stack returns the stack of frames, innermost first.
func (e *EvalError) Stack() []*Frame {
	var stack []*Frame
	for fr := e.Frame; fr != nil; fr = fr.parent {
		stack = append(stack, fr)
	}
	return stack
}

// A Program is a compiled Starlark program.
//
// Programs are immutable, and contain no Values.
// A Program may be created by parsing a source file (see SourceProgram)
// or by loading a previously saved compiled program (see CompiledProgram).
type Program struct {
	compiled *compile.Program
}

// CompilerVersion is the version number of the protocol for compiled
// files. Applications must not run programs compiled by one version
// with an interpreter at another version, and should thus incorporate
// the compiler version into the cache key when reusing compiled code.
const CompilerVersion = compile.Version

// Filename returns the name of the file from which this program was loaded.
func (prog *Program) Filename() string { return prog.compiled.Toplevel.Pos.Filename() }

func (prog *Program) String() string { return prog.Filename() }

// NumLoads returns the number of load statements in the compiled program.
func (prog *Program) NumLoads() int { return len(prog.compiled.Loads) }

// Load(i) returns the name and position of the i'th module directly
// loaded by this one, where 0 <= i < NumLoads().
// The name is unresolved---exactly as it appears in the source.
func (prog *Program) Load(i int) (string, syntax.Position) {
	id := prog.compiled.Loads[i]
	return id.Name, id.Pos
}

// WriteTo writes the compiled module to the specified output stream.
func (prog *Program) Write(out io.Writer) error {
	data := prog.compiled.Encode()
	_, err := out.Write(data)
	return err
}

// ExecFile parses, resolves, and executes a Starlark file in the
// specified global environment, which may be modified during execution.
//
// Thread is the state associated with the Starlark thread.
//
// The filename and src parameters are as for syntax.Parse:
// filename is the name of the file to execute,
// and the name that appears in error messages;
// src is an optional source of bytes to use
// instead of filename.
//
// predeclared defines the predeclared names specific to this module.
// Execution does not modify this dictionary, though it may mutate
// its values.
//
// If ExecFile fails during evaluation, it returns an *EvalError
// containing a backtrace.
func ExecFile(thread *Thread, filename string, src interface{}, predeclared StringDict) (StringDict, error) {
	// Parse, resolve, and compile a Starlark source file.
	_, mod, err := SourceProgram(filename, src, predeclared.Has)
	if err != nil {
		return nil, err
	}

	g, err := mod.Init(thread, predeclared)
	g.Freeze()
	return g, err
}

// SourceProgram produces a new program by parsing, resolving,
// and compiling a Starlark source file.
// On success, it returns the parsed file and the compiled program.
// The filename and src parameters are as for syntax.Parse.
//
// The isPredeclared predicate reports whether a name is
// a pre-declared identifier of the current module.
// Its typical value is predeclared.Has,
// where predeclared is a StringDict of pre-declared values.
func SourceProgram(filename string, src interface{}, isPredeclared func(string) bool) (*syntax.File, *Program, error) {
	f, err := syntax.Parse(filename, src, 0)
	if err != nil {
		return nil, nil, err
	}
	prog, err := FileProgram(f, isPredeclared)
	return f, prog, err
}

// FileProgram produces a new program by resolving,
// and compiling the Starlark source file syntax tree.
// On success, it returns the compiled program.
//
// Resolving a syntax tree mutates it.
// Do not call FileProgram more than once on the same file.
//
// The isPredeclared predicate reports whether a name is
// a pre-declared identifier of the current module.
// Its typical value is predeclared.Has,
// where predeclared is a StringDict of pre-declared values.
func FileProgram(f *syntax.File, isPredeclared func(string) bool) (*Program, error) {
	if err := resolve.File(f, isPredeclared, Universe.Has); err != nil {
		return nil, err
	}

	var pos syntax.Position
	if len(f.Stmts) > 0 {
		pos = syntax.Start(f.Stmts[0])
	} else {
		pos = syntax.MakePosition(&f.Path, 1, 1)
	}

	compiled := compile.File(f.Stmts, pos, "<toplevel>", f.Locals, f.Globals)

	return &Program{compiled}, nil
}

// CompiledProgram produces a new program from the representation
// of a compiled program previously saved by Program.Write.
func CompiledProgram(in io.Reader) (*Program, error) {
	data, err := ioutil.ReadAll(in)
	if err != nil {
		return nil, err
	}
	compiled, err := compile.DecodeProgram(data)
	if err != nil {
		return nil, err
	}
	return &Program{compiled}, nil
}

// Init creates a set of global variables for the program,
// executes the toplevel code of the specified program,
// and returns a new, unfrozen dictionary of the globals.
func (prog *Program) Init(thread *Thread, predeclared StringDict) (StringDict, error) {
	toplevel := makeToplevelFunction(prog.compiled.Toplevel, predeclared)

	_, err := Call(thread, toplevel, nil, nil)

	// Convert the global environment to a map.
	// We return a (partial) map even in case of error.
	return toplevel.Globals(), err
}

func makeToplevelFunction(funcode *compile.Funcode, predeclared StringDict) *Function {
	// Create the Starlark value denoted by each program constant c.
	constants := make([]Value, len(funcode.Prog.Constants))
	for i, c := range funcode.Prog.Constants {
		var v Value
		switch c := c.(type) {
		case int64:
			v = MakeInt64(c)
		case *big.Int:
			v = MakeBigInt(c)
		case string:
			v = String(c)
		case float64:
			v = Float(c)
		default:
			log.Fatalf("unexpected constant %T: %v", c, c)
		}
		constants[i] = v
	}

	return &Function{
		funcode:     funcode,
		predeclared: predeclared,
		globals:     make([]Value, len(funcode.Prog.Globals)),
		constants:   constants,
	}
}

// Eval parses, resolves, and evaluates an expression within the
// specified (predeclared) environment.
//
// Evaluation cannot mutate the environment dictionary itself,
// though it may modify variables reachable from the dictionary.
//
// The filename and src parameters are as for syntax.Parse.
//
// If Eval fails during evaluation, it returns an *EvalError
// containing a backtrace.
func Eval(thread *Thread, filename string, src interface{}, env StringDict) (Value, error) {
	expr, err := syntax.ParseExpr(filename, src, 0)
	if err != nil {
		return nil, err
	}
	f, err := makeExprFunc(expr, env)
	if err != nil {
		return nil, err
	}
	return Call(thread, f, nil, nil)
}

// EvalExpr resolves and evaluates an expression within the
// specified (predeclared) environment.
//
// Resolving an expression mutates it.
// Do not call EvalExpr more than once for the same expression.
//
// Evaluation cannot mutate the environment dictionary itself,
// though it may modify variables reachable from the dictionary.
//
// If Eval fails during evaluation, it returns an *EvalError
// containing a backtrace.
func EvalExpr(thread *Thread, expr syntax.Expr, env StringDict) (Value, error) {
	fn, err := makeExprFunc(expr, env)
	if err != nil {
		return nil, err
	}
	return Call(thread, fn, nil, nil)
}

// ExprFunc returns a no-argument function
// that evaluates the expression whose source is src.
func ExprFunc(filename string, src interface{}, env StringDict) (*Function, error) {
	expr, err := syntax.ParseExpr(filename, src, 0)
	if err != nil {
		return nil, err
	}
	return makeExprFunc(expr, env)
}

// makeExprFunc returns a no-argument function whose body is expr.
func makeExprFunc(expr syntax.Expr, env StringDict) (*Function, error) {
	locals, err := resolve.Expr(expr, env.Has, Universe.Has)
	if err != nil {
		return nil, err
	}

	return makeToplevelFunction(compile.Expr(expr, "<expr>", locals), env), nil
}

// The following functions are primitive operations of the byte code interpreter.

// list += iterable
func listExtend(x *List, y Iterable) {
	if ylist, ok := y.(*List); ok {
		// fast path: list += list
		x.elems = append(x.elems, ylist.elems...)
	} else {
		iter := y.Iterate()
		defer iter.Done()
		var z Value
		for iter.Next(&z) {
			x.elems = append(x.elems, z)
		}
	}
}

// getAttr implements x.dot.
func getAttr(x Value, name string) (Value, error) {
	hasAttr, ok := x.(HasAttrs)
	if !ok {
		return nil, fmt.Errorf("%s has no .%s field or method", x.Type(), name)
	}

	var errmsg string
	v, err := hasAttr.Attr(name)
	if err == nil {
		if v != nil {
			return v, nil // success
		}
		// (nil, nil) => generic error
		errmsg = fmt.Sprintf("%s has no .%s field or method", x.Type(), name)
	} else if nsa, ok := err.(NoSuchAttrError); ok {
		errmsg = string(nsa)
	} else {
		return nil, err // return error as is
	}

	// add spelling hint
	if n := spell.Nearest(name, hasAttr.AttrNames()); n != "" {
		errmsg = fmt.Sprintf("%s (did you mean .%s?)", errmsg, n)
	}

	return nil, fmt.Errorf("%s", errmsg)
}

// setField implements x.name = y.
func setField(x Value, name string, y Value) error {
	if x, ok := x.(HasSetField); ok {
		err := x.SetField(name, y)
		if _, ok := err.(NoSuchAttrError); ok {
			// No such field: check spelling.
			if n := spell.Nearest(name, x.AttrNames()); n != "" {
				err = fmt.Errorf("%s (did you mean .%s?)", err, n)
			}
		}
		return err
	}

	return fmt.Errorf("can't assign to .%s field of %s", name, x.Type())
}

// getIndex implements x[y].
func getIndex(x, y Value) (Value, error) {
	switch x := x.(type) {
	case Mapping: // dict
		z, found, err := x.Get(y)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("key %v not in %s", y, x.Type())
		}
		return z, nil

	case Indexable: // string, list, tuple
		n := x.Len()
		i, err := AsInt32(y)
		if err != nil {
			return nil, fmt.Errorf("%s index: %s", x.Type(), err)
		}
		origI := i
		if i < 0 {
			i += n
		}
		if i < 0 || i >= n {
			return nil, outOfRange(origI, n, x)
		}
		return x.Index(i), nil
	}
	return nil, fmt.Errorf("unhandled index operation %s[%s]", x.Type(), y.Type())
}

func outOfRange(i, n int, x Value) error {
	if n == 0 {
		return fmt.Errorf("index %d out of range: empty %s", i, x.Type())
	} else {
		return fmt.Errorf("%s index %d out of range [%d:%d]", x.Type(), i, -n, n-1)
	}
}

// setIndex implements x[y] = z.
func setIndex(x, y, z Value) error {
	switch x := x.(type) {
	case HasSetKey:
		if err := x.SetKey(y, z); err != nil {
			return err
		}

	case HasSetIndex:
		n := x.Len()
		i, err := AsInt32(y)
		if err != nil {
			return err
		}
		origI := i
		if i < 0 {
			i += n
		}
		if i < 0 || i >= n {
			return outOfRange(origI, n, x)
		}
		return x.SetIndex(i, z)

	default:
		return fmt.Errorf("%s value does not support item assignment", x.Type())
	}
	return nil
}

// Unary applies a unary operator (+, -, ~, not) to its operand.
func Unary(op syntax.Token, x Value) (Value, error) {
	// The NOT operator is not customizable.
	if op == syntax.NOT {
		return !x.Truth(), nil
	}

	// Int, Float, and user-defined types
	if x, ok := x.(HasUnary); ok {
		// (nil, nil) => unhandled
		y, err := x.Unary(op)
		if y != nil || err != nil {
			return y, err
		}
	}

	return nil, fmt.Errorf("unknown unary op: %s %s", op, x.Type())
}

// Binary applies a strict binary operator (not AND or OR) to its operands.
// For equality tests or ordered comparisons, use Compare instead.
func Binary(op syntax.Token, x, y Value) (Value, error) {
	switch op {
	case syntax.PLUS:
		switch x := x.(type) {
		case String:
			if y, ok := y.(String); ok {
				return x + y, nil
			}
		case Int:
			switch y := y.(type) {
			case Int:
				return x.Add(y), nil
			case Float:
				return x.Float() + y, nil
			}
		case Float:
			switch y := y.(type) {
			case Float:
				return x + y, nil
			case Int:
				return x + y.Float(), nil
			}
		case *List:
			if y, ok := y.(*List); ok {
				z := make([]Value, 0, x.Len()+y.Len())
				z = append(z, x.elems...)
				z = append(z, y.elems...)
				return NewList(z), nil
			}
		case Tuple:
			if y, ok := y.(Tuple); ok {
				z := make(Tuple, 0, len(x)+len(y))
				z = append(z, x...)
				z = append(z, y...)
				return z, nil
			}
		}

	case syntax.MINUS:
		switch x := x.(type) {
		case Int:
			switch y := y.(type) {
			case Int:
				return x.Sub(y), nil
			case Float:
				return x.Float() - y, nil
			}
		case Float:
			switch y := y.(type) {
			case Float:
				return x - y, nil
			case Int:
				return x - y.Float(), nil
			}
		}

	case syntax.STAR:
		switch x := x.(type) {
		case Int:
			switch y := y.(type) {
			case Int:
				return x.Mul(y), nil
			case Float:
				return x.Float() * y, nil
			case String:
				return stringRepeat(y, x)
			case *List:
				elems, err := tupleRepeat(Tuple(y.elems), x)
				if err != nil {
					return nil, err
				}
				return NewList(elems), nil
			case Tuple:
				return tupleRepeat(y, x)
			}
		case Float:
			switch y := y.(type) {
			case Float:
				return x * y, nil
			case Int:
				return x * y.Float(), nil
			}
		case String:
			if y, ok := y.(Int); ok {
				return stringRepeat(x, y)
			}
		case *List:
			if y, ok := y.(Int); ok {
				elems, err := tupleRepeat(Tuple(x.elems), y)
				if err != nil {
					return nil, err
				}
				return NewList(elems), nil
			}
		case Tuple:
			if y, ok := y.(Int); ok {
				return tupleRepeat(x, y)
			}

		}

	case syntax.SLASH:
		switch x := x.(type) {
		case Int:
			switch y := y.(type) {
			case Int:
				yf := y.Float()
				if yf == 0.0 {
					return nil, fmt.Errorf("real division by zero")
				}
				return x.Float() / yf, nil
			case Float:
				if y == 0.0 {
					return nil, fmt.Errorf("real division by zero")
				}
				return x.Float() / y, nil
			}
		case Float:
			switch y := y.(type) {
			case Float:
				if y == 0.0 {
					return nil, fmt.Errorf("real division by zero")
				}
				return x / y, nil
			case Int:
				yf := y.Float()
				if yf == 0.0 {
					return nil, fmt.Errorf("real division by zero")
				}
				return x / yf, nil
			}
		}

	case syntax.SLASHSLASH:
		switch x := x.(type) {
		case Int:
			switch y := y.(type) {
			case Int:
				if y.Sign() == 0 {
					return nil, fmt.Errorf("floored division by zero")
				}
				return x.Div(y), nil
			case Float:
				if y == 0.0 {
					return nil, fmt.Errorf("floored division by zero")
				}
				return floor((x.Float() / y)), nil
			}
		case Float:
			switch y := y.(type) {
			case Float:
				if y == 0.0 {
					return nil, fmt.Errorf("floored division by zero")
				}
				return floor(x / y), nil
			case Int:
				yf := y.Float()
				if yf == 0.0 {
					return nil, fmt.Errorf("floored division by zero")
				}
				return floor(x / yf), nil
			}
		}

	case syntax.PERCENT:
		switch x := x.(type) {
		case Int:
			switch y := y.(type) {
			case Int:
				if y.Sign() == 0 {
					return nil, fmt.Errorf("integer modulo by zero")
				}
				return x.Mod(y), nil
			case Float:
				if y == 0 {
					return nil, fmt.Errorf("float modulo by zero")
				}
				return x.Float().Mod(y), nil
			}
		case Float:
			switch y := y.(type) {
			case Float:
				if y == 0.0 {
					return nil, fmt.Errorf("float modulo by zero")
				}
				return Float(math.Mod(float64(x), float64(y))), nil
			case Int:
				if y.Sign() == 0 {
					return nil, fmt.Errorf("float modulo by zero")
				}
				return x.Mod(y.Float()), nil
			}
		case String:
			return interpolate(string(x), y)
		}

	case syntax.NOT_IN:
		z, err := Binary(syntax.IN, x, y)
		if err != nil {
			return nil, err
		}
		return !z.Truth(), nil

	case syntax.IN:
		switch y := y.(type) {
		case *List:
			for _, elem := range y.elems {
				if eq, err := Equal(elem, x); err != nil {
					return nil, err
				} else if eq {
					return True, nil
				}
			}
			return False, nil
		case Tuple:
			for _, elem := range y {
				if eq, err := Equal(elem, x); err != nil {
					return nil, err
				} else if eq {
					return True, nil
				}
			}
			return False, nil
		case Mapping: // e.g. dict
			// Ignore error from Get as we cannot distinguish true
			// errors (value cycle, type error) from "key not found".
			_, found, _ := y.Get(x)
			return Bool(found), nil
		case *Set:
			ok, err := y.Has(x)
			return Bool(ok), err
		case String:
			needle, ok := x.(String)
			if !ok {
				return nil, fmt.Errorf("'in <string>' requires string as left operand, not %s", x.Type())
			}
			return Bool(strings.Contains(string(y), string(needle))), nil
		case rangeValue:
			i, err := NumberToInt(x)
			if err != nil {
				return nil, fmt.Errorf("'in <range>' requires integer as left operand, not %s", x.Type())
			}
			return Bool(y.contains(i)), nil
		}

	case syntax.PIPE:
		switch x := x.(type) {
		case Int:
			if y, ok := y.(Int); ok {
				return x.Or(y), nil
			}
		case *Set: // union
			if y, ok := y.(*Set); ok {
				iter := Iterate(y)
				defer iter.Done()
				return x.Union(iter)
			}
		}

	case syntax.AMP:
		switch x := x.(type) {
		case Int:
			if y, ok := y.(Int); ok {
				return x.And(y), nil
			}
		case *Set: // intersection
			if y, ok := y.(*Set); ok {
				set := new(Set)
				if x.Len() > y.Len() {
					x, y = y, x // opt: range over smaller set
				}
				for _, xelem := range x.elems() {
					// Has, Insert cannot fail here.
					if found, _ := y.Has(xelem); found {
						set.Insert(xelem)
					}
				}
				return set, nil
			}
		}

	case syntax.CIRCUMFLEX:
		switch x := x.(type) {
		case Int:
			if y, ok := y.(Int); ok {
				return x.Xor(y), nil
			}
		case *Set: // symmetric difference
			if y, ok := y.(*Set); ok {
				set := new(Set)
				for _, xelem := range x.elems() {
					if found, _ := y.Has(xelem); !found {
						set.Insert(xelem)
					}
				}
				for _, yelem := range y.elems() {
					if found, _ := x.Has(yelem); !found {
						set.Insert(yelem)
					}
				}
				return set, nil
			}
		}

	case syntax.LTLT, syntax.GTGT:
		if x, ok := x.(Int); ok {
			y, err := AsInt32(y)
			if err != nil {
				return nil, err
			}
			if y < 0 {
				return nil, fmt.Errorf("negative shift count: %v", y)
			}
			if op == syntax.LTLT {
				if y >= 512 {
					return nil, fmt.Errorf("shift count too large: %v", y)
				}
				return x.Lsh(uint(y)), nil
			} else {
				return x.Rsh(uint(y)), nil
			}
		}

	default:
		// unknown operator
		goto unknown
	}

	// user-defined types
	// (nil, nil) => unhandled
	if x, ok := x.(HasBinary); ok {
		z, err := x.Binary(op, y, Left)
		if z != nil || err != nil {
			return z, err
		}
	}
	if y, ok := y.(HasBinary); ok {
		z, err := y.Binary(op, x, Right)
		if z != nil || err != nil {
			return z, err
		}
	}

	// unsupported operand types
unknown:
	return nil, fmt.Errorf("unknown binary op: %s %s %s", x.Type(), op, y.Type())
}

// It's always possible to overeat in small bites but we'll
// try to stop someone swallowing the world in one gulp.
const maxAlloc = 1 << 30

func tupleRepeat(elems Tuple, n Int) (Tuple, error) {
	if len(elems) == 0 {
		return nil, nil
	}
	i, err := AsInt32(n)
	if err != nil {
		return nil, fmt.Errorf("repeat count %s too large", n)
	}
	if i < 1 {
		return nil, nil
	}
	// Inv: i > 0, len > 0
	sz := len(elems) * i
	if sz < 0 || sz >= maxAlloc { // sz < 0 => overflow
		return nil, fmt.Errorf("excessive repeat (%d elements)", sz)
	}
	res := make([]Value, sz)
	// copy elems into res, doubling each time
	x := copy(res, elems)
	for x < len(res) {
		copy(res[x:], res[:x])
		x *= 2
	}
	return res, nil
}

func stringRepeat(s String, n Int) (String, error) {
	if s == "" {
		return "", nil
	}
	i, err := AsInt32(n)
	if err != nil {
		return "", fmt.Errorf("repeat count %s too large", n)
	}
	if i < 1 {
		return "", nil
	}
	// Inv: i > 0, len > 0
	sz := len(s) * i
	if sz < 0 || sz >= maxAlloc { // sz < 0 => overflow
		return "", fmt.Errorf("excessive repeat (%d elements)", sz)
	}
	return String(strings.Repeat(string(s), i)), nil
}

// Call calls the function fn with the specified positional and keyword arguments.
func Call(thread *Thread, fn Value, args Tuple, kwargs []Tuple) (Value, error) {
	c, ok := fn.(Callable)
	if !ok {
		return nil, fmt.Errorf("invalid call of non-function (%s)", fn.Type())
	}

	thread.frame = NewFrame(thread.frame, c)
	result, err := c.CallInternal(thread, args, kwargs)
	thread.frame = thread.frame.parent

	// Sanity check: nil is not a valid Starlark value.
	if result == nil && err == nil {
		return nil, fmt.Errorf("internal error: nil (not None) returned from %s", fn)
	}

	return result, err
}

func slice(x, lo, hi, step_ Value) (Value, error) {
	sliceable, ok := x.(Sliceable)
	if !ok {
		return nil, fmt.Errorf("invalid slice operand %s", x.Type())
	}

	n := sliceable.Len()
	step := 1
	if step_ != None {
		var err error
		step, err = AsInt32(step_)
		if err != nil {
			return nil, fmt.Errorf("got %s for slice step, want int", step_.Type())
		}
		if step == 0 {
			return nil, fmt.Errorf("zero is not a valid slice step")
		}
	}

	// TODO(adonovan): opt: preallocate result array.

	var start, end int
	if step > 0 {
		// positive stride
		// default indices are [0:n].
		var err error
		start, end, err = indices(lo, hi, n)
		if err != nil {
			return nil, err
		}

		if end < start {
			end = start // => empty result
		}
	} else {
		// negative stride
		// default indices are effectively [n-1:-1], though to
		// get this effect using explicit indices requires
		// [n-1:-1-n:-1] because of the treatment of -ve values.
		start = n - 1
		if err := asIndex(lo, n, &start); err != nil {
			return nil, fmt.Errorf("invalid start index: %s", err)
		}
		if start >= n {
			start = n - 1
		}

		end = -1
		if err := asIndex(hi, n, &end); err != nil {
			return nil, fmt.Errorf("invalid end index: %s", err)
		}
		if end < -1 {
			end = -1
		}

		if start < end {
			start = end // => empty result
		}
	}

	return sliceable.Slice(start, end, step), nil
}

// From Hacker's Delight, section 2.8.
func signum64(x int64) int { return int(uint64(x>>63) | uint64(-x)>>63) }
func signum(x int) int     { return signum64(int64(x)) }

// indices converts start_ and end_ to indices in the range [0:len].
// The start index defaults to 0 and the end index defaults to len.
// An index -len < i < 0 is treated like i+len.
// All other indices outside the range are clamped to the nearest value in the range.
// Beware: start may be greater than end.
// This function is suitable only for slices with positive strides.
func indices(start_, end_ Value, len int) (start, end int, err error) {
	start = 0
	if err := asIndex(start_, len, &start); err != nil {
		return 0, 0, fmt.Errorf("invalid start index: %s", err)
	}
	// Clamp to [0:len].
	if start < 0 {
		start = 0
	} else if start > len {
		start = len
	}

	end = len
	if err := asIndex(end_, len, &end); err != nil {
		return 0, 0, fmt.Errorf("invalid end index: %s", err)
	}
	// Clamp to [0:len].
	if end < 0 {
		end = 0
	} else if end > len {
		end = len
	}

	return start, end, nil
}

// asIndex sets *result to the integer value of v, adding len to it
// if it is negative.  If v is nil or None, *result is unchanged.
func asIndex(v Value, len int, result *int) error {
	if v != nil && v != None {
		var err error
		*result, err = AsInt32(v)
		if err != nil {
			return fmt.Errorf("got %s, want int", v.Type())
		}
		if *result < 0 {
			*result += len
		}
	}
	return nil
}

// setArgs sets the values of the formal parameters of function fn in
// based on the actual parameter values in args and kwargs.
func setArgs(locals []Value, fn *Function, args Tuple, kwargs []Tuple) error {

	// This is the general schema of a function:
	//
	//   def f(p1, p2=dp2, p3=dp3, *args, k1, k2=dk2, k3, **kwargs)
	//
	// The p parameters are non-kwonly, and may be specified positionally.
	// The k parameters are kwonly, and must be specified by name.
	// The defaults tuple is (dp2, dp3, mandatory, dk2, mandatory).
	//
	// Arguments are processed as follows:
	// - positional arguments are bound to a prefix of [p1, p2, p3].
	// - surplus positional arguments are bound to *args.
	// - keyword arguments are bound to any of {p1, p2, p3, k1, k2, k3};
	//   duplicate bindings are rejected.
	// - surplus keyword arguments are bound to **kwargs.
	// - defaults are bound to each parameter from p2 to k3 if no value was set.
	//   default values come from the tuple above.
	//   It is an error if the tuple entry for an unset parameter is 'mandatory'.

	// Nullary function?
	if fn.NumParams() == 0 {
		if nactual := len(args) + len(kwargs); nactual > 0 {
			return fmt.Errorf("function %s accepts no arguments (%d given)", fn.Name(), nactual)
		}
		return nil
	}

	cond := func(x bool, y, z interface{}) interface{} {
		if x {
			return y
		}
		return z
	}

	// nparams is the number of ordinary parameters (sans *args and **kwargs).
	nparams := fn.NumParams()
	var kwdict *Dict
	if fn.HasKwargs() {
		nparams--
		kwdict = new(Dict)
		locals[nparams] = kwdict
	}
	if fn.HasVarargs() {
		nparams--
	}

	// nonkwonly is the number of non-kwonly parameters.
	nonkwonly := nparams - fn.NumKwonlyParams()

	// Too many positional args?
	n := len(args)
	if len(args) > nonkwonly {
		if !fn.HasVarargs() {
			return fmt.Errorf("function %s accepts %s%d positional argument%s (%d given)",
				fn.Name(),
				cond(len(fn.defaults) > fn.NumKwonlyParams(), "at most ", ""),
				nonkwonly,
				cond(nonkwonly == 1, "", "s"),
				len(args))
		}
		n = nonkwonly
	}

	// Bind positional arguments to non-kwonly parameters.
	for i := 0; i < n; i++ {
		locals[i] = args[i]
	}

	// Bind surplus positional arguments to *args parameter.
	if fn.HasVarargs() {
		tuple := make(Tuple, len(args)-n)
		for i := n; i < len(args); i++ {
			tuple[i-n] = args[i]
		}
		locals[nparams] = tuple
	}

	// Bind keyword arguments to parameters.
	paramIdents := fn.funcode.Locals[:nparams]
	for _, pair := range kwargs {
		k, v := pair[0].(String), pair[1]
		if i := findParam(paramIdents, string(k)); i >= 0 {
			if locals[i] != nil {
				return fmt.Errorf("function %s got multiple values for parameter %s", fn.Name(), k)
			}
			locals[i] = v
			continue
		}
		if kwdict == nil {
			return fmt.Errorf("function %s got an unexpected keyword argument %s", fn.Name(), k)
		}
		oldlen := kwdict.Len()
		kwdict.SetKey(k, v)
		if kwdict.Len() == oldlen {
			return fmt.Errorf("function %s got multiple values for parameter %s", fn.Name(), k)
		}
	}

	// Are defaults required?
	if n < nparams || fn.NumKwonlyParams() > 0 {
		m := nparams - len(fn.defaults) // first default

		// Report errors for missing required arguments.
		var missing []string
		var i int
		for i = n; i < m; i++ {
			if locals[i] == nil {
				missing = append(missing, paramIdents[i].Name)
			}
		}

		// Bind default values to parameters.
		for ; i < nparams; i++ {
			if locals[i] == nil {
				dflt := fn.defaults[i-m]
				if _, ok := dflt.(mandatory); ok {
					missing = append(missing, paramIdents[i].Name)
					continue
				}
				locals[i] = dflt
			}
		}

		if missing != nil {
			return fmt.Errorf("function %s missing %d argument%s (%s)",
				fn.Name(), len(missing), cond(len(missing) > 1, "s", ""), strings.Join(missing, ", "))
		}
	}
	return nil
}

func findParam(params []compile.Ident, name string) int {
	for i, param := range params {
		if param.Name == name {
			return i
		}
	}
	return -1
}

type intset struct {
	small uint64       // bitset, used if n < 64
	large map[int]bool //    set, used if n >= 64
}

func (is *intset) init(n int) {
	if n >= 64 {
		is.large = make(map[int]bool)
	}
}

func (is *intset) set(i int) (prev bool) {
	if is.large == nil {
		prev = is.small&(1<<uint(i)) != 0
		is.small |= 1 << uint(i)
	} else {
		prev = is.large[i]
		is.large[i] = true
	}
	return
}

func (is *intset) get(i int) bool {
	if is.large == nil {
		return is.small&(1<<uint(i)) != 0
	}
	return is.large[i]
}

func (is *intset) len() int {
	if is.large == nil {
		// Suboptimal, but used only for error reporting.
		len := 0
		for i := 0; i < 64; i++ {
			if is.small&(1<<uint(i)) != 0 {
				len++
			}
		}
		return len
	}
	return len(is.large)
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string-interpolation
func interpolate(format string, x Value) (Value, error) {
	var buf bytes.Buffer
	index := 0
	nargs := 1
	if tuple, ok := x.(Tuple); ok {
		nargs = len(tuple)
	}
	for {
		i := strings.IndexByte(format, '%')
		if i < 0 {
			buf.WriteString(format)
			break
		}
		buf.WriteString(format[:i])
		format = format[i+1:]

		if format != "" && format[0] == '%' {
			buf.WriteByte('%')
			format = format[1:]
			continue
		}

		var arg Value
		if format != "" && format[0] == '(' {
			// keyword argument: %(name)s.
			format = format[1:]
			j := strings.IndexByte(format, ')')
			if j < 0 {
				return nil, fmt.Errorf("incomplete format key")
			}
			key := format[:j]
			if dict, ok := x.(Mapping); !ok {
				return nil, fmt.Errorf("format requires a mapping")
			} else if v, found, _ := dict.Get(String(key)); found {
				arg = v
			} else {
				return nil, fmt.Errorf("key not found: %s", key)
			}
			format = format[j+1:]
		} else {
			// positional argument: %s.
			if index >= nargs {
				return nil, fmt.Errorf("not enough arguments for format string")
			}
			if tuple, ok := x.(Tuple); ok {
				arg = tuple[index]
			} else {
				arg = x
			}
		}

		// NOTE: Starlark does not support any of these optional Python features:
		// - optional conversion flags: [#0- +], etc.
		// - optional minimum field width (number or *).
		// - optional precision (.123 or *)
		// - optional length modifier

		// conversion type
		if format == "" {
			return nil, fmt.Errorf("incomplete format")
		}
		switch c := format[0]; c {
		case 's', 'r':
			if str, ok := AsString(arg); ok && c == 's' {
				buf.WriteString(str)
			} else {
				writeValue(&buf, arg, nil)
			}
		case 'd', 'i', 'o', 'x', 'X':
			i, err := NumberToInt(arg)
			if err != nil {
				return nil, fmt.Errorf("%%%c format requires integer: %v", c, err)
			}
			switch c {
			case 'd', 'i':
				fmt.Fprintf(&buf, "%d", i)
			case 'o':
				fmt.Fprintf(&buf, "%o", i)
			case 'x':
				fmt.Fprintf(&buf, "%x", i)
			case 'X':
				fmt.Fprintf(&buf, "%X", i)
			}
		case 'e', 'f', 'g', 'E', 'F', 'G':
			f, ok := AsFloat(arg)
			if !ok {
				return nil, fmt.Errorf("%%%c format requires float, not %s", c, arg.Type())
			}
			switch c {
			case 'e':
				fmt.Fprintf(&buf, "%e", f)
			case 'f':
				fmt.Fprintf(&buf, "%f", f)
			case 'g':
				fmt.Fprintf(&buf, "%g", f)
			case 'E':
				fmt.Fprintf(&buf, "%E", f)
			case 'F':
				fmt.Fprintf(&buf, "%F", f)
			case 'G':
				fmt.Fprintf(&buf, "%G", f)
			}
		case 'c':
			switch arg := arg.(type) {
			case Int:
				// chr(int)
				r, err := AsInt32(arg)
				if err != nil || r < 0 || r > unicode.MaxRune {
					return nil, fmt.Errorf("%%c format requires a valid Unicode code point, got %s", arg)
				}
				buf.WriteRune(rune(r))
			case String:
				r, size := utf8.DecodeRuneInString(string(arg))
				if size != len(arg) || len(arg) == 0 {
					return nil, fmt.Errorf("%%c format requires a single-character string")
				}
				buf.WriteRune(r)
			default:
				return nil, fmt.Errorf("%%c format requires int or single-character string, not %s", arg.Type())
			}
		case '%':
			buf.WriteByte('%')
		default:
			return nil, fmt.Errorf("unknown conversion %%%c", c)
		}
		format = format[1:]
		index++
	}

	if index < nargs {
		return nil, fmt.Errorf("too many arguments for format string")
	}

	return String(buf.String()), nil
}
