package evalop

import (
	"go/ast"
	"go/constant"

	"github.com/go-delve/delve/pkg/dwarf/godwarf"
)

// Op is a stack machine opcode
type Op interface {
	depthCheck() (npop, npush int)
}

// PushCurg pushes the current goroutine on the stack.
type PushCurg struct {
}

func (*PushCurg) depthCheck() (npop, npush int) { return 0, 1 }

// PushFrameoff pushes the frame offset for the current frame on the stack.
type PushFrameoff struct {
}

func (*PushFrameoff) depthCheck() (npop, npush int) { return 0, 1 }

// PushThreadID pushes the ID of the current thread on the stack.
type PushThreadID struct {
}

func (*PushThreadID) depthCheck() (npop, npush int) { return 0, 1 }

// PushConst pushes a constant on the stack.
type PushConst struct {
	Value constant.Value
}

func (*PushConst) depthCheck() (npop, npush int) { return 0, 1 }

// PushLocal pushes the local variable with the given name on the stack.
type PushLocal struct {
	Name  string
	Frame int64
}

func (*PushLocal) depthCheck() (npop, npush int) { return 0, 1 }

// PushNil pushes an untyped nil on the stack.
type PushNil struct {
}

func (*PushNil) depthCheck() (npop, npush int) { return 0, 1 }

// PushRegister pushes the CPU register Regnum on the stack.
type PushRegister struct {
	Regnum  int
	Regname string
}

func (*PushRegister) depthCheck() (npop, npush int) { return 0, 1 }

// PushPackageVar pushes a package variable on the stack.
type PushPackageVar struct {
	PkgName, Name string // if PkgName == "" use current function's package
}

func (*PushPackageVar) depthCheck() (npop, npush int) { return 0, 1 }

// Select replaces the topmost stack variable v with v.Name.
type Select struct {
	Name string
}

func (*Select) depthCheck() (npop, npush int) { return 1, 1 }

// TypeAssert replaces the topmost stack variable v with v.(DwarfType).
type TypeAssert struct {
	DwarfType godwarf.Type
	Node      *ast.TypeAssertExpr
}

func (*TypeAssert) depthCheck() (npop, npush int) { return 1, 1 }

// PointerDeref replaces the topmost stack variable v with *v.
type PointerDeref struct {
	Node *ast.StarExpr
}

func (*PointerDeref) depthCheck() (npop, npush int) { return 1, 1 }

// Unary applies the given unary operator to the topmost stack variable.
type Unary struct {
	Node *ast.UnaryExpr
}

func (*Unary) depthCheck() (npop, npush int) { return 1, 1 }

// AddrOf replaces the topmost stack variable v with &v.
type AddrOf struct {
	Node *ast.UnaryExpr
}

func (*AddrOf) depthCheck() (npop, npush int) { return 1, 1 }

// TypeCast replaces the topmost stack variable v with (DwarfType)(v).
type TypeCast struct {
	DwarfType godwarf.Type
	Node      *ast.CallExpr
}

func (*TypeCast) depthCheck() (npop, npush int) { return 1, 1 }

// Reslice implements a reslice operation.
// If HasHigh is set it pops three variables, low, high and v, and pushes
// v[low:high].
// Otherwise it pops two variables, low and v, and pushes v[low:].
// If TrustLen is set when the variable resulting from the reslice is loaded it will be fully loaded.
type Reslice struct {
	HasHigh  bool
	TrustLen bool
	Node     *ast.SliceExpr
}

func (op *Reslice) depthCheck() (npop, npush int) {
	if op.HasHigh {
		return 3, 1
	} else {
		return 2, 1
	}
}

// Index pops two variables, idx and v, and pushes v[idx].
type Index struct {
	Node *ast.IndexExpr
}

func (*Index) depthCheck() (npop, npush int) { return 2, 1 }

// Jump looks at the topmost stack variable and if it satisfies the
// condition specified by When it jumps to the stack machine instruction at
// Target+1.
// If Pop is set the topmost stack variable is also popped.
type Jump struct {
	When   JumpCond
	Pop    bool
	Target int
	Node   ast.Expr
}

func (jmpif *Jump) depthCheck() (npop, npush int) {
	if jmpif.Pop {
		return 1, 0
	}
	return 0, 0
}

// JumpCond specifies a condition for the Jump instruction.
type JumpCond uint8

const (
	JumpIfFalse JumpCond = iota
	JumpIfTrue
)

// Binary pops two variables from the stack, applies the specified binary
// operator to them and pushes the result back on the stack.
type Binary struct {
	Node *ast.BinaryExpr
}

func (*Binary) depthCheck() (npop, npush int) { return 2, 1 }

// BoolToConst pops the topmost variable from the stack, which must be a
// boolean variable, and converts it to a constant.
type BoolToConst struct {
}

func (*BoolToConst) depthCheck() (npop, npush int) { return 1, 1 }

// Pop removes the topmost variable from the stack.
type Pop struct {
}

func (*Pop) depthCheck() (npop, npush int) { return 1, 0 }

// BuiltinCall pops len(Args) argument from the stack, calls the specified
// builtin on them and pushes the result back on the stack.
type BuiltinCall struct {
	Name string
	Args []ast.Expr
}

func (bc *BuiltinCall) depthCheck() (npop, npush int) {
	return len(bc.Args), 1
}

// CallInjectionStart starts call injection by calling
// runtime.debugCallVn.
type CallInjectionStart struct {
	id      int  // identifier for all the call injection instructions that belong to the same sequence, this only exists to make reading listings easier
	HasFunc bool // target function already pushed on the stack
	Node    *ast.CallExpr
}

func (*CallInjectionStart) depthCheck() (npop, npush int) { return 0, 1 }

// CallInjectionSetTarget starts the call injection, after
// runtime.debugCallVn set up the stack for us, by copying the entry point
// of the function, setting the closure register and copying the receiver.
type CallInjectionSetTarget struct {
	id int
}

func (*CallInjectionSetTarget) depthCheck() (npop, npush int) { return 1, 0 }

// CallInjectionCopyArg copies one argument for call injection.
type CallInjectionCopyArg struct {
	id      int
	ArgNum  int
	ArgExpr ast.Expr
}

func (*CallInjectionCopyArg) depthCheck() (npop, npush int) { return 1, 0 }

// CallInjectionComplete resumes target execution so that the injected call can run.
type CallInjectionComplete struct {
	id int
}

func (*CallInjectionComplete) depthCheck() (npop, npush int) { return 0, 1 }

// CallInjectionAllocString uses the call injection protocol to allocate the
// value of a string literal somewhere on the target's memory so that it can
// be assigned to a variable (or passed to a function).
// There are three phases to CallInjectionAllocString, distinguished by the
// Phase field. They must always appear in sequence in the program:
//
//	CallInjectionAllocString{Phase: 0}
//	CallInjectionAllocString{Phase: 1}
//	CallInjectionAllocString{Phase: 2}
type CallInjectionAllocString struct {
	Phase int
}

func (op *CallInjectionAllocString) depthCheck() (npop, npush int) { return 1, 1 }

// SetValue pops to variables from the stack, lhv and rhv, and sets lhv to
// rhv.
type SetValue struct {
	lhe, Rhe ast.Expr
}

func (*SetValue) depthCheck() (npop, npush int) { return 2, 0 }
