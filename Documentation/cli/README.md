# Configuration and Command History

If `$XDG_CONFIG_HOME` is set, then configuration and command history files are located in `$XDG_CONFIG_HOME/dlv`. Otherwise, they are located in `$HOME/.config/dlv` on Linux and `$HOME/.dlv` on other systems.

The configuration file `config.yml` contains all the configurable options and their default values. The command history is stored in `.dbg_history`.

# Commands

## Running the program

Command | Description
--------|------------
[call](#call) | Resumes process, injecting a function call (EXPERIMENTAL!!!)
[continue](#continue) | Run until breakpoint or program termination.
[next](#next) | Step over to next source line.
[rebuild](#rebuild) | Rebuild the target executable and restarts it. It does not work if the executable was not built by delve.
[restart](#restart) | Restart process.
[rev](#rev) | Reverses the execution of the target program for the command specified.
[rewind](#rewind) | Run backwards until breakpoint or start of recorded history.
[step](#step) | Single step through program.
[step-instruction](#step-instruction) | Single step a single cpu instruction.
[stepout](#stepout) | Step out of the current function.


## Manipulating breakpoints

Command | Description
--------|------------
[break](#break) | Sets a breakpoint.
[breakpoints](#breakpoints) | Print out info for active breakpoints.
[clear](#clear) | Deletes breakpoint.
[clearall](#clearall) | Deletes multiple breakpoints.
[condition](#condition) | Set breakpoint condition.
[on](#on) | Executes a command when a breakpoint is hit.
[toggle](#toggle) | Toggles on or off a breakpoint.
[trace](#trace) | Set tracepoint.
[watch](#watch) | Set watchpoint.


## Viewing program variables and memory

Command | Description
--------|------------
[args](#args) | Print function arguments.
[display](#display) | Print value of an expression every time the program stops.
[examinemem](#examinemem) | Examine raw memory at the given address.
[locals](#locals) | Print local variables.
[print](#print) | Evaluate an expression.
[regs](#regs) | Print contents of CPU registers.
[set](#set) | Changes the value of a variable.
[vars](#vars) | Print package variables.
[whatis](#whatis) | Prints type of an expression.


## Listing and switching between threads and goroutines

Command | Description
--------|------------
[goroutine](#goroutine) | Shows or changes current goroutine
[goroutines](#goroutines) | List program goroutines.
[thread](#thread) | Switch to the specified thread.
[threads](#threads) | Print out info for every traced thread.


## Viewing the call stack and selecting frames

Command | Description
--------|------------
[deferred](#deferred) | Executes command in the context of a deferred call.
[down](#down) | Move the current frame down.
[frame](#frame) | Set the current frame, or execute command on a different frame.
[stack](#stack) | Print stack trace.
[up](#up) | Move the current frame up.


## Other commands

Command | Description
--------|------------
[check](#check) | Creates a checkpoint at the current position.
[checkpoints](#checkpoints) | Print out info for existing checkpoints.
[clear-checkpoint](#clear-checkpoint) | Deletes checkpoint.
[config](#config) | Changes configuration parameters.
[disassemble](#disassemble) | Disassembler.
[dump](#dump) | Creates a core dump from the current process state
[edit](#edit) | Open where you are in $DELVE_EDITOR or $EDITOR
[exit](#exit) | Exit the debugger.
[funcs](#funcs) | Print list of functions.
[help](#help) | Prints the help message.
[libraries](#libraries) | List loaded dynamic libraries
[list](#list) | Show source code.
[source](#source) | Executes a file containing a list of delve commands
[sources](#sources) | Print list of source files.
[target](#target) | Manages child process debugging.
[transcript](#transcript) | Appends command output to a file.
[types](#types) | Print list of types

## args
Print function arguments.

	[goroutine <n>] [frame <m>] args [-v] [<regex>]

If regex is specified only function arguments with a name matching it will be returned. If -v is specified more information about each function argument will be shown.


## break
Sets a breakpoint.

	break [name] [locspec]

See [Documentation/cli/locspec.md](//github.com/go-delve/delve/tree/master/Documentation/cli/locspec.md) for the syntax of locspec. If locspec is omitted a breakpoint will be set on the current line.

See also: "help on", "help cond" and "help clear"

Aliases: b

## breakpoints
Print out info for active breakpoints.
	
	breakpoints [-a]

Specifying -a prints all physical breakpoint, including internal breakpoints.

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

	clearall [<locspec>]

If called with the locspec argument it will delete all the breakpoints matching the locspec. If locspec is omitted all breakpoints are deleted.


## condition
Set breakpoint condition.

	condition <breakpoint name or id> <boolean expression>.
	condition -hitcount <breakpoint name or id> <operator> <argument>.
	condition -per-g-hitcount <breakpoint name or id> <operator> <argument>.
	condition -clear <breakpoint name or id>.

Specifies that the breakpoint, tracepoint or watchpoint should break only if the boolean expression is true.

See [Documentation/cli/expr.md](//github.com/go-delve/delve/tree/master/Documentation/cli/expr.md) for a description of supported expressions.

With the -hitcount option a condition on the breakpoint hit count can be set, the following operators are supported

	condition -hitcount bp > n
	condition -hitcount bp >= n
	condition -hitcount bp < n
	condition -hitcount bp <= n
	condition -hitcount bp == n
	condition -hitcount bp != n
	condition -hitcount bp % n

The -per-g-hitcount option works like -hitcount, but use per goroutine hitcount to compare with n.

With the -clear option a condition on the breakpoint can removed.
	
The '% n' form means we should stop at the breakpoint when the hitcount is a multiple of n.

Examples:

	cond 2 i == 10				breakpoint 2 will stop when variable i equals 10
	cond name runtime.curg.goid == 5	breakpoint 'name' will stop only on goroutine 5
	cond -clear 2				the condition on breakpoint 2 will be removed


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
	config substitute-path -clear

Adds or removes a path substitution rule, if -clear is used all
substitute-path rules are removed. Without arguments shows the current list
of substitute-path rules.
See also [Documentation/cli/substitutepath.md](//github.com/go-delve/delve/tree/master/Documentation/cli/substitutepath.md) for how the rules are applied.

	config alias <command> <alias>
	config alias <alias>

Defines <alias> as an alias to <command> or removes an alias.

	config debug-info-directories -add <path>
	config debug-info-directories -rm <path>
	config debug-info-directories -clear

Adds, removes or clears debug-info-directories.


## continue
Run until breakpoint or program termination.

	continue [<locspec>]

Optional locspec argument allows you to continue until a specific location is reached. The program will halt if a breakpoint is hit before reaching the specified location.

For example:

	continue main.main
	continue encoding/json.Marshal


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

## display
Print value of an expression every time the program stops.

	display -a [%format] <expression>
	display -d <number>

The '-a' option adds an expression to the list of expression printed every time the program stops. The '-d' option removes the specified expression from the list.

If display is called without arguments it will print the value of all expression in the list.


## down
Move the current frame down.

	down [<m>]
	down [<m>] <command>

Move the current frame down by <m>. The second form runs the command on the given frame.


## dump
Creates a core dump from the current process state

	dump <output file>

The core dump is always written in ELF, even on systems (windows, macOS) where this is not customary. For environments other than linux/amd64 threads and registers are dumped in a format that only Delve can read back.


## edit
Open where you are in $DELVE_EDITOR or $EDITOR

	edit [locspec]
	
If locspec is omitted edit will open the current source file in the editor, otherwise it will open the specified location.

Aliases: ed

## examinemem
Examine raw memory at the given address.

Examine memory:

	examinemem [-fmt <format>] [-count|-len <count>] [-size <size>] <address>
	examinemem [-fmt <format>] [-count|-len <count>] [-size <size>] -x <expression>

Format represents the data format and the value is one of this list (default hex): bin(binary), oct(octal), dec(decimal), hex(hexadecimal).
Length is the number of bytes (default 1) and must be less than or equal to 1000.
Address is the memory location of the target to examine. Please note '-len' is deprecated by '-count and -size'.
Expression can be an integer expression or pointer value of the memory location to examine.

For example:

    x -fmt hex -count 20 -size 1 0xc00008af38
    x -fmt hex -count 20 -size 1 -x 0xc00008af38 + 8
    x -fmt hex -count 20 -size 1 -x &myVar
    x -fmt hex -count 20 -size 1 -x myPtrVar

Aliases: x

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

	goroutines [-u|-r|-g|-s] [-t [depth]] [-l] [-with loc expr] [-without loc expr] [-group argument] [-exec command]

Print out info for every goroutine. The flag controls what information is shown along with each goroutine:

	-u	displays location of topmost stackframe in user code (default)
	-r	displays location of topmost stackframe (including frames inside private runtime functions)
	-g	displays location of go instruction that created the goroutine
	-s	displays location of the start function
	-t	displays goroutine's stacktrace (an optional depth value can be specified, default: 10)
	-l	displays goroutine's labels

If no flag is specified the default is -u, i.e. the first frame within the first 30 frames that is not executing a runtime private function.

FILTERING

If -with or -without are specified only goroutines that match the given condition are returned.

To only display goroutines where the specified location contains (or does not contain, for -without and -wo) expr as a substring, use:

	goroutines -with (userloc|curloc|goloc|startloc) expr
	goroutines -w (userloc|curloc|goloc|startloc) expr
	goroutines -without (userloc|curloc|goloc|startloc) expr
	goroutines -wo (userloc|curloc|goloc|startloc) expr

	Where:
	userloc: filter by the location of the topmost stackframe in user code
	curloc: filter by the location of the topmost stackframe (including frames inside private runtime functions)
	goloc: filter by the location of the go instruction that created the goroutine
	startloc: filter by the location of the start function
	
To only display goroutines that have (or do not have) the specified label key and value, use:

	goroutines -with label key=value
	goroutines -without label key=value
	
To only display goroutines that have (or do not have) the specified label key, use:

	goroutines -with label key
	goroutines -without label key
	
To only display goroutines that are running (or are not running) on a OS thread, use:


	goroutines -with running
	goroutines -without running
	
To only display user (or runtime) goroutines, use:

	goroutines -with user
	goroutines -without user

GROUPING

	goroutines -group (userloc|curloc|goloc|startloc|running|user)

	Where:
	userloc: groups goroutines by the location of the topmost stackframe in user code
	curloc: groups goroutines by the location of the topmost stackframe
	goloc: groups goroutines by the location of the go instruction that created the goroutine
	startloc: groups goroutines by the location of the start function
	running: groups goroutines by whether they are running or not
	user: groups goroutines by weather they are user or runtime goroutines


Groups goroutines by the given location, running status or user classification, up to 5 goroutines per group will be displayed as well as the total number of goroutines in the group.

	goroutines -group label key

Groups goroutines by the value of the label with the specified key.

EXEC

	goroutines -exec <command>

Runs the command on every goroutine.


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

	[goroutine <n>] [frame <m>] list [<locspec>]

Show source around current point or provided locspec.

For example:

	frame 1 list 69
	list testvariables.go:10000
	list main.main:30
	list 40

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

	on <breakpoint name or id> <command>
	on <breakpoint name or id> -edit
	

Supported commands: print, stack, goroutine, trace and cond. 
To convert a breakpoint into a tracepoint use:
	
	on <breakpoint name or id> trace

The command 'on <bp> cond <cond-arguments>' is equivalent to 'cond <bp> <cond-arguments>'.

The command 'on x -edit' can be used to edit the list of commands executed when the breakpoint is hit.


## print
Evaluate an expression.

	[goroutine <n>] [frame <m>] print [%format] <expression>

See [Documentation/cli/expr.md](//github.com/go-delve/delve/tree/master/Documentation/cli/expr.md) for a description of supported expressions.

The optional format argument is a format specifier, like the ones used by the fmt package. For example "print %x v" will print v as an hexadecimal number.

Aliases: p

## rebuild
Rebuild the target executable and restarts it. It does not work if the executable was not built by delve.


## regs
Print contents of CPU registers.

	regs [-a]

Argument -a shows more registers. Individual registers can also be displayed by 'print' and 'display'. See [Documentation/cli/expr.md](//github.com/go-delve/delve/tree/master/Documentation/cli/expr.md).


## restart
Restart process.

For recorded targets the command takes the following forms:

	restart					resets to the start of the recording
	restart [checkpoint]			resets the recording to the given checkpoint
	restart -r [newargv...]	[redirects...]	re-records the target process
	
For live targets the command takes the following forms:

	restart [newargv...] [redirects...]	restarts the process

If newargv is omitted the process is restarted (or re-recorded) with the same argument vector.
If -noargs is specified instead, the argument vector is cleared.

A list of file redirections can be specified after the new argument list to override the redirections defined using the '--redirect' command line option. A syntax similar to Unix shells is used:

	<input.txt	redirects the standard input of the target process from input.txt
	>output.txt	redirects the standard output of the target process to output.txt
	2>error.txt	redirects the standard error of the target process to error.txt


Aliases: r

## rev
Reverses the execution of the target program for the command specified.
Currently, rev next, step, step-instruction and stepout commands are supported.


## rewind
Run backwards until breakpoint or start of recorded history.

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

## target
Manages child process debugging.

	target follow-exec [-on [regex]] [-off]

Enables or disables follow exec mode. When follow exec mode Delve will automatically attach to new child processes executed by the target process. An optional regular expression can be passed to 'target follow-exec', only child processes with a command line matching the regular expression will be followed.

	target list

List currently attached processes.

	target switch [pid]

Switches to the specified process.


## thread
Switch to the specified thread.

	thread <id>

Aliases: tr

## threads
Print out info for every traced thread.


## toggle
Toggles on or off a breakpoint.

toggle <breakpoint name or id>


## trace
Set tracepoint.

	trace [name] [locspec]

A tracepoint is a breakpoint that does not stop the execution of the program, instead when the tracepoint is hit a notification is displayed. See [Documentation/cli/locspec.md](//github.com/go-delve/delve/tree/master/Documentation/cli/locspec.md) for the syntax of locspec. If locspec is omitted a tracepoint will be set on the current line.

See also: "help on", "help cond" and "help clear"

Aliases: t

## transcript
Appends command output to a file.

	transcript [-t] [-x] <output file>
	transcript -off

Output of Delve's command is appended to the specified output file. If '-t' is specified and the output file exists it is truncated. If '-x' is specified output to stdout is suppressed instead.

Using the -off option disables the transcript.


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


## watch
Set watchpoint.
	
	watch [-r|-w|-rw] <expr>
	
	-r	stops when the memory location is read
	-w	stops when the memory location is written
	-rw	stops when the memory location is read or written

The memory location is specified with the same expression language used by 'print', for example:

	watch v
	watch -w *(*int)(0x1400007c018)

will watch the address of variable 'v' and writes to an int at addr '0x1400007c018'.

Note that writes that do not change the value of the watched memory address might not be reported.

See also: "help print".


## whatis
Prints type of an expression.

	whatis <expression>


