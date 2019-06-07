# How to write a Delve client, an informal guide

## Spawning the backend

The `dlv` binary built by our `Makefile` contains both the backend and a
simple command line client. If you are writing your own client you will
probably want to run only the backend, you can do this by specifying the
`--headless` option, for example:

```
$ dlv --headless debug
```

The rest of the command line remains unchanged. You can use `debug`, `exec`,
`test`, etc... along with `--headless` and they will work. If this project
is part of a larger IDE integration then you probably have your own build
system and do not wish to offload this task to Delve, in that case it's
perfectly fine to always use the `dlv exec` command but do remember that:
1. Delve may not have all the information necessary to properly debug optimized binaries, so it is recommended to disable them via: `-gcflags='all=-N -l`.
2. your users *do want* to debug their tests so you should also provide some way to build the test executable (equivalent to `go test -c --gcflags='all=-N -l'`) and pass it to Delve.

It would also be nice for your users if you provided a way to attach to a running process, like `dlv attach` does.

Command line arguments that should be handed to the inferior process should be specified on dlv's command line after a "--" argument:

```
dlv exec --headless ./somebinary -- these arguments are for the inferior process
```

Specifying a static port number, like in the [README](//github.com/go-delve/Delve/tree/master/Documentation/README.md) example, can be done using `--listen=127.0.0.1:portnumber`. 

This will, however, cause problems if you actually spawn multiple instances of the debugger. 

It's probably better to let Delve pick a random unused port number on its own. To do this do not specify any `--listen` option and read one line of output from dlv's stdout. If the first line emitted by dlv starts with "API server listening at: " then dlv started correctly and the rest of the line specifies the address that Delve is listening at.

The `--log-to-file` and `--log-to-fd` options can be used to redirect the "API server listening at:" message to a file or to a file descriptor. If neither is specified the message will be output to stdout.

## Controlling the backend

Once you have a running headless instance you can connect to it and start sending commands. Delve's protocol is built on top of the [JSON-RPC](http://json-rpc.org) specification.

The methods of a `service/rpc2.RPCServer` are exposed through this connection, to find out which requests you can send see the documentation of RPCServer on [godoc](https://godoc.org/github.com/go-delve/Delve/service/rpc2#RPCServer). 

### Example

Let's say you are trying to create a breakpoint. By looking at [godoc](https://godoc.org/github.com/go-delve/Delve/service/rpc2#RPCServer) you'll find that there is a `CreateBreakpoint` method in `RPCServer`.

This method, like all other methods of RPCServer that you can call through the API, has two arguments: `args` and `out`: `args` contains all the input arguments of `CreateBreakpoint`, while `out` is what `CreateBreakpoint` will return to you.

The call that you could want to make, in pseudo-code, would be:

```
RPCServer.CreateBreakpoint(CreateBreakpointIn{ File: "/User/you/some/file.go", Line: 16 })
```

To actually send this request on the JSON-RPC connection you just have to convert the CreateBreakpointIn object to json and then wrap everything into a JSON-RPC envelope:

```
{"method":"RPCServer.CreateBreakpoint","params":[{"Breakpoint":{"file":"/User/you/some/file.go","line":16}}],"id":27}
```

Delve will respond by sending a response packet that will look like this:

```
{"id":27, "result": {"Breakpoint": {"id":3, "name":"", "addr":4538829, "file":"/User/you/some/file.go", "line":16, "functionName":"main.main", "Cond":"", "continue":false, "goroutine":false, "stacktrace":0, "LoadArgs":null, "LoadLocals":null, "hitCount":{}, "totalHitCount":0}}, "error":null}
```

## Diagnostics

Just like any other program, both Delve and your client have bugs. To help
with determining where the problem is you should log the exchange of
messages between Delve and your client somehow.

If you don't want to do this yourself you can also pass the options `--log
--log-output=rpc` to Delve. In fact the `--log-output` has many useful
values and you should expose it to users, if possible, so that we can
diagnose problems that are hard to reproduce.

## Using RPCServer.Command

`Command` is probably the most important API entry point. It lets your
client stop (`Name == "halt"`) and resume (`Name == "continue"`) execution
of the inferior process.

The return value of `Command` is a `DebuggerState` object. If you lose the
DebuggerState object returned by your last call to `Command` you can ask for
a new copy with `RPCServer.State`.

### Dealing with simultaneous breakpoints

Since Go is a programming language with a big emphasis on concurrency and
parallelism it's possible that multiple goroutines will stop at a breakpoint
simultaneously. This may at first seem incredibly unlikely but you must
understand that between the time a breakpoint is triggered and the point
where the debugger finishes stopping all threads of the inferior process
thousands of CPU instructions have to be executed, which make simultaneous
breakpoint triggering not that unlikely.

You should signal to your user *all* the breakpoints that occur after
executing a command, not just the first one. To do this iterate through the
`Threads` array in `DebuggerState` and note all the threads that have a non
nil `Breakpoint` member.

### Special continue commands

In addition to "halt" and vanilla "continue" `Command` offers a few extra
flavours of continue that automatically set interesting temporary
breakpoints: "next" will continue until the next line of the program,
"stepout" will continue until the function returns, "step" is just like
"next" but it will step into function calls (but skip all calls to
unexported runtime functions).

All of "next", "step" and "stepout" operate on the selected goroutine. The
selected gorutine is described by the `SelectedGoroutine` field of
`DebuggerState`. Every time `Command` returns the selected goroutine will be
reset to the goroutine that triggered the breakpoint.

If multiple breakpoints are triggered simultaneously the selected goroutine
will be chosen randomly between the goroutines that are stopped at a
breakpoint. If a breakpoint is hit by a thread that is executing on the
system stack *there will be no selected goroutine*. If the "halt" command is
called *there may not be a selected goroutine*.

The selected goroutine can be changed using the "switchGoroutine" command.
If "switchGoroutine" is used to switch to a goroutine that's currently
parked SelectedGoroutine and CurrentThread will be mismatched. Always prefer
SelectedGoroutine over CurrentThread, you should ignore CurrentThread
entirely unless SelectedGoroutine is nil.

### Special continue commands and asynchronous breakpoints

Because of the way go internals work it is not possible for a debugger to
resume a single goroutine. Therefore it's possible that after executing a
next/step/stepout a goroutine other than the goroutine the next/step/stepout
was executed on will hit a breakpoint.

If this happens Delve will return a DebuggerState with NextInProgress set to
true. When this happens your client has two options:

* You can signal that a different breakpoint was hit and then automatically attempt to complete the next/step/stepout by calling `RPCServer.Command` with `Name == "continue"`
* You can abort the next/step/stepout operation using `RPCServer.CancelNext`.

It is important to note that while NextInProgress is true it is not possible
to call next/step/stepout again without using CancelNext first. There can
not be multiple next/step/stepout operations in progress at any time.

### RPCServer.Command and stale executable files

It's possible (albeit unfortunate) that your user will decide to change the
source of the program being executed in the debugger, while the debugger is
running. Because of this it would be advisable that your client check that
the executable is not stale every time `Command` returns and notify the user
that the executable being run is stale and line numbers may nor align
properly anymore.

You can do this bookkeeping yourself, but Delve can also help you with the
`LastModified` call that returns the LastModified time of the executable
file when Delve started it.

## Using RPCServer.CreateBreakpoint

The only two fields you probably want to fill of the Breakpoint argument of
CreateBreakpoint are File and Line. The file name should be the absolute
path to the file as the compiler saw it.

For example if the compiler saw this path:

```
/Users/you/go/src/something/something.go
```

But `/Users/you/go/src/something` is a symbolic link to
`/Users/you/projects/golang/something` the path *must* be specified as
`/Users/you/go/src/something/something.go` and
`/Users/you/projects/golang/something/something.go` will not be recognized
as valid.

If you want to let your users specify a breakpoint on a function selected
from a list of all functions you should specify the name of the function in
the FunctionName field of Breakpoint.

If you want to support the [same language as dlv's break and trace commands](//github.com/go-delve/Delve/tree/master/Documentation/cli/locspec.md)
 you should call RPCServer.FindLocation and
then use the returned slice of Location objects to create Breakpoints to
pass to CreateBreakpoint: just fill each Breakpoint.Addr with the
contents of the corresponding Location.PC.

## Looking into variables

There are several API entry points to evaluate variables in Delve:

* RPCServer.ListPackageVars returns all global variables in all packages
* PRCServer.ListLocalVars returns all local variables of a stack frame
* RPCServer.ListFunctionArgs returns all function arguments of a stack frame
* RPCServer.Eval evaluets an expression on a given stack frame

All those API calls take a LoadConfig argument. The LoadConfig specifies how
much of the variable's value should actually be loaded. Because of
LoadConfig a variable could be loaded incompletely, you should always notify
the user of this:

* For strings, arrays, slices *and structs* the load is incomplete if: `Variable.Len > len(Variable.Children)`. This can happen to structs even if LoadConfig.MaxStructFields is -1 when MaxVariableRecurse is reached.
* For maps the load is incomplete if: `Variable.Len > len(Variable.Children) / 2`
* For interfaces the load is incomplete if the only children has the onlyAddr attribute set to true.

### Loading more of a Variable

You can also give the user an option to continue loading an incompletely
loaded variable. To load a struct that wasn't loaded automatically evaluate
the expression returned by:

```
fmt.Sprintf("*(*%q)(%#x)", v.Type, v.Addr)
```

where v is the variable that was truncated.

To load more elements from an array, slice or string:

```
fmt.Sprintf("(*(*%q)(%#x))[%d:]", v.Type, v.Addr, len(v.Children))
```

To load more elements from a map:

```
fmt.Sprintf("(*(*%q)(%#x))[%d:]", v.Type, v.Addr, len(v.Children)/2)
```

All the evaluation API calls except ListPackageVars also take a EvalScope
argument, this specifies which stack frame you are interested in. If you
are interested in the topmost stack frame of the current goroutine (or
thread) use: `EvalScope{ GoroutineID: -1, Frame: 0 }`.

More information on the expression language interpreted by RPCServer.Eval
can be found [here](//github.com/go-delve/Delve/tree/master/Documentation/cli/expr.md).

### Variable shadowing

Let's assume you are debugging a piece of code that looks like this:

```
	for i := 0; i < N; i++ {
		for i := 0; i < M; i++ {
			f(i) // <-- debugger is stopped here
		}
	}
```

The response to a ListLocalVars request will list two variables named `i`,
because at that point in the code two variables named `i` exist and are in
scope. Only one (the innermost one), however, is visible to the user. The
other one is *shadowed*.

Delve will tell you which variable is shadowed through the `Flags` field of
the `Variable` object. If `Flags` has the `VariableShadowed` bit set then
the variable in question is shadowed.

Users of your client should be able to distinguish between shadowed and
non-shadowed variables.

## Gracefully ending the debug session

To ensure that Delve cleans up after itself by deleting the `debug` or `debug.test` binary it creates 
and killing any processes spawned by the program being debugged, the `Detach` command needs to be called.
In case you are disconnecting a running program, ensure to halt the program before trying to detach.

## Testing the Client

A set of [example programs is
available](https://github.com/aarzilli/delve_client_testing) to test corner
cases in handling breakpoints and displaying data structures. Follow the
instructions in the README.txt file.
