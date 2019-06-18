// Copyright 2017 The Bazel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package resolve defines a name-resolution pass for Starlark abstract
// syntax trees.
//
// The resolver sets the Locals and FreeVars arrays of each DefStmt and
// the LocalIndex field of each syntax.Ident that refers to a local or
// free variable.  It also sets the Locals array of a File for locals
// bound by comprehensions outside any function.  Identifiers for global
// variables do not get an index.
package resolve // import "go.starlark.net/resolve"

// All references to names are statically resolved.  Names may be
// predeclared, global, or local to a function or module-level comprehension.
// The resolver maps each global name to a small integer and each local
// name to a small integer; these integers enable a fast and compact
// representation of globals and locals in the evaluator.
//
// As an optimization, the resolver classifies each predeclared name as
// either universal (e.g. None, len) or per-module (e.g. glob in Bazel's
// build language), enabling the evaluator to share the representation
// of the universal environment across all modules.
//
// The lexical environment is a tree of blocks with the module block at
// its root.  The module's child blocks may be of two kinds: functions
// and comprehensions, and these may have further children of either
// kind.
//
// Python-style resolution requires multiple passes because a name is
// determined to be local to a function only if the function contains a
// "binding" use of it; similarly, a name is determined be global (as
// opposed to predeclared) if the module contains a binding use at the
// top level. For both locals and globals, a non-binding use may
// lexically precede the binding to which it is resolved.
// In the first pass, we inspect each function, recording in
// 'uses' each identifier and the environment block in which it occurs.
// If a use of a name is binding, such as a function parameter or
// assignment, we add the name to the block's bindings mapping and add a
// local variable to the enclosing function.
//
// As we finish resolving each function, we inspect all the uses within
// that function and discard ones that were found to be local. The
// remaining ones must be either free (local to some lexically enclosing
// function), or nonlocal (global or predeclared), but we cannot tell
// which until we have finished inspecting the outermost enclosing
// function. At that point, we can distinguish local from global names
// (and this is when Python would compute free variables).
//
// However, Starlark additionally requires that all references to global
// names are satisfied by some declaration in the current module;
// Starlark permits a function to forward-reference a global that has not
// been declared yet so long as it is declared before the end of the
// module.  So, instead of re-resolving the unresolved references after
// each top-level function, we defer this until the end of the module
// and ensure that all such references are satisfied by some definition.
//
// At the end of the module, we visit each of the nested function blocks
// in bottom-up order, doing a recursive lexical lookup for each
// unresolved name.  If the name is found to be local to some enclosing
// function, we must create a DefStmt.FreeVar (capture) parameter for
// each intervening function.  We enter these synthetic bindings into
// the bindings map so that we create at most one freevar per name.  If
// the name was not local, we check that it was defined at module level.
//
// We resolve all uses of locals in the module (due to comprehensions)
// in a similar way and compute the set of its local variables.
//
// Starlark enforces that all global names are assigned at most once on
// all control flow paths by forbidding if/else statements and loops at
// top level. A global may be used before it is defined, leading to a
// dynamic error.
//
// TODO(adonovan): opt: reuse local slots once locals go out of scope.

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"go.starlark.net/internal/spell"
	"go.starlark.net/syntax"
)

const debug = false
const doesnt = "this Starlark dialect does not "

// global options
// These features are either not standard Starlark (yet), or deprecated
// features of the BUILD language, so we put them behind flags.
var (
	AllowNestedDef      = false // allow def statements within function bodies
	AllowLambda         = false // allow lambda expressions
	AllowFloat          = false // allow floating point literals, the 'float' built-in, and x / y
	AllowSet            = false // allow the 'set' built-in
	AllowGlobalReassign = false // allow reassignment to globals declared in same file (deprecated)
	AllowRecursion      = false // allow while statements and recursive functions

	AllowBitwise = true // obsolete; bitwise operations (&, |, ^, ~, <<, and >>) are always enabled
)

// File resolves the specified file.
//
// The isPredeclared and isUniversal predicates report whether a name is
// a pre-declared identifier (visible in the current module) or a
// universal identifier (visible in every module).
// Clients should typically pass predeclared.Has for the first and
// starlark.Universe.Has for the second, where predeclared is the
// module's StringDict of predeclared names and starlark.Universe is the
// standard set of built-ins.
// The isUniverse predicate is supplied a parameter to avoid a cyclic
// dependency upon starlark.Universe, not because users should ever need
// to redefine it.
func File(file *syntax.File, isPredeclared, isUniversal func(name string) bool) error {
	r := newResolver(isPredeclared, isUniversal)
	r.stmts(file.Stmts)

	r.env.resolveLocalUses()

	// At the end of the module, resolve all non-local variable references,
	// computing closures.
	// Function bodies may contain forward references to later global declarations.
	r.resolveNonLocalUses(r.env)

	file.Locals = r.moduleLocals
	file.Globals = r.moduleGlobals

	if len(r.errors) > 0 {
		return r.errors
	}
	return nil
}

// Expr resolves the specified expression.
// It returns the local variables bound within the expression.
//
// The isPredeclared and isUniversal predicates behave as for the File function.
func Expr(expr syntax.Expr, isPredeclared, isUniversal func(name string) bool) ([]*syntax.Ident, error) {
	r := newResolver(isPredeclared, isUniversal)
	r.expr(expr)
	r.env.resolveLocalUses()
	r.resolveNonLocalUses(r.env) // globals & universals
	if len(r.errors) > 0 {
		return nil, r.errors
	}
	return r.moduleLocals, nil
}

// An ErrorList is a non-empty list of resolver error messages.
type ErrorList []Error // len > 0

func (e ErrorList) Error() string { return e[0].Error() }

// An Error describes the nature and position of a resolver error.
type Error struct {
	Pos syntax.Position
	Msg string
}

func (e Error) Error() string { return e.Pos.String() + ": " + e.Msg }

// The Scope of a syntax.Ident indicates what kind of scope it has.
type Scope uint8

const (
	Undefined   Scope = iota // name is not defined
	Local                    // name is local to its function
	Free                     // name is local to some enclosing function
	Global                   // name is global to module
	Predeclared              // name is predeclared for this module (e.g. glob)
	Universal                // name is universal (e.g. len)
)

var scopeNames = [...]string{
	Undefined:   "undefined",
	Local:       "local",
	Free:        "free",
	Global:      "global",
	Predeclared: "predeclared",
	Universal:   "universal",
}

func (scope Scope) String() string { return scopeNames[scope] }

func newResolver(isPredeclared, isUniversal func(name string) bool) *resolver {
	return &resolver{
		env:           new(block), // module block
		isPredeclared: isPredeclared,
		isUniversal:   isUniversal,
		globals:       make(map[string]*syntax.Ident),
	}
}

type resolver struct {
	// env is the current local environment:
	// a linked list of blocks, innermost first.
	// The tail of the list is the module block.
	env *block

	// moduleLocals contains the local variables of the module
	// (due to comprehensions outside any function).
	// moduleGlobals contains the global variables of the module.
	moduleLocals  []*syntax.Ident
	moduleGlobals []*syntax.Ident

	// globals maps each global name in the module
	// to its first binding occurrence.
	globals map[string]*syntax.Ident

	// These predicates report whether a name is
	// pre-declared, either in this module or universally.
	isPredeclared, isUniversal func(name string) bool

	loops int // number of enclosing for loops

	errors ErrorList
}

// container returns the innermost enclosing "container" block:
// a function (function != nil) or module (function == nil).
// Container blocks accumulate local variable bindings.
func (r *resolver) container() *block {
	for b := r.env; ; b = b.parent {
		if b.function != nil || b.isModule() {
			return b
		}
	}
}

func (r *resolver) push(b *block) {
	r.env.children = append(r.env.children, b)
	b.parent = r.env
	r.env = b
}

func (r *resolver) pop() { r.env = r.env.parent }

type block struct {
	parent *block // nil for module block

	// In the module (root) block, both these fields are nil.
	function *syntax.Function      // only for function blocks
	comp     *syntax.Comprehension // only for comprehension blocks

	// bindings maps a name to its binding.
	// A local binding has an index into its innermost enclosing container's locals array.
	// A free binding has an index into its innermost enclosing function's freevars array.
	bindings map[string]binding

	// children records the child blocks of the current one.
	children []*block

	// uses records all identifiers seen in this container (function or module),
	// and a reference to the environment in which they appear.
	// As we leave each container block, we resolve them,
	// so that only free and global ones remain.
	// At the end of each top-level function we compute closures.
	uses []use
}

type binding struct {
	scope Scope
	index int
}

func (b *block) isModule() bool { return b.parent == nil }

func (b *block) bind(name string, bind binding) {
	if b.bindings == nil {
		b.bindings = make(map[string]binding)
	}
	b.bindings[name] = bind
}

func (b *block) String() string {
	if b.function != nil {
		return "function block at " + fmt.Sprint(b.function.Span())
	}
	if b.comp != nil {
		return "comprehension block at " + fmt.Sprint(b.comp.Span())
	}
	return "module block"
}

func (r *resolver) errorf(posn syntax.Position, format string, args ...interface{}) {
	r.errors = append(r.errors, Error{posn, fmt.Sprintf(format, args...)})
}

// A use records an identifier and the environment in which it appears.
type use struct {
	id  *syntax.Ident
	env *block
}

// bind creates a binding for id in the current block,
// if there is not one already, and reports an error if
// a global was re-bound.
// It returns whether a binding already existed.
func (r *resolver) bind(id *syntax.Ident) bool {
	// Binding outside any local (comprehension/function) block?
	if r.env.isModule() {
		id.Scope = uint8(Global)
		prev, ok := r.globals[id.Name]
		if ok {
			if !AllowGlobalReassign {
				r.errorf(id.NamePos, "cannot reassign global %s declared at %s", id.Name, prev.NamePos)
			}
			id.Index = prev.Index
		} else {
			// first global binding of this name
			r.globals[id.Name] = id
			id.Index = len(r.moduleGlobals)
			r.moduleGlobals = append(r.moduleGlobals, id)
		}
		return ok
	}

	// Mark this name as local to current block.
	// Assign it a new local (positive) index in the current container.
	_, ok := r.env.bindings[id.Name]
	if !ok {
		var locals *[]*syntax.Ident
		if fn := r.container().function; fn != nil {
			locals = &fn.Locals
		} else {
			locals = &r.moduleLocals
		}
		r.env.bind(id.Name, binding{Local, len(*locals)})
		*locals = append(*locals, id)
	}

	r.use(id)
	return ok
}

func (r *resolver) use(id *syntax.Ident) {
	use := use{id, r.env}

	// The spec says that if there is a global binding of a name
	// then all references to that name in that block refer to the
	// global, even if the use precedes the def---just as for locals.
	// For example, in this code,
	//
	//   print(len); len=1; print(len)
	//
	// both occurrences of len refer to the len=1 binding, which
	// completely shadows the predeclared len function.
	//
	// The rationale for these semantics, which differ from Python,
	// is that the static meaning of len (a reference to a global)
	// does not change depending on where it appears in the file.
	// Of course, its dynamic meaning does change, from an error
	// into a valid reference, so it's not clear these semantics
	// have any practical advantage.
	//
	// In any case, the Bazel implementation lags behind the spec
	// and follows Python behavior, so the first use of len refers
	// to the predeclared function.  This typically used in a BUILD
	// file that redefines a predeclared name half way through,
	// for example:
	//
	//	proto_library(...) 			# built-in rule
	//      load("myproto.bzl", "proto_library")
	//	proto_library(...) 			# user-defined rule
	//
	// We will piggyback support for the legacy semantics on the
	// AllowGlobalReassign flag, which is loosely related and also
	// required for Bazel.
	if AllowGlobalReassign && r.env.isModule() {
		r.useGlobal(use)
		return
	}

	b := r.container()
	b.uses = append(b.uses, use)
}

// useGlobals resolves use.id as a reference to a global.
// The use.env field captures the original environment for error reporting.
func (r *resolver) useGlobal(use use) binding {
	id := use.id
	var scope Scope
	if prev, ok := r.globals[id.Name]; ok {
		scope = Global // use of global declared by module
		id.Index = prev.Index
	} else if r.isPredeclared(id.Name) {
		scope = Predeclared // use of pre-declared
	} else if r.isUniversal(id.Name) {
		scope = Universal // use of universal name
		if !AllowFloat && id.Name == "float" {
			r.errorf(id.NamePos, doesnt+"support floating point")
		}
		if !AllowSet && id.Name == "set" {
			r.errorf(id.NamePos, doesnt+"support sets")
		}
	} else {
		scope = Undefined
		var hint string
		if n := r.spellcheck(use); n != "" {
			hint = fmt.Sprintf(" (did you mean %s?)", n)
		}
		r.errorf(id.NamePos, "undefined: %s%s", id.Name, hint)
	}
	id.Scope = uint8(scope)
	return binding{scope, id.Index}
}

// spellcheck returns the most likely misspelling of
// the name use.id in the environment use.env.
func (r *resolver) spellcheck(use use) string {
	var names []string

	// locals
	for b := use.env; b != nil; b = b.parent {
		for name := range b.bindings {
			names = append(names, name)
		}
	}

	// globals
	//
	// We have no way to enumerate predeclared/universe,
	// which includes prior names in the REPL session.
	for _, id := range r.moduleGlobals {
		names = append(names, id.Name)
	}

	sort.Strings(names)
	return spell.Nearest(use.id.Name, names)
}

// resolveLocalUses is called when leaving a container (function/module)
// block.  It resolves all uses of locals within that block.
func (b *block) resolveLocalUses() {
	unresolved := b.uses[:0]
	for _, use := range b.uses {
		if bind := lookupLocal(use); bind.scope == Local {
			use.id.Scope = uint8(bind.scope)
			use.id.Index = bind.index
		} else {
			unresolved = append(unresolved, use)
		}
	}
	b.uses = unresolved
}

func (r *resolver) stmts(stmts []syntax.Stmt) {
	for _, stmt := range stmts {
		r.stmt(stmt)
	}
}

func (r *resolver) stmt(stmt syntax.Stmt) {
	switch stmt := stmt.(type) {
	case *syntax.ExprStmt:
		r.expr(stmt.X)

	case *syntax.BranchStmt:
		if r.loops == 0 && (stmt.Token == syntax.BREAK || stmt.Token == syntax.CONTINUE) {
			r.errorf(stmt.TokenPos, "%s not in a loop", stmt.Token)
		}

	case *syntax.IfStmt:
		if !AllowGlobalReassign && r.container().function == nil {
			r.errorf(stmt.If, "if statement not within a function")
		}
		r.expr(stmt.Cond)
		r.stmts(stmt.True)
		r.stmts(stmt.False)

	case *syntax.AssignStmt:
		r.expr(stmt.RHS)
		isAugmented := stmt.Op != syntax.EQ
		r.assign(stmt.LHS, isAugmented)

	case *syntax.DefStmt:
		if !AllowNestedDef && r.container().function != nil {
			r.errorf(stmt.Def, doesnt+"support nested def")
		}
		r.bind(stmt.Name)
		r.function(stmt.Def, stmt.Name.Name, &stmt.Function)

	case *syntax.ForStmt:
		if !AllowGlobalReassign && r.container().function == nil {
			r.errorf(stmt.For, "for loop not within a function")
		}
		r.expr(stmt.X)
		const isAugmented = false
		r.assign(stmt.Vars, isAugmented)
		r.loops++
		r.stmts(stmt.Body)
		r.loops--

	case *syntax.WhileStmt:
		if !AllowRecursion {
			r.errorf(stmt.While, doesnt+"support while loops")
		}
		if !AllowGlobalReassign && r.container().function == nil {
			r.errorf(stmt.While, "while loop not within a function")
		}
		r.expr(stmt.Cond)
		r.loops++
		r.stmts(stmt.Body)
		r.loops--

	case *syntax.ReturnStmt:
		if r.container().function == nil {
			r.errorf(stmt.Return, "return statement not within a function")
		}
		if stmt.Result != nil {
			r.expr(stmt.Result)
		}

	case *syntax.LoadStmt:
		if r.container().function != nil {
			r.errorf(stmt.Load, "load statement within a function")
		}

		for i, from := range stmt.From {
			if from.Name == "" {
				r.errorf(from.NamePos, "load: empty identifier")
				continue
			}
			if from.Name[0] == '_' {
				r.errorf(from.NamePos, "load: names with leading underscores are not exported: %s", from.Name)
			}
			r.bind(stmt.To[i])
		}

	default:
		log.Fatalf("unexpected stmt %T", stmt)
	}
}

func (r *resolver) assign(lhs syntax.Expr, isAugmented bool) {
	switch lhs := lhs.(type) {
	case *syntax.Ident:
		// x = ...
		r.bind(lhs)

	case *syntax.IndexExpr:
		// x[i] = ...
		r.expr(lhs.X)
		r.expr(lhs.Y)

	case *syntax.DotExpr:
		// x.f = ...
		r.expr(lhs.X)

	case *syntax.TupleExpr:
		// (x, y) = ...
		if len(lhs.List) == 0 {
			r.errorf(syntax.Start(lhs), "can't assign to ()")
		}
		if isAugmented {
			r.errorf(syntax.Start(lhs), "can't use tuple expression in augmented assignment")
		}
		for _, elem := range lhs.List {
			r.assign(elem, isAugmented)
		}

	case *syntax.ListExpr:
		// [x, y, z] = ...
		if len(lhs.List) == 0 {
			r.errorf(syntax.Start(lhs), "can't assign to []")
		}
		if isAugmented {
			r.errorf(syntax.Start(lhs), "can't use list expression in augmented assignment")
		}
		for _, elem := range lhs.List {
			r.assign(elem, isAugmented)
		}

	case *syntax.ParenExpr:
		r.assign(lhs.X, isAugmented)

	default:
		name := strings.ToLower(strings.TrimPrefix(fmt.Sprintf("%T", lhs), "*syntax."))
		r.errorf(syntax.Start(lhs), "can't assign to %s", name)
	}
}

func (r *resolver) expr(e syntax.Expr) {
	switch e := e.(type) {
	case *syntax.Ident:
		r.use(e)

	case *syntax.Literal:
		if !AllowFloat && e.Token == syntax.FLOAT {
			r.errorf(e.TokenPos, doesnt+"support floating point")
		}

	case *syntax.ListExpr:
		for _, x := range e.List {
			r.expr(x)
		}

	case *syntax.CondExpr:
		r.expr(e.Cond)
		r.expr(e.True)
		r.expr(e.False)

	case *syntax.IndexExpr:
		r.expr(e.X)
		r.expr(e.Y)

	case *syntax.DictEntry:
		r.expr(e.Key)
		r.expr(e.Value)

	case *syntax.SliceExpr:
		r.expr(e.X)
		if e.Lo != nil {
			r.expr(e.Lo)
		}
		if e.Hi != nil {
			r.expr(e.Hi)
		}
		if e.Step != nil {
			r.expr(e.Step)
		}

	case *syntax.Comprehension:
		// The 'in' operand of the first clause (always a ForClause)
		// is resolved in the outer block; consider: [x for x in x].
		clause := e.Clauses[0].(*syntax.ForClause)
		r.expr(clause.X)

		// A list/dict comprehension defines a new lexical block.
		// Locals defined within the block will be allotted
		// distinct slots in the locals array of the innermost
		// enclosing container (function/module) block.
		r.push(&block{comp: e})

		const isAugmented = false
		r.assign(clause.Vars, isAugmented)

		for _, clause := range e.Clauses[1:] {
			switch clause := clause.(type) {
			case *syntax.IfClause:
				r.expr(clause.Cond)
			case *syntax.ForClause:
				r.assign(clause.Vars, isAugmented)
				r.expr(clause.X)
			}
		}
		r.expr(e.Body) // body may be *DictEntry
		r.pop()

	case *syntax.TupleExpr:
		for _, x := range e.List {
			r.expr(x)
		}

	case *syntax.DictExpr:
		for _, entry := range e.List {
			entry := entry.(*syntax.DictEntry)
			r.expr(entry.Key)
			r.expr(entry.Value)
		}

	case *syntax.UnaryExpr:
		r.expr(e.X)

	case *syntax.BinaryExpr:
		if !AllowFloat && e.Op == syntax.SLASH {
			r.errorf(e.OpPos, doesnt+"support floating point (use //)")
		}
		r.expr(e.X)
		r.expr(e.Y)

	case *syntax.DotExpr:
		r.expr(e.X)
		// ignore e.Name

	case *syntax.CallExpr:
		r.expr(e.Fn)
		var seenVarargs, seenKwargs bool
		var seenName map[string]bool
		var n, p int
		for _, arg := range e.Args {
			pos, _ := arg.Span()
			if unop, ok := arg.(*syntax.UnaryExpr); ok && unop.Op == syntax.STARSTAR {
				// **kwargs
				if seenKwargs {
					r.errorf(pos, "multiple **kwargs not allowed")
				}
				seenKwargs = true
				r.expr(arg)
			} else if ok && unop.Op == syntax.STAR {
				// *args
				if seenKwargs {
					r.errorf(pos, "*args may not follow **kwargs")
				} else if seenVarargs {
					r.errorf(pos, "multiple *args not allowed")
				}
				seenVarargs = true
				r.expr(arg)
			} else if binop, ok := arg.(*syntax.BinaryExpr); ok && binop.Op == syntax.EQ {
				// k=v
				n++
				if seenKwargs {
					r.errorf(pos, "argument may not follow **kwargs")
				}
				x := binop.X.(*syntax.Ident)
				if seenName[x.Name] {
					r.errorf(x.NamePos, "keyword argument %s repeated", x.Name)
				} else {
					if seenName == nil {
						seenName = make(map[string]bool)
					}
					seenName[x.Name] = true
				}
				r.expr(binop.Y)
			} else {
				// positional argument
				p++
				if seenVarargs {
					r.errorf(pos, "argument may not follow *args")
				} else if seenKwargs {
					r.errorf(pos, "argument may not follow **kwargs")
				} else if len(seenName) > 0 {
					r.errorf(pos, "positional argument may not follow named")
				}
				r.expr(arg)
			}
		}

		// Fail gracefully if compiler-imposed limit is exceeded.
		if p >= 256 {
			pos, _ := e.Span()
			r.errorf(pos, "%v positional arguments in call, limit is 255", p)
		}
		if n >= 256 {
			pos, _ := e.Span()
			r.errorf(pos, "%v keyword arguments in call, limit is 255", n)
		}

	case *syntax.LambdaExpr:
		if !AllowLambda {
			r.errorf(e.Lambda, doesnt+"support lambda")
		}
		r.function(e.Lambda, "lambda", &e.Function)

	case *syntax.ParenExpr:
		r.expr(e.X)

	default:
		log.Fatalf("unexpected expr %T", e)
	}
}

func (r *resolver) function(pos syntax.Position, name string, function *syntax.Function) {
	// Resolve defaults in enclosing environment.
	for _, param := range function.Params {
		if binary, ok := param.(*syntax.BinaryExpr); ok {
			r.expr(binary.Y)
		}
	}

	// Enter function block.
	b := &block{function: function}
	r.push(b)

	var seenOptional bool
	var star *syntax.UnaryExpr // * or *args param
	var starStar *syntax.Ident // **kwargs ident
	var numKwonlyParams int
	for _, param := range function.Params {
		switch param := param.(type) {
		case *syntax.Ident:
			// e.g. x
			if starStar != nil {
				r.errorf(param.NamePos, "required parameter may not follow **%s", starStar.Name)
			} else if star != nil {
				numKwonlyParams++
			} else if seenOptional {
				r.errorf(param.NamePos, "required parameter may not follow optional")
			}
			if r.bind(param) {
				r.errorf(param.NamePos, "duplicate parameter: %s", param.Name)
			}

		case *syntax.BinaryExpr:
			// e.g. y=dflt
			if starStar != nil {
				r.errorf(param.OpPos, "optional parameter may not follow **%s", starStar.Name)
			} else if star != nil {
				numKwonlyParams++
			}
			if id := param.X.(*syntax.Ident); r.bind(id) {
				r.errorf(param.OpPos, "duplicate parameter: %s", id.Name)
			}
			seenOptional = true

		case *syntax.UnaryExpr:
			// * or *args or **kwargs
			if param.Op == syntax.STAR {
				if starStar != nil {
					r.errorf(param.OpPos, "* parameter may not follow **%s", starStar.Name)
				} else if star != nil {
					r.errorf(param.OpPos, "multiple * parameters not allowed")
				} else {
					star = param
				}
			} else {
				if starStar != nil {
					r.errorf(param.OpPos, "multiple ** parameters not allowed")
				}
				starStar = param.X.(*syntax.Ident)
			}
		}
	}

	// Bind the *args and **kwargs parameters at the end,
	// so that regular parameters a/b/c are contiguous and
	// there is no hole for the "*":
	//   def f(a, b, *args, c=0, **kwargs)
	//   def f(a, b, *,     c=0, **kwargs)
	if star != nil {
		if id, _ := star.X.(*syntax.Ident); id != nil {
			// *args
			if r.bind(id) {
				r.errorf(id.NamePos, "duplicate parameter: %s", id.Name)
			}
			function.HasVarargs = true
		} else if numKwonlyParams == 0 {
			r.errorf(star.OpPos, "bare * must be followed by keyword-only parameters")
		}
	}
	if starStar != nil {
		if r.bind(starStar) {
			r.errorf(starStar.NamePos, "duplicate parameter: %s", starStar.Name)
		}
		function.HasKwargs = true
	}

	function.NumKwonlyParams = numKwonlyParams
	r.stmts(function.Body)

	// Resolve all uses of this function's local vars,
	// and keep just the remaining uses of free/global vars.
	b.resolveLocalUses()

	// Leave function block.
	r.pop()

	// References within the function body to globals are not
	// resolved until the end of the module.
}

func (r *resolver) resolveNonLocalUses(b *block) {
	// First resolve inner blocks.
	for _, child := range b.children {
		r.resolveNonLocalUses(child)
	}
	for _, use := range b.uses {
		bind := r.lookupLexical(use, use.env)
		use.id.Scope = uint8(bind.scope)
		use.id.Index = bind.index
	}
}

// lookupLocal looks up an identifier within its immediately enclosing function.
func lookupLocal(use use) binding {
	for env := use.env; env != nil; env = env.parent {
		if bind, ok := env.bindings[use.id.Name]; ok {
			if bind.scope == Free {
				// shouldn't exist till later
				log.Fatalf("%s: internal error: %s, %d", use.id.NamePos, use.id.Name, bind)
			}
			return bind // found
		}
		if env.function != nil {
			break
		}
	}
	return binding{} // not found in this function
}

// lookupLexical looks up an identifier use.id within its lexically enclosing environment.
// The use.env field captures the original environment for error reporting.
func (r *resolver) lookupLexical(use use, env *block) (bind binding) {
	if debug {
		fmt.Printf("lookupLexical %s in %s = ...\n", use.id.Name, env)
		defer func() { fmt.Printf("= %d\n", bind) }()
	}

	// Is this the module block?
	if env.isModule() {
		return r.useGlobal(use) // global, predeclared, or not found
	}

	// Defined in this block?
	bind, ok := env.bindings[use.id.Name]
	if !ok {
		// Defined in parent block?
		bind = r.lookupLexical(use, env.parent)
		if env.function != nil && (bind.scope == Local || bind.scope == Free) {
			// Found in parent block, which belongs to enclosing function.
			id := &syntax.Ident{
				Name:  use.id.Name,
				Scope: uint8(bind.scope),
				Index: bind.index,
			}
			bind.scope = Free
			bind.index = len(env.function.FreeVars)
			env.function.FreeVars = append(env.function.FreeVars, id)
			if debug {
				fmt.Printf("creating freevar %v in function at %s: %s\n",
					len(env.function.FreeVars), fmt.Sprint(env.function.Span()), id.Name)
			}
		}

		// Memoize, to avoid duplicate free vars
		// and redundant global (failing) lookups.
		env.bind(use.id.Name, bind)
	}
	return bind
}
