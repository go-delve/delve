# Introduction

Passing a file with the .star extension to the `source` command will cause delve to interpret it as a starlark script.

Starlark is a dialect of python, a [specification of its syntax can be found here](https://github.com/google/starlark-go/blob/master/doc/spec.md).

In addition to the normal starlark built-ins delve defines [a number of global functions](#Starlark-built-ins) that can be used to interact with the debugger.

After the file has been evaluated delve will bind any function starting with `command_` to a command-line command: for example `command_goroutines_wait_reason` will be bound to `goroutines_wait_reason`. 

Then if a function named `main` exists it will be executed.

Global functions with a name that begins with a capital letter will be available to other scripts.

# Starlark built-ins

<!-- BEGIN MAPPING TABLE -->
Function | API Call
---------|---------
amend_breakpoint(Breakpoint) | Equivalent to API call [AmendBreakpoint](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.AmendBreakpoint)
ancestors(GoroutineID, NumAncestors, Depth) | Equivalent to API call [Ancestors](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.Ancestors)
attached_to_existing_process() | Equivalent to API call [AttachedToExistingProcess](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.AttachedToExistingProcess)
cancel_next() | Equivalent to API call [CancelNext](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.CancelNext)
checkpoint(Where) | Equivalent to API call [Checkpoint](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.Checkpoint)
clear_breakpoint(Id, Name) | Equivalent to API call [ClearBreakpoint](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ClearBreakpoint)
clear_checkpoint(ID) | Equivalent to API call [ClearCheckpoint](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ClearCheckpoint)
raw_command(Name, ThreadID, GoroutineID, ReturnInfoLoadConfig, Expr, UnsafeCall) | Equivalent to API call [Command](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.Command)
create_breakpoint(Breakpoint) | Equivalent to API call [CreateBreakpoint](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.CreateBreakpoint)
detach(Kill) | Equivalent to API call [Detach](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.Detach)
disassemble(Scope, StartPC, EndPC, Flavour) | Equivalent to API call [Disassemble](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.Disassemble)
dump_cancel() | Equivalent to API call [DumpCancel](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.DumpCancel)
dump_start(Destination) | Equivalent to API call [DumpStart](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.DumpStart)
dump_wait(Wait) | Equivalent to API call [DumpWait](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.DumpWait)
eval(Scope, Expr, Cfg) | Equivalent to API call [Eval](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.Eval)
examine_memory(Address, Length) | Equivalent to API call [ExamineMemory](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ExamineMemory)
find_location(Scope, Loc, IncludeNonExecutableLines, SubstitutePathRules) | Equivalent to API call [FindLocation](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.FindLocation)
function_return_locations(FnName) | Equivalent to API call [FunctionReturnLocations](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.FunctionReturnLocations)
get_breakpoint(Id, Name) | Equivalent to API call [GetBreakpoint](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.GetBreakpoint)
get_thread(Id) | Equivalent to API call [GetThread](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.GetThread)
is_multiclient() | Equivalent to API call [IsMulticlient](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.IsMulticlient)
last_modified() | Equivalent to API call [LastModified](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.LastModified)
breakpoints() | Equivalent to API call [ListBreakpoints](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ListBreakpoints)
checkpoints() | Equivalent to API call [ListCheckpoints](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ListCheckpoints)
dynamic_libraries() | Equivalent to API call [ListDynamicLibraries](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ListDynamicLibraries)
function_args(Scope, Cfg) | Equivalent to API call [ListFunctionArgs](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ListFunctionArgs)
functions(Filter) | Equivalent to API call [ListFunctions](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ListFunctions)
goroutines(Start, Count) | Equivalent to API call [ListGoroutines](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ListGoroutines)
local_vars(Scope, Cfg) | Equivalent to API call [ListLocalVars](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ListLocalVars)
package_vars(Filter, Cfg) | Equivalent to API call [ListPackageVars](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ListPackageVars)
packages_build_info(IncludeFiles) | Equivalent to API call [ListPackagesBuildInfo](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ListPackagesBuildInfo)
registers(ThreadID, IncludeFp, Scope) | Equivalent to API call [ListRegisters](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ListRegisters)
sources(Filter) | Equivalent to API call [ListSources](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ListSources)
threads() | Equivalent to API call [ListThreads](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ListThreads)
types(Filter) | Equivalent to API call [ListTypes](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ListTypes)
process_pid() | Equivalent to API call [ProcessPid](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.ProcessPid)
recorded() | Equivalent to API call [Recorded](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.Recorded)
restart(Position, ResetArgs, NewArgs, Rerecord, Rebuild, NewRedirects) | Equivalent to API call [Restart](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.Restart)
set_expr(Scope, Symbol, Value) | Equivalent to API call [Set](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.Set)
stacktrace(Id, Depth, Full, Defers, Opts, Cfg) | Equivalent to API call [Stacktrace](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.Stacktrace)
state(NonBlocking) | Equivalent to API call [State](https://godoc.org/github.com/go-delve/delve/service/rpc2#RPCServer.State)
dlv_command(command) | Executes the specified command as if typed at the dlv_prompt
read_file(path) | Reads the file as a string
write_file(path, contents) | Writes string to a file
cur_scope() | Returns the current evaluation scope
default_load_config() | Returns the current default load configuration
<!-- END MAPPING TABLE -->

## Should I use raw_command or dlv_command?

There are two ways to resume the execution of the target program:

	raw_command("continue")
	dlv_command("continue")

The first one maps to the API call [Command](https://godoc.org/github.com/derekparker/delve/service/rpc2#RPCServer.Command). As such all the caveats explained in the [Client HowTo](../api/ClientHowto.md).

The latter is equivalent to typing `continue` to the `(dlv)` command line and should do what you expect.

In general `dlv_command("continue")` should be preferred, unless the behavior you wish to produces diverges significantly from that of the command line's `continue`.

# Creating new commands

Any global function with a name starting with `command_` will be made available as a command line command. If the function has a single argument named `args` all arguments passed on the command line will be passed to the function as a single string. 

Otherwise arguments passed on the command line are interpreted as starlark expressions. See the [expression arguments](#expression-arguments) example. 

If the command function has a doc string it will be used as a help message.

# Working with variables

Variables of the target program can be accessed using `local_vars`, `function_args` or the `eval` functions. Each variable will be returned as a [Variable](https://godoc.org/github.com/go-delve/delve/service/api#Variable) struct, with one special field: `Value`.

## Variable.Value

The `Value` field will return the value of the target variable converted to a starlark value:

* integers, floating point numbers and strings are represented by equivalent starlark values
* structs are represented as starlark dictionaries
* slices and arrays are represented by starlark lists
* maps are represented by starlark dicts
* pointers and interfaces are represented by a one-element starlark list containing the value they point to

For example, given this variable in the target program:

```
type astruct struct {
	A int
	B int
}

s2 := []astruct{{1, 2}, {3, 4}, {5, 6}, {7, 8}, {9, 10}, {11, 12}, {13, 14}, {15, 16}}
```

The following is possible:

```
>>> s2 = eval(None, "s2").Variable
>>> s2.Value[0]                                     # access of a slice item by index
main.astruct {A: 1, B: 2}
>>> a = s2.Value[1]
>>> a.Value.A                                       # access to a struct field
3
>>> a.Value.A + 10                            # arithmetic on the value of s2[1].X
13
>>> a.Value["B"]                                    # access to a struct field, using dictionary syntax
4
```

For more examples see the [linked list example](#Print-all-elements-of-a-linked-list) below.

# Examples

## Listing goroutines and making custom commands

Create a `goroutine_start_line` command that prints the starting line of each goroutine, sets `gsl` as an alias:

```
def command_goroutine_start_line(args):
	gs = goroutines().Goroutines
	for g in gs:
		line = read_file(g.StartLoc.File).splitlines()[g.StartLoc.Line-1].strip()
		print(g.ID, "\t", g.StartLoc.File + ":" + str(g.StartLoc.Line), "\t", line)

def main():
	dlv_command("config alias goroutine_start_line gsl")
```

Use it like this:

```
(dlv) source goroutine_start_line.start
(dlv) goroutine_start_line
1 	 /usr/local/go/src/runtime/proc.go:110 	 func main() {
2 	 /usr/local/go/src/runtime/proc.go:242 	 func forcegchelper() {
17 	 /usr/local/go/src/runtime/mgcsweep.go:64 	 func bgsweep(c chan int) {
18 	 /usr/local/go/src/runtime/mfinal.go:161 	 func runfinq() {
(dlv) gsl
1 	 /usr/local/go/src/runtime/proc.go:110 	 func main() {
2 	 /usr/local/go/src/runtime/proc.go:242 	 func forcegchelper() {
17 	 /usr/local/go/src/runtime/mgcsweep.go:64 	 func bgsweep(c chan int) {
18 	 /usr/local/go/src/runtime/mfinal.go:161 	 func runfinq() {
```

## Expression arguments

After evaluating this script:

```
def command_echo(args):
	print(args)

def command_echo_expr(a, b, c):
	print("a", a, "b", b, "c", c)
```

The first commnad, `echo`, takes its arguments as a single string, while for `echo_expr` it will be possible to pass starlark expression as arguments:

```
(dlv) echo 2+2, 2-1, 2*3
"2+2, 2-1, 2*3"
(dlv) echo_expr 2+2, 2-1, 2*3
a 4 b 1 c 6
```

## Creating breakpoints

Set a breakpoint on all private methods of package `main`:

```
def main():
	for f in functions().Funcs:
		v = f.split('.')
		if len(v) != 2:
			continue
		if v[0] != "main":
			continue
		if v[1][0] >= 'a' and v[1][0] <= 'z':
			create_breakpoint({ "FunctionName": f, "Line": -1 }) # see documentation of RPCServer.CreateBreakpoint
```

## Switching goroutines

Create a command, `switch_to_main_goroutine`, that searches for a goroutine running a function in the main package and switches to it:

```
def command_switch_to_main_goroutine(args):
	for g in goroutines().Goroutines:
		if g.currentLoc.function != None and g.currentLoc.function.name.startswith("main."):
			print("switching to:", g.id)
			raw_command("switchGoroutine", GoroutineID=g.id)
			break
```

## Listing goroutines

Create a command, "goexcl", that lists all goroutines excluding the ones stopped on a specified function.

```
def command_goexcl(args):
	"""Prints all goroutines not stopped in the function passed as argument."""
	excluded = 0
	start = 0
	while start >= 0:
		gr = goroutines(start, 10)
		start = gr.Nextg
		for g in gr.Goroutines:
			fn = g.UserCurrentLoc.Function
			if fn == None:
				print("Goroutine", g.ID, "User:", g.UserCurrentLoc.File, g.UserCurrentLoc.Line)
			elif fn.Name_ != args:
				print("Goroutine", g.ID, "User:", g.UserCurrentLoc.File, g.UserCurrentLoc.Line, fn.Name_)
			else:
				excluded = excluded + 1
	print("Excluded", excluded, "goroutines")
```

Usage:

```
(dlv) goexcl main.somefunc
```

prints all goroutines that are not stopped inside `main.somefunc`.

## Repeatedly executing the target until a breakpoint is hit.

Repeatedly call continue and restart until the target hits a breakpoint.

```
def command_flaky(args):
	"Repeatedly runs program until a breakpoint is hit"
	while True:
		if dlv_command("continue") == None:
			break
		dlv_command("restart")
```

## Print all elements of a linked list

```
def command_linked_list(args):
	"""Prints the contents of a linked list.
	
	linked_list <var_name> <next_field_name> <max_depth>

Prints up to max_depth elements of the linked list variable 'var_name' using 'next_field_name' as the name of the link field.
"""
	var_name, next_field_name, max_depth = args.split(" ")
	max_depth = int(max_depth)
	next_name = var_name
	v = eval(None, var_name).Variable.Value
	for i in range(0, max_depth):
		print(str(i)+":",v)
		if v[0] == None:
			break
		v = v[next_field_name]
```

## Find an array element matching a predicate

```
def command_find_array(arr, pred):
	"""Calls pred for each element of the array or slice 'arr' returns the index of the first element for which pred returns true.
	
	find_arr <arr> <pred>
	
Example use (find the first element of slice 's2' with field A equal to 5):
	
	find_arr "s2", lambda x: x.A == 5
"""
	arrv = eval(None, arr).Variable
	for i in range(0, arrv.Len):
		v = arrv.Value[i]
		if pred(v):
			print("found", i)
			return

	print("not found")
```

## Rerunning a program until it fails or hits a breakpoint

```
def command_flaky(args):
	"Continues and restarts the target program repeatedly (re-recording it on the rr backend), until a breakpoint is hit"
	count = 1
	while True:
		if dlv_command("continue") == None:
			break
		print("restarting", count, "...")
		count = count+1
		restart(Rerecord=True)

```
