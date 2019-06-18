// Copyright 2017 The Bazel Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package starlark

// This file defines the library of built-ins.
//
// Built-ins must explicitly check the "frozen" flag before updating
// mutable types such as lists and dicts.

import (
	"bytes"
	"fmt"
	"log"
	"math/big"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.starlark.net/syntax"
)

// Universe defines the set of universal built-ins, such as None, True, and len.
//
// The Go application may add or remove items from the
// universe dictionary before Starlark evaluation begins.
// All values in the dictionary must be immutable.
// Starlark programs cannot modify the dictionary.
var Universe StringDict

func init() {
	// https://github.com/google/starlark-go/blob/master/doc/spec.md#built-in-constants-and-functions
	Universe = StringDict{
		"None":      None,
		"True":      True,
		"False":     False,
		"any":       NewBuiltin("any", any),
		"all":       NewBuiltin("all", all),
		"bool":      NewBuiltin("bool", bool_),
		"chr":       NewBuiltin("chr", chr),
		"dict":      NewBuiltin("dict", dict),
		"dir":       NewBuiltin("dir", dir),
		"enumerate": NewBuiltin("enumerate", enumerate),
		"float":     NewBuiltin("float", float), // requires resolve.AllowFloat
		"getattr":   NewBuiltin("getattr", getattr),
		"hasattr":   NewBuiltin("hasattr", hasattr),
		"hash":      NewBuiltin("hash", hash),
		"int":       NewBuiltin("int", int_),
		"len":       NewBuiltin("len", len_),
		"list":      NewBuiltin("list", list),
		"max":       NewBuiltin("max", minmax),
		"min":       NewBuiltin("min", minmax),
		"ord":       NewBuiltin("ord", ord),
		"print":     NewBuiltin("print", print),
		"range":     NewBuiltin("range", range_),
		"repr":      NewBuiltin("repr", repr),
		"reversed":  NewBuiltin("reversed", reversed),
		"set":       NewBuiltin("set", set), // requires resolve.AllowSet
		"sorted":    NewBuiltin("sorted", sorted),
		"str":       NewBuiltin("str", str),
		"tuple":     NewBuiltin("tuple", tuple),
		"type":      NewBuiltin("type", type_),
		"zip":       NewBuiltin("zip", zip),
	}
}

type builtinMethod func(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error)

// methods of built-in types
// https://github.com/google/starlark-go/blob/master/doc/spec.md#built-in-methods
var (
	dictMethods = map[string]builtinMethod{
		"clear":      dict_clear,
		"get":        dict_get,
		"items":      dict_items,
		"keys":       dict_keys,
		"pop":        dict_pop,
		"popitem":    dict_popitem,
		"setdefault": dict_setdefault,
		"update":     dict_update,
		"values":     dict_values,
	}

	listMethods = map[string]builtinMethod{
		"append": list_append,
		"clear":  list_clear,
		"extend": list_extend,
		"index":  list_index,
		"insert": list_insert,
		"pop":    list_pop,
		"remove": list_remove,
	}

	stringMethods = map[string]builtinMethod{
		"capitalize":     string_capitalize,
		"codepoint_ords": string_iterable,
		"codepoints":     string_iterable, // sic
		"count":          string_count,
		"elem_ords":      string_iterable,
		"elems":          string_iterable,   // sic
		"endswith":       string_startswith, // sic
		"find":           string_find,
		"format":         string_format,
		"index":          string_index,
		"isalnum":        string_isalnum,
		"isalpha":        string_isalpha,
		"isdigit":        string_isdigit,
		"islower":        string_islower,
		"isspace":        string_isspace,
		"istitle":        string_istitle,
		"isupper":        string_isupper,
		"join":           string_join,
		"lower":          string_lower,
		"lstrip":         string_strip, // sic
		"partition":      string_partition,
		"replace":        string_replace,
		"rfind":          string_rfind,
		"rindex":         string_rindex,
		"rpartition":     string_partition, // sic
		"rsplit":         string_split,     // sic
		"rstrip":         string_strip,     // sic
		"split":          string_split,
		"splitlines":     string_splitlines,
		"startswith":     string_startswith,
		"strip":          string_strip,
		"title":          string_title,
		"upper":          string_upper,
	}

	setMethods = map[string]builtinMethod{
		"union": set_union,
	}
)

func builtinAttr(recv Value, name string, methods map[string]builtinMethod) (Value, error) {
	method := methods[name]
	if method == nil {
		return nil, nil // no such method
	}

	// Allocate a closure over 'method'.
	impl := func(thread *Thread, b *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
		return method(b.Name(), b.Receiver(), args, kwargs)
	}
	return NewBuiltin(name, impl).BindReceiver(recv), nil
}

func builtinAttrNames(methods map[string]builtinMethod) []string {
	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// UnpackArgs unpacks the positional and keyword arguments into the
// supplied parameter variables.  pairs is an alternating list of names
// and pointers to variables.
//
// If the variable is a bool, int, string, *List, *Dict, Callable,
// Iterable, or user-defined implementation of Value,
// UnpackArgs performs the appropriate type check.
// An int uses the AsInt32 check.
// If the parameter name ends with "?",
// it and all following parameters are optional.
//
// If the variable implements Value, UnpackArgs may call
// its Type() method while constructing the error message.
//
// Beware: an optional *List, *Dict, Callable, Iterable, or Value variable that is
// not assigned is not a valid Starlark Value, so the caller must
// explicitly handle such cases by interpreting nil as None or some
// computed default.
func UnpackArgs(fnname string, args Tuple, kwargs []Tuple, pairs ...interface{}) error {
	nparams := len(pairs) / 2
	var defined intset
	defined.init(nparams)

	// positional arguments
	if len(args) > nparams {
		return fmt.Errorf("%s: got %d arguments, want at most %d",
			fnname, len(args), nparams)
	}
	for i, arg := range args {
		defined.set(i)
		if err := unpackOneArg(arg, pairs[2*i+1]); err != nil {
			return fmt.Errorf("%s: for parameter %d: %s", fnname, i+1, err)
		}
	}

	// keyword arguments
kwloop:
	for _, item := range kwargs {
		name, arg := item[0].(String), item[1]
		for i := 0; i < nparams; i++ {
			paramName := pairs[2*i].(string)
			if paramName[len(paramName)-1] == '?' {
				paramName = paramName[:len(paramName)-1]
			}
			if paramName == string(name) {
				// found it
				if defined.set(i) {
					return fmt.Errorf("%s: got multiple values for keyword argument %s",
						fnname, name)
				}
				ptr := pairs[2*i+1]
				if err := unpackOneArg(arg, ptr); err != nil {
					return fmt.Errorf("%s: for parameter %s: %s", fnname, name, err)
				}
				continue kwloop
			}
		}
		return fmt.Errorf("%s: unexpected keyword argument %s", fnname, name)
	}

	// Check that all non-optional parameters are defined.
	// (We needn't check the first len(args).)
	for i := len(args); i < nparams; i++ {
		name := pairs[2*i].(string)
		if strings.HasSuffix(name, "?") {
			break // optional
		}
		if !defined.get(i) {
			return fmt.Errorf("%s: missing argument for %s", fnname, name)
		}
	}

	return nil
}

// UnpackPositionalArgs unpacks the positional arguments into
// corresponding variables.  Each element of vars is a pointer; see
// UnpackArgs for allowed types and conversions.
//
// UnpackPositionalArgs reports an error if the number of arguments is
// less than min or greater than len(vars), if kwargs is nonempty, or if
// any conversion fails.
func UnpackPositionalArgs(fnname string, args Tuple, kwargs []Tuple, min int, vars ...interface{}) error {
	if len(kwargs) > 0 {
		return fmt.Errorf("%s: unexpected keyword arguments", fnname)
	}
	max := len(vars)
	if len(args) < min {
		var atleast string
		if min < max {
			atleast = "at least "
		}
		return fmt.Errorf("%s: got %d arguments, want %s%d", fnname, len(args), atleast, min)
	}
	if len(args) > max {
		var atmost string
		if max > min {
			atmost = "at most "
		}
		return fmt.Errorf("%s: got %d arguments, want %s%d", fnname, len(args), atmost, max)
	}
	for i, arg := range args {
		if err := unpackOneArg(arg, vars[i]); err != nil {
			return fmt.Errorf("%s: for parameter %d: %s", fnname, i+1, err)
		}
	}
	return nil
}

func unpackOneArg(v Value, ptr interface{}) error {
	// On failure, don't clobber *ptr.
	switch ptr := ptr.(type) {
	case *Value:
		*ptr = v
	case *string:
		s, ok := AsString(v)
		if !ok {
			return fmt.Errorf("got %s, want string", v.Type())
		}
		*ptr = s
	case *bool:
		b, ok := v.(Bool)
		if !ok {
			return fmt.Errorf("got %s, want bool", v.Type())
		}
		*ptr = bool(b)
	case *int:
		i, err := AsInt32(v)
		if err != nil {
			return err
		}
		*ptr = i
	case **List:
		list, ok := v.(*List)
		if !ok {
			return fmt.Errorf("got %s, want list", v.Type())
		}
		*ptr = list
	case **Dict:
		dict, ok := v.(*Dict)
		if !ok {
			return fmt.Errorf("got %s, want dict", v.Type())
		}
		*ptr = dict
	case *Callable:
		f, ok := v.(Callable)
		if !ok {
			return fmt.Errorf("got %s, want callable", v.Type())
		}
		*ptr = f
	case *Iterable:
		it, ok := v.(Iterable)
		if !ok {
			return fmt.Errorf("got %s, want iterable", v.Type())
		}
		*ptr = it
	default:
		// v must have type *V, where V is some subtype of starlark.Value.
		ptrv := reflect.ValueOf(ptr)
		if ptrv.Kind() != reflect.Ptr {
			log.Panicf("internal error: not a pointer: %T", ptr)
		}
		paramVar := ptrv.Elem()
		if !reflect.TypeOf(v).AssignableTo(paramVar.Type()) {
			// The value is not assignable to the variable.

			// Detect a possible bug in the Go program that called Unpack:
			// If the variable *ptr is not a subtype of Value,
			// no value of v can possibly work.
			if !paramVar.Type().AssignableTo(reflect.TypeOf(new(Value)).Elem()) {
				log.Panicf("pointer element type does not implement Value: %T", ptr)
			}

			// Report Starlark dynamic type error.
			//
			// We prefer the Starlark Value.Type name over
			// its Go reflect.Type name, but calling the
			// Value.Type method on the variable is not safe
			// in general. If the variable is an interface,
			// the call will fail. Even if the variable has
			// a concrete type, it might not be safe to call
			// Type() on a zero instance. Thus we must use
			// recover.

			// Default to Go reflect.Type name
			paramType := paramVar.Type().String()

			// Attempt to call Value.Type method.
			func() {
				defer func() { recover() }()
				paramType = paramVar.MethodByName("Type").Call(nil)[0].String()
			}()
			return fmt.Errorf("got %s, want %s", v.Type(), paramType)
		}
		paramVar.Set(reflect.ValueOf(v))
	}
	return nil
}

// ---- built-in functions ----

// https://github.com/google/starlark-go/blob/master/doc/spec.md#all
func all(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var iterable Iterable
	if err := UnpackPositionalArgs("all", args, kwargs, 1, &iterable); err != nil {
		return nil, err
	}
	iter := iterable.Iterate()
	defer iter.Done()
	var x Value
	for iter.Next(&x) {
		if !x.Truth() {
			return False, nil
		}
	}
	return True, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#any
func any(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var iterable Iterable
	if err := UnpackPositionalArgs("any", args, kwargs, 1, &iterable); err != nil {
		return nil, err
	}
	iter := iterable.Iterate()
	defer iter.Done()
	var x Value
	for iter.Next(&x) {
		if x.Truth() {
			return True, nil
		}
	}
	return False, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#bool
func bool_(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var x Value = False
	if err := UnpackPositionalArgs("bool", args, kwargs, 0, &x); err != nil {
		return nil, err
	}
	return x.Truth(), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#chr
func chr(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("chr does not accept keyword arguments")
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("chr: got %d arguments, want 1", len(args))
	}
	i, err := AsInt32(args[0])
	if err != nil {
		return nil, fmt.Errorf("chr: got %s, want int", args[0].Type())
	}
	if i < 0 {
		return nil, fmt.Errorf("chr: Unicode code point %d out of range (<0)", i)
	}
	if i > unicode.MaxRune {
		return nil, fmt.Errorf("chr: Unicode code point U+%X out of range (>0x10FFFF)", i)
	}
	return String(string(i)), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#dict
func dict(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("dict: got %d arguments, want at most 1", len(args))
	}
	dict := new(Dict)
	if err := updateDict(dict, args, kwargs); err != nil {
		return nil, fmt.Errorf("dict: %v", err)
	}
	return dict, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#dir
func dir(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("dir does not accept keyword arguments")
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("dir: got %d arguments, want 1", len(args))
	}

	var names []string
	if x, ok := args[0].(HasAttrs); ok {
		names = x.AttrNames()
	}
	elems := make([]Value, len(names))
	for i, name := range names {
		elems[i] = String(name)
	}
	return NewList(elems), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#enumerate
func enumerate(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var iterable Iterable
	var start int
	if err := UnpackPositionalArgs("enumerate", args, kwargs, 1, &iterable, &start); err != nil {
		return nil, err
	}

	iter := iterable.Iterate()
	if iter == nil {
		return nil, fmt.Errorf("enumerate: got %s, want iterable", iterable.Type())
	}
	defer iter.Done()

	var pairs []Value
	var x Value

	if n := Len(iterable); n >= 0 {
		// common case: known length
		pairs = make([]Value, 0, n)
		array := make(Tuple, 2*n) // allocate a single backing array
		for i := 0; iter.Next(&x); i++ {
			pair := array[:2:2]
			array = array[2:]
			pair[0] = MakeInt(start + i)
			pair[1] = x
			pairs = append(pairs, pair)
		}
	} else {
		// non-sequence (unknown length)
		for i := 0; iter.Next(&x); i++ {
			pair := Tuple{MakeInt(start + i), x}
			pairs = append(pairs, pair)
		}
	}

	return NewList(pairs), nil
}

func float(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("float does not accept keyword arguments")
	}
	if len(args) == 0 {
		return Float(0.0), nil
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("float got %d arguments, wants 1", len(args))
	}
	switch x := args[0].(type) {
	case Bool:
		if x {
			return Float(1.0), nil
		} else {
			return Float(0.0), nil
		}
	case Int:
		return x.Float(), nil
	case Float:
		return x, nil
	case String:
		f, err := strconv.ParseFloat(string(x), 64)
		if err != nil {
			return nil, err
		}
		return Float(f), nil
	default:
		return nil, fmt.Errorf("float got %s, want number or string", x.Type())
	}
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#getattr
func getattr(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var object, dflt Value
	var name string
	if err := UnpackPositionalArgs("getattr", args, kwargs, 2, &object, &name, &dflt); err != nil {
		return nil, err
	}
	if object, ok := object.(HasAttrs); ok {
		v, err := object.Attr(name)
		if err != nil {
			// An error could mean the field doesn't exist,
			// or it exists but could not be computed.
			if dflt != nil {
				return dflt, nil
			}
			return nil, err
		}
		if v != nil {
			return v, nil
		}
		// (nil, nil) => no such field
	}
	if dflt != nil {
		return dflt, nil
	}
	return nil, fmt.Errorf("%s has no .%s field or method", object.Type(), name)
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#hasattr
func hasattr(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var object Value
	var name string
	if err := UnpackPositionalArgs("hasattr", args, kwargs, 2, &object, &name); err != nil {
		return nil, err
	}
	if object, ok := object.(HasAttrs); ok {
		v, err := object.Attr(name)
		if err == nil {
			return Bool(v != nil), nil
		}

		// An error does not conclusively indicate presence or
		// absence of a field: it could occur while computing
		// the value of a present attribute, or it could be a
		// "no such attribute" error with details.
		for _, x := range object.AttrNames() {
			if x == name {
				return True, nil
			}
		}
	}
	return False, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#hash
func hash(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var x Value
	if err := UnpackPositionalArgs("hash", args, kwargs, 1, &x); err != nil {
		return nil, err
	}
	h, err := x.Hash()
	return MakeUint(uint(h)), err
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#int
func int_(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var x Value = zero
	var base Value
	if err := UnpackArgs("int", args, kwargs, "x", &x, "base?", &base); err != nil {
		return nil, err
	}

	// "If x is not a number or base is given, x must be a string."
	if s, ok := AsString(x); ok {
		b := 10
		if base != nil {
			var err error
			b, err = AsInt32(base)
			if err != nil || b != 0 && (b < 2 || b > 36) {
				return nil, fmt.Errorf("int: base must be an integer >= 2 && <= 36")
			}
		}

		orig := s // save original for error message

		// remove sign
		var neg bool
		if s != "" {
			if s[0] == '+' {
				s = s[1:]
			} else if s[0] == '-' {
				neg = true
				s = s[1:]
			}
		}

		// remove base prefix
		baseprefix := 0
		if len(s) > 1 && s[0] == '0' {
			if len(s) > 2 {
				switch s[1] {
				case 'o', 'O':
					s = s[2:]
					baseprefix = 8
				case 'x', 'X':
					s = s[2:]
					baseprefix = 16
				case 'b', 'B':
					s = s[2:]
					baseprefix = 2
				}
			}

			// For automatic base detection,
			// a string starting with zero
			// must be all zeros.
			// Thus we reject int("0755", 0).
			if baseprefix == 0 && b == 0 {
				for i := 1; i < len(s); i++ {
					if s[i] != '0' {
						goto invalid
					}
				}
				return zero, nil
			}

			if b != 0 && baseprefix != 0 && baseprefix != b {
				// Explicit base doesn't match prefix,
				// e.g. int("0o755", 16).
				goto invalid
			}
		}

		// select base
		if b == 0 {
			if baseprefix != 0 {
				b = baseprefix
			} else {
				b = 10
			}
		}

		// we explicitly handled sign above.
		// if a sign remains, it is invalid.
		if s != "" && (s[0] == '-' || s[0] == '+') {
			goto invalid
		}

		// s has no sign or base prefix.
		//
		// int(x) permits arbitrary precision, unlike the scanner.
		if i, ok := new(big.Int).SetString(s, b); ok {
			res := MakeBigInt(i)
			if neg {
				res = zero.Sub(res)
			}
			return res, nil
		}

	invalid:
		return nil, fmt.Errorf("int: invalid literal with base %d: %s", b, orig)
	}

	if base != nil {
		return nil, fmt.Errorf("int: can't convert non-string with explicit base")
	}

	if b, ok := x.(Bool); ok {
		if b {
			return one, nil
		} else {
			return zero, nil
		}
	}

	i, err := NumberToInt(x)
	if err != nil {
		return nil, fmt.Errorf("int: %s", err)
	}
	return i, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#len
func len_(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var x Value
	if err := UnpackPositionalArgs("len", args, kwargs, 1, &x); err != nil {
		return nil, err
	}
	len := Len(x)
	if len < 0 {
		return nil, fmt.Errorf("value of type %s has no len", x.Type())
	}
	return MakeInt(len), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#list
func list(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var iterable Iterable
	if err := UnpackPositionalArgs("list", args, kwargs, 0, &iterable); err != nil {
		return nil, err
	}
	var elems []Value
	if iterable != nil {
		iter := iterable.Iterate()
		defer iter.Done()
		if n := Len(iterable); n > 0 {
			elems = make([]Value, 0, n) // preallocate if length known
		}
		var x Value
		for iter.Next(&x) {
			elems = append(elems, x)
		}
	}
	return NewList(elems), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#min
func minmax(thread *Thread, fn *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("%s requires at least one positional argument", fn.Name())
	}
	var keyFunc Callable
	if err := UnpackArgs(fn.Name(), nil, kwargs, "key?", &keyFunc); err != nil {
		return nil, err
	}
	var op syntax.Token
	if fn.Name() == "max" {
		op = syntax.GT
	} else {
		op = syntax.LT
	}
	var iterable Value
	if len(args) == 1 {
		iterable = args[0]
	} else {
		iterable = args
	}
	iter := Iterate(iterable)
	if iter == nil {
		return nil, fmt.Errorf("%s: %s value is not iterable", fn.Name(), iterable.Type())
	}
	defer iter.Done()
	var extremum Value
	if !iter.Next(&extremum) {
		return nil, fmt.Errorf("%s: argument is an empty sequence", fn.Name())
	}

	var extremeKey Value
	var keyargs Tuple
	if keyFunc == nil {
		extremeKey = extremum
	} else {
		keyargs = Tuple{extremum}
		res, err := Call(thread, keyFunc, keyargs, nil)
		if err != nil {
			return nil, err
		}
		extremeKey = res
	}

	var x Value
	for iter.Next(&x) {
		var key Value
		if keyFunc == nil {
			key = x
		} else {
			keyargs[0] = x
			res, err := Call(thread, keyFunc, keyargs, nil)
			if err != nil {
				return nil, err
			}
			key = res
		}

		if ok, err := Compare(op, key, extremeKey); err != nil {
			return nil, err
		} else if ok {
			extremum = x
			extremeKey = key
		}
	}
	return extremum, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#ord
func ord(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("ord does not accept keyword arguments")
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("ord: got %d arguments, want 1", len(args))
	}
	s, ok := AsString(args[0])
	if !ok {
		return nil, fmt.Errorf("ord: got %s, want string", args[0].Type())
	}
	r, sz := utf8.DecodeRuneInString(s)
	if sz == 0 || sz != len(s) {
		n := utf8.RuneCountInString(s)
		return nil, fmt.Errorf("ord: string encodes %d Unicode code points, want 1", n)
	}
	return MakeInt(int(r)), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#print
func print(thread *Thread, fn *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	sep := " "
	if err := UnpackArgs("print", nil, kwargs, "sep?", &sep); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	for i, v := range args {
		if i > 0 {
			buf.WriteString(sep)
		}
		if s, ok := AsString(v); ok {
			buf.WriteString(s)
		} else {
			writeValue(&buf, v, nil)
		}
	}

	if thread.Print != nil {
		thread.Print(thread, buf.String())
	} else {
		fmt.Fprintln(os.Stderr, &buf)
	}
	return None, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#range
func range_(thread *Thread, fn *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var start, stop, step int
	step = 1
	if err := UnpackPositionalArgs("range", args, kwargs, 1, &start, &stop, &step); err != nil {
		return nil, err
	}

	// TODO(adonovan): analyze overflow/underflows cases for 32-bit implementations.
	if len(args) == 1 {
		// range(stop)
		start, stop = 0, start
	}
	if step == 0 {
		// we were given range(start, stop, 0)
		return nil, fmt.Errorf("range: step argument must not be zero")
	}

	return rangeValue{start: start, stop: stop, step: step, len: rangeLen(start, stop, step)}, nil
}

// A rangeValue is a comparable, immutable, indexable sequence of integers
// defined by the three parameters to a range(...) call.
// Invariant: step != 0.
type rangeValue struct{ start, stop, step, len int }

var (
	_ Indexable  = rangeValue{}
	_ Sequence   = rangeValue{}
	_ Comparable = rangeValue{}
	_ Sliceable  = rangeValue{}
)

func (r rangeValue) Len() int          { return r.len }
func (r rangeValue) Index(i int) Value { return MakeInt(r.start + i*r.step) }
func (r rangeValue) Iterate() Iterator { return &rangeIterator{r, 0} }

// rangeLen calculates the length of a range with the provided start, stop, and step.
// caller must ensure that step is non-zero.
func rangeLen(start, stop, step int) int {
	switch {
	case step > 0:
		if stop > start {
			return (stop-1-start)/step + 1
		}
	case step < 0:
		if start > stop {
			return (start-1-stop)/-step + 1
		}
	default:
		panic("rangeLen: zero step")
	}
	return 0
}

func (r rangeValue) Slice(start, end, step int) Value {
	newStart := r.start + r.step*start
	newStop := r.start + r.step*end
	newStep := r.step * step
	return rangeValue{
		start: newStart,
		stop:  newStop,
		step:  newStep,
		len:   rangeLen(newStart, newStop, newStep),
	}
}

func (r rangeValue) Freeze() {} // immutable
func (r rangeValue) String() string {
	if r.step != 1 {
		return fmt.Sprintf("range(%d, %d, %d)", r.start, r.stop, r.step)
	} else if r.start != 0 {
		return fmt.Sprintf("range(%d, %d)", r.start, r.stop)
	} else {
		return fmt.Sprintf("range(%d)", r.stop)
	}
}
func (r rangeValue) Type() string          { return "range" }
func (r rangeValue) Truth() Bool           { return r.len > 0 }
func (r rangeValue) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable: range") }

func (x rangeValue) CompareSameType(op syntax.Token, y_ Value, depth int) (bool, error) {
	y := y_.(rangeValue)
	switch op {
	case syntax.EQL:
		return rangeEqual(x, y), nil
	case syntax.NEQ:
		return !rangeEqual(x, y), nil
	default:
		return false, fmt.Errorf("%s %s %s not implemented", x.Type(), op, y.Type())
	}
}

func rangeEqual(x, y rangeValue) bool {
	// Two ranges compare equal if they denote the same sequence.
	if x.len != y.len {
		return false // sequences differ in length
	}
	if x.len == 0 {
		return true // both sequences are empty
	}
	if x.start != y.start {
		return false // first element differs
	}
	return x.len == 1 || x.step == y.step
}

func (r rangeValue) contains(x Int) bool {
	x32, err := AsInt32(x)
	if err != nil {
		return false // out of range
	}
	delta := x32 - r.start
	quo, rem := delta/r.step, delta%r.step
	return rem == 0 && 0 <= quo && quo < r.len
}

type rangeIterator struct {
	r rangeValue
	i int
}

func (it *rangeIterator) Next(p *Value) bool {
	if it.i < it.r.len {
		*p = it.r.Index(it.i)
		it.i++
		return true
	}
	return false
}
func (*rangeIterator) Done() {}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#repr
func repr(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var x Value
	if err := UnpackPositionalArgs("repr", args, kwargs, 1, &x); err != nil {
		return nil, err
	}
	return String(x.String()), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#reversed
func reversed(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var iterable Iterable
	if err := UnpackPositionalArgs("reversed", args, kwargs, 1, &iterable); err != nil {
		return nil, err
	}
	iter := iterable.Iterate()
	defer iter.Done()
	var elems []Value
	if n := Len(args[0]); n >= 0 {
		elems = make([]Value, 0, n) // preallocate if length known
	}
	var x Value
	for iter.Next(&x) {
		elems = append(elems, x)
	}
	n := len(elems)
	for i := 0; i < n>>1; i++ {
		elems[i], elems[n-1-i] = elems[n-1-i], elems[i]
	}
	return NewList(elems), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#set
func set(thread *Thread, fn *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var iterable Iterable
	if err := UnpackPositionalArgs("set", args, kwargs, 0, &iterable); err != nil {
		return nil, err
	}
	set := new(Set)
	if iterable != nil {
		iter := iterable.Iterate()
		defer iter.Done()
		var x Value
		for iter.Next(&x) {
			if err := set.Insert(x); err != nil {
				return nil, err
			}
		}
	}
	return set, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#sorted
func sorted(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var iterable Iterable
	var key Callable
	var reverse bool
	if err := UnpackArgs("sorted", args, kwargs,
		"iterable", &iterable,
		"key?", &key,
		"reverse?", &reverse,
	); err != nil {
		return nil, err
	}

	iter := iterable.Iterate()
	defer iter.Done()
	var values []Value
	if n := Len(iterable); n > 0 {
		values = make(Tuple, 0, n) // preallocate if length is known
	}
	var x Value
	for iter.Next(&x) {
		values = append(values, x)
	}

	// Derive keys from values by applying key function.
	var keys []Value
	if key != nil {
		keys = make([]Value, len(values))
		for i, v := range values {
			k, err := Call(thread, key, Tuple{v}, nil)
			if err != nil {
				return nil, err // to preserve backtrace, don't modify error
			}
			keys[i] = k
		}
	}

	slice := &sortSlice{keys: keys, values: values}
	if reverse {
		sort.Stable(sort.Reverse(slice))
	} else {
		sort.Stable(slice)
	}
	return NewList(slice.values), slice.err
}

type sortSlice struct {
	keys   []Value // nil => values[i] is key
	values []Value
	err    error
}

func (s *sortSlice) Len() int { return len(s.values) }
func (s *sortSlice) Less(i, j int) bool {
	keys := s.keys
	if s.keys == nil {
		keys = s.values
	}
	ok, err := Compare(syntax.LT, keys[i], keys[j])
	if err != nil {
		s.err = err
	}
	return ok
}
func (s *sortSlice) Swap(i, j int) {
	if s.keys != nil {
		s.keys[i], s.keys[j] = s.keys[j], s.keys[i]
	}
	s.values[i], s.values[j] = s.values[j], s.values[i]
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#str
func str(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("str does not accept keyword arguments")
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("str: got %d arguments, want exactly 1", len(args))
	}
	x := args[0]
	if _, ok := AsString(x); !ok {
		x = String(x.String())
	}
	return x, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#tuple
func tuple(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	var iterable Iterable
	if err := UnpackPositionalArgs("tuple", args, kwargs, 0, &iterable); err != nil {
		return nil, err
	}
	if len(args) == 0 {
		return Tuple(nil), nil
	}
	iter := iterable.Iterate()
	defer iter.Done()
	var elems Tuple
	if n := Len(iterable); n > 0 {
		elems = make(Tuple, 0, n) // preallocate if length is known
	}
	var x Value
	for iter.Next(&x) {
		elems = append(elems, x)
	}
	return elems, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#type
func type_(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("type does not accept keyword arguments")
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("type: got %d arguments, want exactly 1", len(args))
	}
	return String(args[0].Type()), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#zip
func zip(thread *Thread, _ *Builtin, args Tuple, kwargs []Tuple) (Value, error) {
	if len(kwargs) > 0 {
		return nil, fmt.Errorf("zip does not accept keyword arguments")
	}
	rows, cols := 0, len(args)
	iters := make([]Iterator, cols)
	defer func() {
		for _, iter := range iters {
			if iter != nil {
				iter.Done()
			}
		}
	}()
	for i, seq := range args {
		it := Iterate(seq)
		if it == nil {
			return nil, fmt.Errorf("zip: argument #%d is not iterable: %s", i+1, seq.Type())
		}
		iters[i] = it
		n := Len(seq)
		if i == 0 || n < rows {
			rows = n // possibly -1
		}
	}
	var result []Value
	if rows >= 0 {
		// length known
		result = make([]Value, rows)
		array := make(Tuple, cols*rows) // allocate a single backing array
		for i := 0; i < rows; i++ {
			tuple := array[:cols:cols]
			array = array[cols:]
			for j, iter := range iters {
				iter.Next(&tuple[j])
			}
			result[i] = tuple
		}
	} else {
		// length not known
	outer:
		for {
			tuple := make(Tuple, cols)
			for i, iter := range iters {
				if !iter.Next(&tuple[i]) {
					break outer
				}
			}
			result = append(result, tuple)
		}
	}
	return NewList(result), nil
}

// ---- methods of built-in types ---

// https://github.com/google/starlark-go/blob/master/doc/spec.md#dict·get
func dict_get(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	var key, dflt Value
	if err := UnpackPositionalArgs(fnname, args, kwargs, 1, &key, &dflt); err != nil {
		return nil, err
	}
	if v, ok, err := recv.(*Dict).Get(key); err != nil {
		return nil, err
	} else if ok {
		return v, nil
	} else if dflt != nil {
		return dflt, nil
	}
	return None, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#dict·clear
func dict_clear(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	return None, recv.(*Dict).Clear()
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#dict·items
func dict_items(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	items := recv.(*Dict).Items()
	res := make([]Value, len(items))
	for i, item := range items {
		res[i] = item // convert [2]Value to Value
	}
	return NewList(res), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#dict·keys
func dict_keys(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	return NewList(recv.(*Dict).Keys()), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#dict·pop
func dict_pop(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	recv := recv_.(*Dict)
	var k, d Value
	if err := UnpackPositionalArgs(fnname, args, kwargs, 1, &k, &d); err != nil {
		return nil, err
	}
	if v, found, err := recv.Delete(k); err != nil {
		return nil, err // dict is frozen or key is unhashable
	} else if found {
		return v, nil
	} else if d != nil {
		return d, nil
	}
	return nil, fmt.Errorf("pop: missing key")
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#dict·popitem
func dict_popitem(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	recv := recv_.(*Dict)
	k, ok := recv.ht.first()
	if !ok {
		return nil, fmt.Errorf("popitem: empty dict")
	}
	v, _, err := recv.Delete(k)
	if err != nil {
		return nil, err // dict is frozen
	}
	return Tuple{k, v}, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#dict·setdefault
func dict_setdefault(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	var key, dflt Value = nil, None
	if err := UnpackPositionalArgs(fnname, args, kwargs, 1, &key, &dflt); err != nil {
		return nil, err
	}
	dict := recv.(*Dict)
	if v, ok, err := dict.Get(key); err != nil {
		return nil, err
	} else if ok {
		return v, nil
	} else {
		return dflt, dict.SetKey(key, dflt)
	}
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#dict·update
func dict_update(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("update: got %d arguments, want at most 1", len(args))
	}
	if err := updateDict(recv.(*Dict), args, kwargs); err != nil {
		return nil, fmt.Errorf("update: %v", err)
	}
	return None, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#dict·update
func dict_values(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	items := recv.(*Dict).Items()
	res := make([]Value, len(items))
	for i, item := range items {
		res[i] = item[1]
	}
	return NewList(res), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#list·append
func list_append(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	recv := recv_.(*List)
	var object Value
	if err := UnpackPositionalArgs(fnname, args, kwargs, 1, &object); err != nil {
		return nil, err
	}
	if err := recv.checkMutable("append to"); err != nil {
		return nil, err
	}
	recv.elems = append(recv.elems, object)
	return None, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#list·clear
func list_clear(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	return None, recv_.(*List).Clear()
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#list·extend
func list_extend(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	recv := recv_.(*List)
	var iterable Iterable
	if err := UnpackPositionalArgs(fnname, args, kwargs, 1, &iterable); err != nil {
		return nil, err
	}
	if err := recv.checkMutable("extend"); err != nil {
		return nil, err
	}
	listExtend(recv, iterable)
	return None, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#list·index
func list_index(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	recv := recv_.(*List)
	var value, start_, end_ Value
	if err := UnpackPositionalArgs(fnname, args, kwargs, 1, &value, &start_, &end_); err != nil {
		return nil, err
	}

	start, end, err := indices(start_, end_, recv.Len())
	if err != nil {
		return nil, fmt.Errorf("%s: %s", fnname, err)
	}

	for i := start; i < end; i++ {
		if eq, err := Equal(recv.elems[i], value); err != nil {
			return nil, fmt.Errorf("index: %s", err)
		} else if eq {
			return MakeInt(i), nil
		}
	}
	return nil, fmt.Errorf("index: value not in list")
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#list·insert
func list_insert(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	recv := recv_.(*List)
	var index int
	var object Value
	if err := UnpackPositionalArgs(fnname, args, kwargs, 2, &index, &object); err != nil {
		return nil, err
	}
	if err := recv.checkMutable("insert into"); err != nil {
		return nil, err
	}

	if index < 0 {
		index += recv.Len()
	}

	if index >= recv.Len() {
		// end
		recv.elems = append(recv.elems, object)
	} else {
		if index < 0 {
			index = 0 // start
		}
		recv.elems = append(recv.elems, nil)
		copy(recv.elems[index+1:], recv.elems[index:]) // slide up one
		recv.elems[index] = object
	}
	return None, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#list·remove
func list_remove(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	recv := recv_.(*List)
	var value Value
	if err := UnpackPositionalArgs(fnname, args, kwargs, 1, &value); err != nil {
		return nil, err
	}
	if err := recv.checkMutable("remove from"); err != nil {
		return nil, err
	}
	for i, elem := range recv.elems {
		if eq, err := Equal(elem, value); err != nil {
			return nil, fmt.Errorf("remove: %v", err)
		} else if eq {
			recv.elems = append(recv.elems[:i], recv.elems[i+1:]...)
			return None, nil
		}
	}
	return nil, fmt.Errorf("remove: element not found")
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#list·pop
func list_pop(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	list := recv.(*List)
	n := list.Len()
	i := n - 1
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0, &i); err != nil {
		return nil, err
	}
	origI := i
	if i < 0 {
		i += n
	}
	if i < 0 || i >= n {
		return nil, outOfRange(origI, n, list)
	}
	if err := list.checkMutable("pop from"); err != nil {
		return nil, err
	}
	res := list.elems[i]
	list.elems = append(list.elems[:i], list.elems[i+1:]...)
	return res, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·capitalize
func string_capitalize(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	s := string(recv.(String))
	var res bytes.Buffer
	res.Grow(len(s))
	for i, r := range s {
		if i == 0 {
			r = unicode.ToTitle(r)
		} else {
			r = unicode.ToLower(r)
		}
		res.WriteRune(r)
	}
	return String(res.String()), nil
}

// string_iterable returns an unspecified iterable value whose iterator yields:
// - elems: successive 1-byte substrings
// - codepoints: successive substrings that encode a single Unicode code point.
// - elem_ords: numeric values of successive bytes
// - codepoint_ords: numeric values of successive Unicode code points
func string_iterable(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	return stringIterable{
		s:          recv.(String),
		ords:       fnname[len(fnname)-2] == 'd',
		codepoints: fnname[0] == 'c',
	}, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·count
func string_count(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	recv := string(recv_.(String))

	var sub string
	var start_, end_ Value
	if err := UnpackPositionalArgs(fnname, args, kwargs, 1, &sub, &start_, &end_); err != nil {
		return nil, err
	}

	start, end, err := indices(start_, end_, len(recv))
	if err != nil {
		return nil, fmt.Errorf("%s: %s", fnname, err)
	}

	var slice string
	if start < end {
		slice = recv[start:end]
	}
	return MakeInt(strings.Count(slice, sub)), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·isalnum
func string_isalnum(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	recv := string(recv_.(String))
	for _, r := range recv {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return False, nil
		}
	}
	return Bool(recv != ""), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·isalpha
func string_isalpha(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	recv := string(recv_.(String))
	for _, r := range recv {
		if !unicode.IsLetter(r) {
			return False, nil
		}
	}
	return Bool(recv != ""), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·isdigit
func string_isdigit(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	recv := string(recv_.(String))
	for _, r := range recv {
		if !unicode.IsDigit(r) {
			return False, nil
		}
	}
	return Bool(recv != ""), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·islower
func string_islower(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	recv := string(recv_.(String))
	return Bool(isCasedString(recv) && recv == strings.ToLower(recv)), nil
}

// isCasedString reports whether its argument contains any cased code points.
func isCasedString(s string) bool {
	for _, r := range s {
		if isCasedRune(r) {
			return true
		}
	}
	return false
}

func isCasedRune(r rune) bool {
	// It's unclear what the correct behavior is for a rune such as 'ﬃ',
	// a lowercase letter with no upper or title case and no SimpleFold.
	return 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || unicode.SimpleFold(r) != r
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·isspace
func string_isspace(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	recv := string(recv_.(String))
	for _, r := range recv {
		if !unicode.IsSpace(r) {
			return False, nil
		}
	}
	return Bool(recv != ""), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·istitle
func string_istitle(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	recv := string(recv_.(String))

	// Python semantics differ from x==strings.{To,}Title(x) in Go:
	// "uppercase characters may only follow uncased characters and
	// lowercase characters only cased ones."
	var cased, prevCased bool
	for _, r := range recv {
		if 'A' <= r && r <= 'Z' || unicode.IsTitle(r) { // e.g. "ǅ"
			if prevCased {
				return False, nil
			}
			prevCased = true
			cased = true
		} else if unicode.IsLower(r) {
			if !prevCased {
				return False, nil
			}
			prevCased = true
			cased = true
		} else if unicode.IsUpper(r) {
			return False, nil
		} else {
			prevCased = false
		}
	}
	return Bool(cased), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·isupper
func string_isupper(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	recv := string(recv_.(String))
	return Bool(isCasedString(recv) && recv == strings.ToUpper(recv)), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·find
func string_find(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	return string_find_impl(fnname, string(recv.(String)), args, kwargs, true, false)
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·format
func string_format(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	format := string(recv_.(String))
	var auto, manual bool // kinds of positional indexing used
	var buf bytes.Buffer
	index := 0
	for {
		literal := format
		i := strings.IndexByte(format, '{')
		if i >= 0 {
			literal = format[:i]
		}

		// Replace "}}" with "}" in non-field portion, rejecting a lone '}'.
		for {
			j := strings.IndexByte(literal, '}')
			if j < 0 {
				buf.WriteString(literal)
				break
			}
			if len(literal) == j+1 || literal[j+1] != '}' {
				return nil, fmt.Errorf("single '}' in format")
			}
			buf.WriteString(literal[:j+1])
			literal = literal[j+2:]
		}

		if i < 0 {
			break // end of format string
		}

		if i+1 < len(format) && format[i+1] == '{' {
			// "{{" means a literal '{'
			buf.WriteByte('{')
			format = format[i+2:]
			continue
		}

		format = format[i+1:]
		i = strings.IndexByte(format, '}')
		if i < 0 {
			return nil, fmt.Errorf("unmatched '{' in format")
		}

		var arg Value
		conv := "s"
		var spec string

		field := format[:i]
		format = format[i+1:]

		var name string
		if i := strings.IndexByte(field, '!'); i < 0 {
			// "name" or "name:spec"
			if i := strings.IndexByte(field, ':'); i < 0 {
				name = field
			} else {
				name = field[:i]
				spec = field[i+1:]
			}
		} else {
			// "name!conv" or "name!conv:spec"
			name = field[:i]
			field = field[i+1:]
			// "conv" or "conv:spec"
			if i := strings.IndexByte(field, ':'); i < 0 {
				conv = field
			} else {
				conv = field[:i]
				spec = field[i+1:]
			}
		}

		if name == "" {
			// "{}": automatic indexing
			if manual {
				return nil, fmt.Errorf("cannot switch from manual field specification to automatic field numbering")
			}
			auto = true
			if index >= len(args) {
				return nil, fmt.Errorf("tuple index out of range")
			}
			arg = args[index]
			index++
		} else if num, ok := decimal(name); ok {
			// positional argument
			if auto {
				return nil, fmt.Errorf("cannot switch from automatic field numbering to manual field specification")
			}
			manual = true
			if num >= len(args) {
				return nil, fmt.Errorf("tuple index out of range")
			} else {
				arg = args[num]
			}
		} else {
			// keyword argument
			for _, kv := range kwargs {
				if string(kv[0].(String)) == name {
					arg = kv[1]
					break
				}
			}
			if arg == nil {
				// Starlark does not support Python's x.y or a[i] syntaxes,
				// or nested use of {...}.
				if strings.Contains(name, ".") {
					return nil, fmt.Errorf("attribute syntax x.y is not supported in replacement fields: %s", name)
				}
				if strings.Contains(name, "[") {
					return nil, fmt.Errorf("element syntax a[i] is not supported in replacement fields: %s", name)
				}
				if strings.Contains(name, "{") {
					return nil, fmt.Errorf("nested replacement fields not supported")
				}
				return nil, fmt.Errorf("keyword %s not found", name)
			}
		}

		if spec != "" {
			// Starlark does not support Python's format_spec features.
			return nil, fmt.Errorf("format spec features not supported in replacement fields: %s", spec)
		}

		switch conv {
		case "s":
			if str, ok := AsString(arg); ok {
				buf.WriteString(str)
			} else {
				writeValue(&buf, arg, nil)
			}
		case "r":
			writeValue(&buf, arg, nil)
		default:
			return nil, fmt.Errorf("unknown conversion %q", conv)
		}
	}
	return String(buf.String()), nil
}

// decimal interprets s as a sequence of decimal digits.
func decimal(s string) (x int, ok bool) {
	n := len(s)
	for i := 0; i < n; i++ {
		digit := s[i] - '0'
		if digit > 9 {
			return 0, false
		}
		x = x*10 + int(digit)
		if x < 0 {
			return 0, false // underflow
		}
	}
	return x, true
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·index
func string_index(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	return string_find_impl(fnname, string(recv.(String)), args, kwargs, false, false)
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·join
func string_join(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	recv := string(recv_.(String))
	var iterable Iterable
	if err := UnpackPositionalArgs(fnname, args, kwargs, 1, &iterable); err != nil {
		return nil, err
	}
	iter := iterable.Iterate()
	defer iter.Done()
	var buf bytes.Buffer
	var x Value
	for i := 0; iter.Next(&x); i++ {
		if i > 0 {
			buf.WriteString(recv)
		}
		s, ok := AsString(x)
		if !ok {
			return nil, fmt.Errorf("in list, want string, got %s", x.Type())
		}
		buf.WriteString(s)
	}
	return String(buf.String()), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·lower
func string_lower(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	return String(strings.ToLower(string(recv.(String)))), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·partition
func string_partition(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	recv := string(recv_.(String))
	var sep string
	if err := UnpackPositionalArgs(fnname, args, kwargs, 1, &sep); err != nil {
		return nil, err
	}
	if sep == "" {
		return nil, fmt.Errorf("%s: empty separator", fnname)
	}
	var i int
	if fnname[0] == 'p' {
		i = strings.Index(recv, sep) // partition
	} else {
		i = strings.LastIndex(recv, sep) // rpartition
	}
	tuple := make(Tuple, 0, 3)
	if i < 0 {
		if fnname[0] == 'p' {
			tuple = append(tuple, String(recv), String(""), String(""))
		} else {
			tuple = append(tuple, String(""), String(""), String(recv))
		}
	} else {
		tuple = append(tuple, String(recv[:i]), String(sep), String(recv[i+len(sep):]))
	}
	return tuple, nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·replace
func string_replace(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	recv := string(recv_.(String))
	var old, new string
	count := -1
	if err := UnpackPositionalArgs(fnname, args, kwargs, 2, &old, &new, &count); err != nil {
		return nil, err
	}
	return String(strings.Replace(recv, old, new, count)), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·rfind
func string_rfind(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	return string_find_impl(fnname, string(recv.(String)), args, kwargs, true, true)
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·rindex
func string_rindex(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	return string_find_impl(fnname, string(recv.(String)), args, kwargs, false, true)
}

// https://github.com/google/starlark-go/starlark/blob/master/doc/spec.md#string·startswith
// https://github.com/google/starlark-go/starlark/blob/master/doc/spec.md#string·endswith
func string_startswith(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	var x Value
	var start, end Value = None, None
	if err := UnpackPositionalArgs(fnname, args, kwargs, 1, &x, &start, &end); err != nil {
		return nil, err
	}

	// compute effective substring.
	s := string(recv_.(String))
	if start, end, err := indices(start, end, len(s)); err != nil {
		return nil, err
	} else {
		if end < start {
			end = start // => empty result
		}
		s = s[start:end]
	}

	f := strings.HasPrefix
	if fnname[0] == 'e' { // endswith
		f = strings.HasSuffix
	}

	switch x := x.(type) {
	case Tuple:
		for i, x := range x {
			prefix, ok := AsString(x)
			if !ok {
				return nil, fmt.Errorf("%s: want string, got %s, for element %d",
					fnname, x.Type(), i)
			}
			if f(s, prefix) {
				return True, nil
			}
		}
		return False, nil
	case String:
		return Bool(f(s, string(x))), nil
	}
	return nil, fmt.Errorf("%s: got %s, want string or tuple of string", fnname, x.Type())
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·strip
// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·lstrip
// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·rstrip
func string_strip(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	var chars string
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0, &chars); err != nil {
		return nil, err
	}
	recv := string(recv_.(String))
	var s string
	switch fnname[0] {
	case 's': // strip
		if chars != "" {
			s = strings.Trim(recv, chars)
		} else {
			s = strings.TrimSpace(recv)
		}
	case 'l': // lstrip
		if chars != "" {
			s = strings.TrimLeft(recv, chars)
		} else {
			s = strings.TrimLeftFunc(recv, unicode.IsSpace)
		}
	case 'r': // rstrip
		if chars != "" {
			s = strings.TrimRight(recv, chars)
		} else {
			s = strings.TrimRightFunc(recv, unicode.IsSpace)
		}
	}
	return String(s), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·title
func string_title(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}

	s := string(recv.(String))

	// Python semantics differ from x==strings.{To,}Title(x) in Go:
	// "uppercase characters may only follow uncased characters and
	// lowercase characters only cased ones."
	var buf bytes.Buffer
	buf.Grow(len(s))
	var prevCased bool
	for _, r := range s {
		if prevCased {
			r = unicode.ToLower(r)
		} else {
			r = unicode.ToTitle(r)
		}
		prevCased = isCasedRune(r)
		buf.WriteRune(r)
	}
	return String(buf.String()), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·upper
func string_upper(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0); err != nil {
		return nil, err
	}
	return String(strings.ToUpper(string(recv.(String)))), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·split
// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·rsplit
func string_split(fnname string, recv_ Value, args Tuple, kwargs []Tuple) (Value, error) {
	recv := string(recv_.(String))
	var sep_ Value
	maxsplit := -1
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0, &sep_, &maxsplit); err != nil {
		return nil, err
	}

	var res []string

	if sep_ == nil || sep_ == None {
		// special case: split on whitespace
		if maxsplit < 0 {
			res = strings.Fields(recv)
		} else if fnname == "split" {
			res = splitspace(recv, maxsplit)
		} else { // rsplit
			res = rsplitspace(recv, maxsplit)
		}

	} else if sep, ok := AsString(sep_); ok {
		if sep == "" {
			return nil, fmt.Errorf("split: empty separator")
		}
		// usual case: split on non-empty separator
		if maxsplit < 0 {
			res = strings.Split(recv, sep)
		} else if fnname == "split" {
			res = strings.SplitN(recv, sep, maxsplit+1)
		} else { // rsplit
			res = strings.Split(recv, sep)
			if excess := len(res) - maxsplit; excess > 0 {
				res[0] = strings.Join(res[:excess], sep)
				res = append(res[:1], res[excess:]...)
			}
		}

	} else {
		return nil, fmt.Errorf("split: got %s for separator, want string", sep_.Type())
	}

	list := make([]Value, len(res))
	for i, x := range res {
		list[i] = String(x)
	}
	return NewList(list), nil
}

// Precondition: max >= 0.
func rsplitspace(s string, max int) []string {
	res := make([]string, 0, max+1)
	end := -1 // index of field end, or -1 in a region of spaces.
	for i := len(s); i > 0; {
		r, sz := utf8.DecodeLastRuneInString(s[:i])
		if unicode.IsSpace(r) {
			if end >= 0 {
				if len(res) == max {
					break // let this field run to the start
				}
				res = append(res, s[i:end])
				end = -1
			}
		} else if end < 0 {
			end = i
		}
		i -= sz
	}
	if end >= 0 {
		res = append(res, s[:end])
	}

	resLen := len(res)
	for i := 0; i < resLen/2; i++ {
		res[i], res[resLen-1-i] = res[resLen-1-i], res[i]
	}

	return res
}

// Precondition: max >= 0.
func splitspace(s string, max int) []string {
	var res []string
	start := -1 // index of field start, or -1 in a region of spaces
	for i, r := range s {
		if unicode.IsSpace(r) {
			if start >= 0 {
				if len(res) == max {
					break // let this field run to the end
				}
				res = append(res, s[start:i])
				start = -1
			}
		} else if start == -1 {
			start = i
		}
	}
	if start >= 0 {
		res = append(res, s[start:])
	}
	return res
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#string·splitlines
func string_splitlines(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	var keepends bool
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0, &keepends); err != nil {
		return nil, err
	}
	var lines []string
	if s := string(recv.(String)); s != "" {
		// TODO(adonovan): handle CRLF correctly.
		if keepends {
			lines = strings.SplitAfter(s, "\n")
		} else {
			lines = strings.Split(s, "\n")
		}
		if strings.HasSuffix(s, "\n") {
			lines = lines[:len(lines)-1]
		}
	}
	list := make([]Value, len(lines))
	for i, x := range lines {
		list[i] = String(x)
	}
	return NewList(list), nil
}

// https://github.com/google/starlark-go/blob/master/doc/spec.md#set·union.
func set_union(fnname string, recv Value, args Tuple, kwargs []Tuple) (Value, error) {
	var iterable Iterable
	if err := UnpackPositionalArgs(fnname, args, kwargs, 0, &iterable); err != nil {
		return nil, err
	}
	iter := iterable.Iterate()
	defer iter.Done()
	union, err := recv.(*Set).Union(iter)
	if err != nil {
		return nil, fmt.Errorf("union: %v", err)
	}
	return union, nil
}

// Common implementation of string_{r}{find,index}.
func string_find_impl(fnname string, s string, args Tuple, kwargs []Tuple, allowError, last bool) (Value, error) {
	var sub string
	var start_, end_ Value
	if err := UnpackPositionalArgs(fnname, args, kwargs, 1, &sub, &start_, &end_); err != nil {
		return nil, err
	}

	start, end, err := indices(start_, end_, len(s))
	if err != nil {
		return nil, fmt.Errorf("%s: %s", fnname, err)
	}
	var slice string
	if start < end {
		slice = s[start:end]
	}

	var i int
	if last {
		i = strings.LastIndex(slice, sub)
	} else {
		i = strings.Index(slice, sub)
	}
	if i < 0 {
		if !allowError {
			return nil, fmt.Errorf("substring not found")
		}
		return MakeInt(-1), nil
	}
	return MakeInt(i + start), nil
}

// Common implementation of builtin dict function and dict.update method.
// Precondition: len(updates) == 0 or 1.
func updateDict(dict *Dict, updates Tuple, kwargs []Tuple) error {
	if len(updates) == 1 {
		switch updates := updates[0].(type) {
		case IterableMapping:
			// Iterate over dict's key/value pairs, not just keys.
			for _, item := range updates.Items() {
				if err := dict.SetKey(item[0], item[1]); err != nil {
					return err // dict is frozen
				}
			}
		default:
			// all other sequences
			iter := Iterate(updates)
			if iter == nil {
				return fmt.Errorf("got %s, want iterable", updates.Type())
			}
			defer iter.Done()
			var pair Value
			for i := 0; iter.Next(&pair); i++ {
				iter2 := Iterate(pair)
				if iter2 == nil {
					return fmt.Errorf("dictionary update sequence element #%d is not iterable (%s)", i, pair.Type())

				}
				defer iter2.Done()
				len := Len(pair)
				if len < 0 {
					return fmt.Errorf("dictionary update sequence element #%d has unknown length (%s)", i, pair.Type())
				} else if len != 2 {
					return fmt.Errorf("dictionary update sequence element #%d has length %d, want 2", i, len)
				}
				var k, v Value
				iter2.Next(&k)
				iter2.Next(&v)
				if err := dict.SetKey(k, v); err != nil {
					return err
				}
			}
		}
	}

	// Then add the kwargs.
	before := dict.Len()
	for _, pair := range kwargs {
		if err := dict.SetKey(pair[0], pair[1]); err != nil {
			return err // dict is frozen
		}
	}
	// In the common case, each kwarg will add another dict entry.
	// If that's not so, check whether it is because there was a duplicate kwarg.
	if dict.Len() < before+len(kwargs) {
		keys := make(map[String]bool, len(kwargs))
		for _, kv := range kwargs {
			k := kv[0].(String)
			if keys[k] {
				return fmt.Errorf("duplicate keyword arg: %v", k)
			}
			keys[k] = true
		}
	}

	return nil
}
