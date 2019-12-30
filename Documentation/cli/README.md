# Configuration

Delve can be configured via the configuration file located in `$HOME/.config/dlv/config.yml`.
You can open the file and discover all the configurable options and their default value.

# History

The command history of delve debugger is stored in `$HOME/.config/dlv/.dbg_history`.

# Commands

Command | Description
--------|------------
[args](#args) | Print function arguments.
[break](#break) | Sets a breakpoint.
[breakpoints](#breakpoints) | Print out info for active breakpoints.
[call](#call) | Resumes process, injecting a function call (EXPERIMENTAL!!!)
[check](#check) | Creates a checkpoint at the current position.
[checkpoints](#checkpoints) | Print out info for existing checkpoints.
[clear](#clear) | Deletes breakpoint.
[clear-checkpoint](#clear-checkpoint) | Deletes checkpoint.
[clearall](#clearall) | Deletes multiple breakpoints.
[condition](#condition) | Set breakpoint condition.
[config](#config) | Changes configuration parameters.
[continue](#continue) | Run until breakpoint or program termination.
[deferred](#deferred) | Executes command in the context of a deferred call.
[disassemble](#disassemble) | Disassembler.
[down](#down) | Move the current frame down.
[edit](#edit) | Open where you are in $DELVE_EDITOR or $EDITOR
[exit](#exit) | Exit the debugger.
[frame](#frame) | Set the current frame, or execute command on a different frame.
[funcs](#funcs) | Print list of functions.
[goroutine](#goroutine) | Shows or changes current goroutine
[goroutines](#goroutines) | List program goroutines.
[help](#help) | Prints the help message.
[libraries](#libraries) | List loaded dynamic libraries
[list](#list) | Show source code.
[locals](#locals) | Print local variables.
[next](#next) | Step over to next source line.
[on](#on) | Executes a command when a breakpoint is hit.
[print](#print) | Evaluate an expression.
[regs](#regs) | Print contents of CPU registers.
[restart](#restart) | Restart process from a checkpoint or event.
[rev](#rev) | Reverses the execution of the target program for the command specified.
[rewind](#rewind) | Run backwards until breakpoint or program termination.
[set](#set) | Changes the value of a variable.
[source](#source) | Executes a file containing a list of delve commands
[sources](#sources) | Print list of source files.
[stack](#stack) | Print stack trace.
[step](#step) | Single step through program.
[step-instruction](#step-instruction) | Single step a single cpu instruction.
[stepout](#stepout) | Step out of the current function.
[thread](#thread) | Switch to the specified thread.
[threads](#threads) | Print out info for every traced thread.
[trace](#trace) | Set tracepoint.
[types](#types) | Print list of types
[up](#up) | Move the current frame up.
[vars](#vars) | Print package variables.
[whatis](#whatis) | Prints type of an expression.

## args
Print function arguments.

	[goroutine <n>] [frame <m>] args [-v] [<regex>]

If regex is specified only function arguments with a name matching it will be returned. If -v is specified more information about each function argument will be shown.


## break
Sets a breakpoint.

	break [name] <linespec>

See [Documentation/cli/locspec.md](//github.com/go-delve/delve/tree/master/Documentation/cli/locspec.md) for the syntax of linespec.

See also: "help on", "help cond" and "help clear"

Aliases: b

## breakpoints
Print out info for active breakpoints.

Aliases: bp

## call
Resumes process, injecting a function call (EXPERIMENTAL!!!)
	
	call [-unsafe] <function call expression>
	
Current limitations:
- only pointers to stack-allocated objects can be passed as argument.
- only some automatic type conversions are supported.
- functions can only be called on running goroutines that are not
  executing the runtime.
- the current goroutine needs to have at least 256 bytes of free space on
  the stack.
- functions can only be called when the goroutine is stopped at a safe
  point.
- calling a function will resume execution of all goroutines.
- only supported on linux's native backend.



## check
Creates a checkpoint at the current position.

	checkpoint [note]

The "note" is arbitrary text that can be used to identify the checkpoint, if it is not specified it defaults to the current filename:line position.

Aliases: checkpoint

## checkpoints
Print out info for existing checkpoints.


## clear
Deletes breakpoint.

	clear <breakpoint name or id>


## clear-checkpoint
Deletes checkpoint.

	clear-checkpoint <id>

Aliases: clearcheck

## clearall
Deletes multiple breakpoints.

	clearall [<linespec>]

If called with the linespec argument it will delete all the breakpoints matching the linespec. If linespec is omitted all breakpoints are deleted.


## condition
Set breakpoint condition.

	condition <breakpoint name or id> <boolean expression>.

Specifies that the breakpoint or tracepoint should break only if the boolean expression is true.

Aliases: cond

## config
Changes configuration parameters.

	config -list

Show all configuration parameters.

	config -save

Saves the configuration file to disk, overwriting the current configuration file.

	config <parameter> <value>

Changes the value of a configuration parameter.

	config substitute-path <from> <to>
	config substitute-path <from>

Adds or removes a path substitution rule.

	config alias <command> <alias>
	config alias <alias>

Defines <alias> as an alias to <command> or removes an alias.


## continue
Run until breakpoint or program termination.

Aliases: c

## deferred
Executes command in the context of a deferred call.

	deferred <n> <command>

Executes the specified command (print, args, locals) in the context of the n-th deferred call in the current frame.


## disassemble
Disassembler.

	[goroutine <n>] [frame <m>] disassemble [-a <start> <end>] [-l <locspec>]

If no argument is specified the function being executed in the selected stack frame will be executed.

	-a <start> <end>	disassembles the specified address range
	-l <locspec>		disassembles the specified function

Aliases: disass

## down
Move the current frame down.

	down [<m>]
	down [<m>] <command>

Move the current frame down by <m>. The second form runs the command on the given frame.


## edit
Open where you are in $DELVE_EDITOR or $EDITOR

	edit [locspec]
	
If locspec is omitted edit will open the current source file in the editor, otherwise it will open the specified location.

Aliases: ed

## exit
Exit the debugger.
		
	exit [-c]
	
When connected to a headless instance started with the --accept-multiclient, pass -c to resume the execution of the target process before disconnecting.

Aliases: quit q

## frame
Set the current frame, or execute command on a different frame.

	frame <m>
	frame <m> <command>

The first form sets frame used by subsequent commands such as "print" or "set".
The second form runs the command on the given frame.


## funcs
Print list of functions.

	funcs [<regex>]

If regex is specified only the functions matching it will be returned.


## goroutine
Shows or changes current goroutine

	goroutine
	goroutine <id>
	goroutine <id> <command>

Called without arguments it will show information about the current goroutine.
Called with a single argument it will switch to the specified goroutine.
Called with more arguments it will execute a command on the specified goroutine.

Aliases: gr

## goroutines
List program goroutines.

	goroutines [-u (default: user location)|-r (runtime location)|-g (go statement location)|-s (start location)] [ -t (stack trace)]

Print out info for every goroutine. The flag controls what information is shown along with each goroutine:

	-u	displays location of topmost stackframe in user code
	-r	displays location of topmost stackframe (including frames inside private runtime functions)
	-g	displays location of go instruction that created the goroutine
	-s	displays location of the start function
	-t	displays stack trace of goroutine

If no flag is specified the default is -u.

Aliases: grs

## help
Prints the help message.

	help [command]

Type "help" followed by the name of a command for more information about it.

Aliases: h

## libraries
List loaded dynamic libraries


## list
Show source code.

	[goroutine <n>] [frame <m>] list [<linespec>]

Show source around current point or provided linespec.

Aliases: ls l

## locals
Print local variables.

	[goroutine <n>] [frame <m>] locals [-v] [<regex>]

The name of variables that are shadowed in the current scope will be shown in parenthesis.

If regex is specified only local variables with a name matching it will be returned. If -v is specified more information about each local variable will be shown.


## next
Step over to next source line.

	 next [count]

Optional [count] argument allows you to skip multiple lines.


Aliases: n

## on
Executes a command when a breakpoint is hit.

	on <breakpoint name or id> <command>.

Supported commands: print, stack and goroutine)


## print
Evaluate an expression.

	[goroutine <n>] [frame <m>] print <expression>

See [Documentation/cli/expr.md](//github.com/go-delve/delve/tree/master/Documentation/cli/expr.md) for a description of supported expressions.

Aliases: p

## regs
Print contents of CPU registers.

	regs [-a]

Argument -a shows more registers.


## restart
Restart process from a checkpoint or event.

  restart [event number or checkpoint id]

Aliases: r

## rev
Reverses the execution of the target program for the command specified.
Currently, only the rev step-instruction command is supported.


## rewind
Run backwards until breakpoint or program termination.

Aliases: rw

## set
Changes the value of a variable.

	[goroutine <n>] [frame <m>] set <variable> = <value>

See [Documentation/cli/expr.md](//github.com/go-delve/delve/tree/master/Documentation/cli/expr.md) for a description of supported expressions. Only numerical variables and pointers can be changed.


## source
Executes a file containing a list of delve commands

	source <path>
	
If path ends with the .star extension it will be interpreted as a starlark script. See [Documentation/cli/starlark.md](//github.com/go-delve/delve/tree/master/Documentation/cli/starlark.md) for the syntax.

If path is a single '-' character an interactive starlark interpreter will start instead. Type 'exit' to exit.


## sources
Print list of source files.

	sources [<regex>]

If regex is specified only the source files matching it will be returned.


## stack
Print stack trace.

	[goroutine <n>] [frame <m>] stack [<depth>] [-full] [-offsets] [-defer] [-a <n>] [-adepth <depth>] [-mode <mode>]

	-full		every stackframe is decorated with the value of its local variables and arguments.
	-offsets	prints frame offset of each frame.
	-defer		prints deferred function call stack for each frame.
	-a <n>		prints stacktrace of n ancestors of the selected goroutine (target process must have tracebackancestors enabled)
	-adepth <depth>	configures depth of ancestor stacktrace
	-mode <mode>	specifies the stacktrace mode, possible values are:
			normal	- attempts to automatically switch between cgo frames and go frames
			simple	- disables automatic switch between cgo and go
			fromg	- starts from the registers stored in the runtime.g struct


Aliases: bt

## step
Single step through program.

Aliases: s

## step-instruction
Single step a single cpu instruction.

Aliases: si

## stepout
Step out of the current function.

Aliases: so

## thread
Switch to the specified thread.

	thread <id>

Aliases: tr

## threads
Print out info for every traced thread.


## trace
Set tracepoint.

	trace [name] <linespec>

A tracepoint is a breakpoint that does not stop the execution of the program, instead when the tracepoint is hit a notification is displayed. See [Documentation/cli/locspec.md](//github.com/go-delve/delve/tree/master/Documentation/cli/locspec.md) for the syntax of linespec.

See also: "help on", "help cond" and "help clear"

Aliases: t

## types
Print list of types

	types [<regex>]

If regex is specified only the types matching it will be returned.


## up
Move the current frame up.

	up [<m>]
	up [<m>] <command>

Move the current frame up by <m>. The second form runs the command on the given frame.


## vars
Print package variables.

	vars [-v] [<regex>]

If regex is specified only package variables with a name matching it will be returned. If -v is specified more information about each package variable will be shown.


## whatis
Prints type of an expression.

	whatis <expression>


