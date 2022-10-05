package starbind

// Code in this file is derived from go.starlark.net/repl/repl.go
// Which is licensed under the following copyright:
//
// Copyright (c) 2017 The Bazel Authors.  All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are
// met:
//
// 1. Redistributions of source code must retain the above copyright
//    notice, this list of conditions and the following disclaimer.
//
// 2. Redistributions in binary form must reproduce the above copyright
//    notice, this list of conditions and the following disclaimer in the
//    documentation and/or other materials provided with the
//    distribution.
//
// 3. Neither the name of the copyright holder nor the names of its
//    contributors may be used to endorse or promote products derived
//    from this software without specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
// HOLDER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
// SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
// LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
// DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
// THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
// (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

import (
	"fmt"
	"io"
	"os"

	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"github.com/go-delve/liner"
)

// REPL executes a read, eval, print loop.
func (env *Env) REPL() error {
	thread := env.newThread()
	globals := starlark.StringDict{}
	for k, v := range env.env {
		globals[k] = v
	}

	rl := liner.NewLiner()
	defer rl.Close()
	for {
		if err := isCancelled(thread); err != nil {
			return err
		}
		if err := rep(rl, thread, globals, env.out); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}
	fmt.Fprintln(env.out)
	return env.exportGlobals(globals)
}

const (
	normalPrompt = ">>> "
	extraPrompt  = "... "

	exitCommand = "exit"
)

// rep reads, evaluates, and prints one item.
//
// It returns an error (possibly readline.ErrInterrupt)
// only if readline failed. Starlark errors are printed.
func rep(rl *liner.State, thread *starlark.Thread, globals starlark.StringDict, out EchoWriter) error {
	defer out.Flush()
	eof := false

	prompt := normalPrompt
	readline := func() ([]byte, error) {
		line, err := rl.Prompt(prompt)
		out.Echo(prompt + line)
		if line == exitCommand {
			eof = true
			return nil, io.EOF
		}
		rl.AppendHistory(line)
		prompt = extraPrompt
		if err != nil {
			if err == io.EOF {
				eof = true
			}
			return nil, err
		}
		return []byte(line + "\n"), nil
	}

	// parse
	f, err := syntax.ParseCompoundStmt("<stdin>", readline)
	if err != nil {
		if eof {
			return io.EOF
		}
		printError(err)
		return nil
	}

	if expr := soleExpr(f); expr != nil {
		//TODO: check for 'exit'
		// eval
		v, err := starlark.EvalExpr(thread, expr, globals)
		if err != nil {
			printError(err)
			return nil
		}

		// print
		if v != starlark.None {
			fmt.Fprintln(out, v)
		}
	} else {
		// compile
		prog, err := starlark.FileProgram(f, globals.Has)
		if err != nil {
			printError(err)
			return nil
		}

		// execute (but do not freeze)
		res, err := prog.Init(thread, globals)
		if err != nil {
			printError(err)
		}

		// The global names from the previous call become
		// the predeclared names of this call.
		// If execution failed, some globals may be undefined.
		for k, v := range res {
			globals[k] = v
		}
	}

	return nil
}

func soleExpr(f *syntax.File) syntax.Expr {
	if len(f.Stmts) == 1 {
		if stmt, ok := f.Stmts[0].(*syntax.ExprStmt); ok {
			return stmt.X
		}
	}
	return nil
}

// printError prints the error to stderr,
// or its backtrace if it is a Starlark evaluation error.
func printError(err error) {
	if evalErr, ok := err.(*starlark.EvalError); ok {
		fmt.Fprintln(os.Stderr, evalErr.Backtrace())
	} else {
		fmt.Fprintln(os.Stderr, err)
	}
}

// MakeLoad returns a simple sequential implementation of module loading
// suitable for use in the REPL.
// Each function returned by MakeLoad accesses a distinct private cache.
func MakeLoad() func(thread *starlark.Thread, module string) (starlark.StringDict, error) {
	type entry struct {
		globals starlark.StringDict
		err     error
	}

	var cache = make(map[string]*entry)

	return func(thread *starlark.Thread, module string) (starlark.StringDict, error) {
		e, ok := cache[module]
		if e == nil {
			if ok {
				// request for package whose loading is in progress
				return nil, fmt.Errorf("cycle in load graph")
			}

			// Add a placeholder to indicate "load in progress".
			cache[module] = nil

			// Load it.
			thread := &starlark.Thread{Name: "exec " + module, Load: thread.Load}
			globals, err := starlark.ExecFile(thread, module, nil, nil)
			e = &entry{globals, err}

			// Update the cache.
			cache[module] = e
		}
		return e.globals, e.err
	}
}
