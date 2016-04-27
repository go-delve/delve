# Commands

Command | Description
--------|------------
[help](#help) | Prints the help message.
[break](#break) | Sets a breakpoint.
[trace](#trace) | Set tracepoint.
[restart](#restart) | Restart process.
[continue](#continue) | Run until breakpoint or program termination.
[step](#step) | Single step through program.
[step-instruction](#step-instruction) | Single step a single cpu instruction.
[next](#next) | Step over to next source line.
[threads](#threads) | Print out info for every traced thread.
[thread](#thread) | Switch to the specified thread.
[clear](#clear) | Deletes breakpoint.
[clearall](#clearall) | Deletes multiple breakpoints.
[goroutines](#goroutines) | List program goroutines.
[goroutine](#goroutine) | Shows or changes current goroutine
[breakpoints](#breakpoints) | Print out info for active breakpoints.
[print](#print) | Evaluate an expression.
[set](#set) | Changes the value of a variable.
[sources](#sources) | Print list of source files.
[funcs](#funcs) | Print list of functions.
[types](#types) | Print list of types
[args](#args) | Print function arguments.
[locals](#locals) | Print local variables.
[vars](#vars) | Print package variables.
[regs](#regs) | Print contents of CPU registers.
[exit](#exit) | Exit the debugger.
[list](#list) | Show source code.
[stack](#stack) | Print stack trace.
[frame](#frame) | Executes command on a different frame.
[source](#source) | Executes a file containing a list of delve commands
[disassemble](#disassemble) | Disassembler.
[on](#on) | Executes a command when a breakpoint is hit.
[condition](#condition) | Set breakpoint condition.

## help
Prints the help message.

	help [command]
	
Type "help" followed by the name of a command for more information about it.

Aliases: h

## break
Sets a breakpoint.

	break [name] <linespec>

See [Documentation/cli/locspec.md](//github.com/derekparker/delve/tree/master/Documentation/cli/locspec.md) for the syntax of linespec.

See also: "help on", "help cond" and "help clear"

Aliases: b

## trace
Set tracepoint.

	trace [name] <linespec>
	
A tracepoint is a breakpoint that does not stop the execution of the program, instead when the tracepoint is hit a notification is displayed. See [Documentation/cli/locspec.md](//github.com/derekparker/delve/tree/master/Documentation/cli/locspec.md) for the syntax of linespec.

See also: "help on", "help cond" and "help clear"

Aliases: t

## restart
Restart process.

Aliases: r

## continue
Run until breakpoint or program termination.

Aliases: c

## step
Single step through program.

Aliases: s

## step-instruction
Single step a single cpu instruction.

Aliases: si

## next
Step over to next source line.

Aliases: n

## threads
Print out info for every traced thread.


## thread
Switch to the specified thread.

	thread <id>

Aliases: tr

## clear
Deletes breakpoint.

	clear <breakpoint name or id>


## clearall
Deletes multiple breakpoints.

	clearall [<linespec>]
	
If called with the linespec argument it will delete all the breakpoints matching the linespec. If linespec is omitted all breakpoints are deleted.


## goroutines
List program goroutines.

	goroutines [-u (default: user location)|-r (runtime location)|-g (go statement location)]

Print out info for every goroutine. The flag controls what information is shown along with each goroutine:

	-u	displays location of topmost stackframe in user code
	-r	displays location of topmost stackframe (including frames inside private runtime functions)
	-g	displays location of go instruction that created the goroutine
	
If no flag is specified the default is -u.


## goroutine
Shows or changes current goroutine

	goroutine
	goroutine <id>
	goroutine <id> <command>

Called without arguments it will show information about the current goroutine.
Called with a single argument it will switch to the specified goroutine.
Called with more arguments it will execute a command on the specified goroutine.


## breakpoints
Print out info for active breakpoints.

Aliases: bp

## print
Evaluate an expression.

	[goroutine <n>] [frame <m>] print <expression>

See [Documentation/cli/expr.md](//github.com/derekparker/delve/tree/master/Documentation/cli/expr.md) for a description of supported expressions.

Aliases: p

## set
Changes the value of a variable.

	[goroutine <n>] [frame <m>] set <variable> = <value>

See [Documentation/cli/expr.md](//github.com/derekparker/delve/tree/master/Documentation/cli/expr.md) for a description of supported expressions. Only numerical variables and pointers can be changed.


## sources
Print list of source files.

	sources [<regex>]

If regex is specified only the source files matching it will be returned.


## funcs
Print list of functions.

	funcs [<regex>]

If regex is specified only the functions matching it will be returned.


## types
Print list of types

	types [<regex>]

If regex is specified only the functions matching it will be returned.


## args
Print function arguments.

	[goroutine <n>] [frame <m>] args [-v] [<regex>]

If regex is specified only function arguments with a name matching it will be returned. If -v is specified more information about each function argument will be shown.


## locals
Print local variables.

	[goroutine <n>] [frame <m>] locals [-v] [<regex>]

If regex is specified only local variables with a name matching it will be returned. If -v is specified more information about each local variable will be shown.


## vars
Print package variables.

	vars [-v] [<regex>]

If regex is specified only package variables with a name matching it will be returned. If -v is specified more information about each package variable will be shown.


## regs
Print contents of CPU registers.


## exit
Exit the debugger.

Aliases: quit q

## list
Show source code.

	[goroutine <n>] [frame <m>] list [<linespec>]

Show source around current point or provided linespec.

Aliases: ls

## stack
Print stack trace.

	[goroutine <n>] [frame <m>] stack [<depth>] [-full]

If -full is specified every stackframe will be decorated by the value of its local variables and function arguments.

Aliases: bt

## frame
Executes command on a different frame.

	frame <frame index> <command>.


## source
Executes a file containing a list of delve commands

	source <path>


## disassemble
Disassembler.

	[goroutine <n>] [frame <m>] disassemble [-a <start> <end>] [-l <locspec>]

If no argument is specified the function being executed in the selected stack frame will be executed.
	
	-a <start> <end>	disassembles the specified address range
	-l <locspec>		disassembles the specified function

Aliases: disass

## on
Executes a command when a breakpoint is hit.

	on <breakpoint name or id> <command>.
	
Supported commands: print, stack and goroutine)


## condition
Set breakpoint condition.

	condition <breakpoint name or id> <boolean expression>.
	
Specifies that the breakpoint or tracepoint should break only if the boolean expression is true.

Aliases: cond

