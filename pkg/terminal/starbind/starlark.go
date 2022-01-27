package starbind

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"runtime"
	"strings"
	"sync"

	"go.starlark.net/resolve"
	"go.starlark.net/starlark"

	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
)

//go:generate go run ../../../_scripts/gen-starlark-bindings.go go ./starlark_mapping.go
//go:generate go run ../../../_scripts/gen-starlark-bindings.go doc ../../../Documentation/cli/starlark.md

const (
	dlvCommandBuiltinName        = "dlv_command"
	readFileBuiltinName          = "read_file"
	writeFileBuiltinName         = "write_file"
	commandPrefix                = "command_"
	dlvContextName               = "dlv_context"
	curScopeBuiltinName          = "cur_scope"
	defaultLoadConfigBuiltinName = "default_load_config"
)

func init() {
	resolve.AllowNestedDef = true
	resolve.AllowLambda = true
	resolve.AllowFloat = true
	resolve.AllowSet = true
	resolve.AllowBitwise = true
	resolve.AllowRecursion = true
	resolve.AllowGlobalReassign = true
}

// Context is the context in which starlark scripts are evaluated.
// It contains methods to call API functions, command line commands, etc.
type Context interface {
	Client() service.Client
	RegisterCommand(name, helpMsg string, cmdfn func(args string) error)
	CallCommand(cmdstr string) error
	Scope() api.EvalScope
	LoadConfig() api.LoadConfig
}

// Env is the environment used to evaluate starlark scripts.
type Env struct {
	env       starlark.StringDict
	contextMu sync.Mutex
	thread    *starlark.Thread
	cancelfn  context.CancelFunc

	ctx Context
	out EchoWriter
}

// New creates a new starlark binding environment.
func New(ctx Context, out EchoWriter) *Env {
	env := &Env{}

	env.ctx = ctx
	env.out = out

	env.env = env.starlarkPredeclare()
	env.env[dlvCommandBuiltinName] = starlark.NewBuiltin(dlvCommandBuiltinName, func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, err
		}
		argstrs := make([]string, len(args))
		for i := range args {
			a, ok := args[i].(starlark.String)
			if !ok {
				return nil, fmt.Errorf("argument of dlv_command is not a string")
			}
			argstrs[i] = string(a)
		}
		err := env.ctx.CallCommand(strings.Join(argstrs, " "))
		if err != nil && strings.Contains(err.Error(), " has exited with status ") {
			return env.interfaceToStarlarkValue(err), nil
		}
		return starlark.None, decorateError(thread, err)
	})
	env.env[readFileBuiltinName] = starlark.NewBuiltin(readFileBuiltinName, func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if len(args) != 1 {
			return nil, decorateError(thread, fmt.Errorf("wrong number of arguments"))
		}
		path, ok := args[0].(starlark.String)
		if !ok {
			return nil, decorateError(thread, fmt.Errorf("argument of read_file was not a string"))
		}
		buf, err := ioutil.ReadFile(string(path))
		if err != nil {
			return nil, decorateError(thread, err)
		}
		return starlark.String(string(buf)), nil
	})
	env.env[writeFileBuiltinName] = starlark.NewBuiltin(writeFileBuiltinName, func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if len(args) != 2 {
			return nil, decorateError(thread, fmt.Errorf("wrong number of arguments"))
		}
		path, ok := args[0].(starlark.String)
		if !ok {
			return nil, decorateError(thread, fmt.Errorf("first argument of write_file was not a string"))
		}
		err := ioutil.WriteFile(string(path), []byte(args[1].String()), 0640)
		return starlark.None, decorateError(thread, err)
	})
	env.env[curScopeBuiltinName] = starlark.NewBuiltin(curScopeBuiltinName, func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		return env.interfaceToStarlarkValue(env.ctx.Scope()), nil
	})
	env.env[defaultLoadConfigBuiltinName] = starlark.NewBuiltin(defaultLoadConfigBuiltinName, func(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		return env.interfaceToStarlarkValue(env.ctx.LoadConfig()), nil
	})
	return env
}

// Redirect redirects starlark output to out.
func (env *Env) Redirect(out EchoWriter) {
	env.out = out
	if env.thread != nil {
		env.thread.Print = env.printFunc()
	}
}

func (env *Env) printFunc() func(_ *starlark.Thread, msg string) {
	return func(_ *starlark.Thread, msg string) { fmt.Fprintln(env.out, msg) }
}

// Execute executes a script. Path is the name of the file to execute and
// source is the source code to execute.
// Source can be either a []byte, a string or a io.Reader. If source is nil
// Execute will execute the file specified by 'path'.
// After the file is executed if a function named mainFnName exists it will be called, passing args to it.
func (env *Env) Execute(path string, source interface{}, mainFnName string, args []interface{}) (starlark.Value, error) {
	defer func() {
		err := recover()
		if err == nil {
			return
		}
		fmt.Fprintf(env.out, "panic executing starlark script: %v\n", err)
		for i := 0; ; i++ {
			pc, file, line, ok := runtime.Caller(i)
			if !ok {
				break
			}
			fname := "<unknown>"
			fn := runtime.FuncForPC(pc)
			if fn != nil {
				fname = fn.Name()
			}
			fmt.Fprintf(env.out, "%s\n\tin %s:%d\n", fname, file, line)
		}
	}()

	thread := env.newThread()
	globals, err := starlark.ExecFile(thread, path, source, env.env)
	if err != nil {
		return starlark.None, err
	}

	err = env.exportGlobals(globals)
	if err != nil {
		return starlark.None, err
	}

	return env.callMain(thread, globals, mainFnName, args)
}

// exportGlobals saves globals with a name starting with a capital letter
// into the environment and creates commands from globals with a name
// starting with "command_"
func (env *Env) exportGlobals(globals starlark.StringDict) error {
	for name, val := range globals {
		switch {
		case strings.HasPrefix(name, commandPrefix):
			err := env.createCommand(name, val)
			if err != nil {
				return err
			}
		case name[0] >= 'A' && name[0] <= 'Z':
			env.env[name] = val
		}
	}
	return nil
}

// Cancel cancels the execution of a currently running script or function.
func (env *Env) Cancel() {
	if env == nil {
		return
	}
	env.contextMu.Lock()
	if env.cancelfn != nil {
		env.cancelfn()
		env.cancelfn = nil
	}
	if env.thread != nil {
		env.thread.Cancel("user interrupt")
	}
	env.contextMu.Unlock()
}

func (env *Env) newThread() *starlark.Thread {
	thread := &starlark.Thread{
		Print: env.printFunc(),
	}
	env.contextMu.Lock()
	var ctx context.Context
	ctx, env.cancelfn = context.WithCancel(context.Background())
	env.thread = thread
	env.contextMu.Unlock()
	thread.SetLocal(dlvContextName, ctx)
	return thread
}

func (env *Env) createCommand(name string, val starlark.Value) error {
	fnval, ok := val.(*starlark.Function)
	if !ok {
		return nil
	}

	name = name[len(commandPrefix):]

	helpMsg := fnval.Doc()
	if helpMsg == "" {
		helpMsg = "user defined"
	}

	if fnval.NumParams() == 1 {
		if p0, _ := fnval.Param(0); p0 == "args" {
			env.ctx.RegisterCommand(name, helpMsg, func(args string) error {
				_, err := starlark.Call(env.newThread(), fnval, starlark.Tuple{starlark.String(args)}, nil)
				return err
			})
			return nil
		}
	}

	env.ctx.RegisterCommand(name, helpMsg, func(args string) error {
		thread := env.newThread()
		argval, err := starlark.Eval(thread, "<input>", "("+args+")", env.env)
		if err != nil {
			return err
		}
		argtuple, ok := argval.(starlark.Tuple)
		if !ok {
			argtuple = starlark.Tuple{argval}
		}
		_, err = starlark.Call(thread, fnval, argtuple, nil)
		return err
	})
	return nil
}

// callMain calls the main function in globals, if one was defined.
func (env *Env) callMain(thread *starlark.Thread, globals starlark.StringDict, mainFnName string, args []interface{}) (starlark.Value, error) {
	if mainFnName == "" {
		return starlark.None, nil
	}
	mainval := globals[mainFnName]
	if mainval == nil {
		return starlark.None, nil
	}
	mainfn, ok := mainval.(*starlark.Function)
	if !ok {
		return starlark.None, fmt.Errorf("%s is not a function", mainFnName)
	}
	if mainfn.NumParams() != len(args) {
		return starlark.None, fmt.Errorf("wrong number of arguments for %s", mainFnName)
	}
	argtuple := make(starlark.Tuple, len(args))
	for i := range args {
		argtuple[i] = env.interfaceToStarlarkValue(args[i])
	}
	return starlark.Call(thread, mainfn, argtuple, nil)
}

func isCancelled(thread *starlark.Thread) error {
	if ctx, ok := thread.Local(dlvContextName).(context.Context); ok {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	return nil
}

func decorateError(thread *starlark.Thread, err error) error {
	if err == nil {
		return nil
	}
	pos := thread.CallFrame(1).Pos
	if pos.Col > 0 {
		return fmt.Errorf("%s:%d:%d: %v", pos.Filename(), pos.Line, pos.Col, err)
	}
	return fmt.Errorf("%s:%d: %v", pos.Filename(), pos.Line, err)
}

type EchoWriter interface {
	io.Writer
	Echo(string)
	Flush()
}
