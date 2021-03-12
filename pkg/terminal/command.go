// Package terminal implements functions for responding to user
// input and dispatching to appropriate backend commands.
package terminal

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"go/parser"
	"go/scanner"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cosiner/argv"
	"github.com/go-delve/delve/pkg/locspec"
	"github.com/go-delve/delve/pkg/terminal/colorize"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/rpc2"
)

const optimizedFunctionWarning = "Warning: debugging optimized function"

type cmdPrefix int

const (
	noPrefix = cmdPrefix(0)
	onPrefix = cmdPrefix(1 << iota)
	deferredPrefix
	revPrefix
)

type callContext struct {
	Prefix     cmdPrefix
	Scope      api.EvalScope
	Breakpoint *api.Breakpoint
}

func (ctx *callContext) scoped() bool {
	return ctx.Scope.GoroutineID >= 0 || ctx.Scope.Frame > 0
}

type frameDirection int

const (
	frameSet frameDirection = iota
	frameUp
	frameDown
)

type cmdfunc func(t *Term, ctx callContext, args string) error

type command struct {
	aliases         []string
	builtinAliases  []string
	group           commandGroup
	allowedPrefixes cmdPrefix
	helpMsg         string
	cmdFn           cmdfunc
}

// Returns true if the command string matches one of the aliases for this command
func (c command) match(cmdstr string) bool {
	for _, v := range c.aliases {
		if v == cmdstr {
			return true
		}
	}
	return false
}

// Commands represents the commands for Delve terminal process.
type Commands struct {
	cmds   []command
	client service.Client
	frame  int // Current frame as set by frame/up/down commands.
}

var (
	// longLoadConfig loads more information:
	// * Follows pointers
	// * Loads more array values
	// * Does not limit struct fields
	longLoadConfig = api.LoadConfig{FollowPointers: true, MaxVariableRecurse: 1, MaxStringLen: 64, MaxArrayValues: 64, MaxStructFields: -1}
	// ShortLoadConfig loads less information, not following pointers
	// and limiting struct fields loaded to 3.
	ShortLoadConfig = api.LoadConfig{MaxStringLen: 64, MaxStructFields: 3}
)

// byFirstAlias will sort by the first
// alias of a command.
type byFirstAlias []command

func (a byFirstAlias) Len() int           { return len(a) }
func (a byFirstAlias) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byFirstAlias) Less(i, j int) bool { return a[i].aliases[0] < a[j].aliases[0] }

// DebugCommands returns a Commands struct with default commands defined.
func DebugCommands(client service.Client) *Commands {
	c := &Commands{client: client}

	c.cmds = []command{
		{aliases: []string{"help", "h"}, cmdFn: c.help, helpMsg: `Prints the help message.

	help [command]

Type "help" followed by the name of a command for more information about it.`},
		{aliases: []string{"break", "b"}, group: breakCmds, cmdFn: breakpoint, helpMsg: `Sets a breakpoint.

	break [name] <linespec>

See $GOPATH/src/github.com/go-delve/delve/Documentation/cli/locspec.md for the syntax of linespec.

See also: "help on", "help cond" and "help clear"`},
		{aliases: []string{"trace", "t"}, group: breakCmds, cmdFn: tracepoint, helpMsg: `Set tracepoint.

	trace [name] <linespec>

A tracepoint is a breakpoint that does not stop the execution of the program, instead when the tracepoint is hit a notification is displayed. See $GOPATH/src/github.com/go-delve/delve/Documentation/cli/locspec.md for the syntax of linespec.

See also: "help on", "help cond" and "help clear"`},
		{aliases: []string{"restart", "r"}, group: runCmds, cmdFn: restart, helpMsg: `Restart process.

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
`},
		{aliases: []string{"rebuild"}, group: runCmds, cmdFn: c.rebuild, allowedPrefixes: revPrefix, helpMsg: "Rebuild the target executable and restarts it. It does not work if the executable was not built by delve."},
		{aliases: []string{"continue", "c"}, group: runCmds, cmdFn: c.cont, allowedPrefixes: revPrefix, helpMsg: `Run until breakpoint or program termination.

	continue [<linespec>]

Optional linespec argument allows you to continue until a specific location is reached. The program will halt if a breakpoint is hit before reaching the specified location.

For example:

	continue main.main
	continue encoding/json.Marshal
`},
		{aliases: []string{"step", "s"}, group: runCmds, cmdFn: c.step, allowedPrefixes: revPrefix, helpMsg: "Single step through program."},
		{aliases: []string{"step-instruction", "si"}, group: runCmds, allowedPrefixes: revPrefix, cmdFn: c.stepInstruction, helpMsg: "Single step a single cpu instruction."},
		{aliases: []string{"next", "n"}, group: runCmds, cmdFn: c.next, allowedPrefixes: revPrefix, helpMsg: `Step over to next source line.

	next [count]

Optional [count] argument allows you to skip multiple lines.
`},
		{aliases: []string{"stepout", "so"}, group: runCmds, allowedPrefixes: revPrefix, cmdFn: c.stepout, helpMsg: "Step out of the current function."},
		{aliases: []string{"call"}, group: runCmds, cmdFn: c.call, helpMsg: `Resumes process, injecting a function call (EXPERIMENTAL!!!)
	
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
`},
		{aliases: []string{"threads"}, group: goroutineCmds, cmdFn: threads, helpMsg: "Print out info for every traced thread."},
		{aliases: []string{"thread", "tr"}, group: goroutineCmds, cmdFn: thread, helpMsg: `Switch to the specified thread.

	thread <id>`},
		{aliases: []string{"clear"}, group: breakCmds, cmdFn: clear, helpMsg: `Deletes breakpoint.

	clear <breakpoint name or id>`},
		{aliases: []string{"clearall"}, group: breakCmds, cmdFn: clearAll, helpMsg: `Deletes multiple breakpoints.

	clearall [<linespec>]

If called with the linespec argument it will delete all the breakpoints matching the linespec. If linespec is omitted all breakpoints are deleted.`},
		{aliases: []string{"goroutines", "grs"}, group: goroutineCmds, cmdFn: goroutines, helpMsg: `List program goroutines.

	goroutines [-u (default: user location)|-r (runtime location)|-g (go statement location)|-s (start location)] [-t (stack trace)] [-l (labels)]

Print out info for every goroutine. The flag controls what information is shown along with each goroutine:

	-u	displays location of topmost stackframe in user code
	-r	displays location of topmost stackframe (including frames inside private runtime functions)
	-g	displays location of go instruction that created the goroutine
	-s	displays location of the start function
	-t	displays goroutine's stacktrace
	-l	displays goroutine's labels

If no flag is specified the default is -u.`},
		{aliases: []string{"goroutine", "gr"}, group: goroutineCmds, allowedPrefixes: onPrefix, cmdFn: c.goroutine, helpMsg: `Shows or changes current goroutine

	goroutine
	goroutine <id>
	goroutine <id> <command>

Called without arguments it will show information about the current goroutine.
Called with a single argument it will switch to the specified goroutine.
Called with more arguments it will execute a command on the specified goroutine.`},
		{aliases: []string{"breakpoints", "bp"}, group: breakCmds, cmdFn: breakpoints, helpMsg: "Print out info for active breakpoints."},
		{aliases: []string{"print", "p"}, group: dataCmds, allowedPrefixes: onPrefix | deferredPrefix, cmdFn: printVar, helpMsg: `Evaluate an expression.

	[goroutine <n>] [frame <m>] print <expression>

See $GOPATH/src/github.com/go-delve/delve/Documentation/cli/expr.md for a description of supported expressions.`},
		{aliases: []string{"whatis"}, group: dataCmds, cmdFn: whatisCommand, helpMsg: `Prints type of an expression.

	whatis <expression>`},
		{aliases: []string{"set"}, group: dataCmds, cmdFn: setVar, helpMsg: `Changes the value of a variable.

	[goroutine <n>] [frame <m>] set <variable> = <value>

See $GOPATH/src/github.com/go-delve/delve/Documentation/cli/expr.md for a description of supported expressions. Only numerical variables and pointers can be changed.`},
		{aliases: []string{"sources"}, cmdFn: sources, helpMsg: `Print list of source files.

	sources [<regex>]

If regex is specified only the source files matching it will be returned.`},
		{aliases: []string{"funcs"}, cmdFn: funcs, helpMsg: `Print list of functions.

	funcs [<regex>]

If regex is specified only the functions matching it will be returned.`},
		{aliases: []string{"types"}, cmdFn: types, helpMsg: `Print list of types

	types [<regex>]

If regex is specified only the types matching it will be returned.`},
		{aliases: []string{"args"}, allowedPrefixes: onPrefix | deferredPrefix, group: dataCmds, cmdFn: args, helpMsg: `Print function arguments.

	[goroutine <n>] [frame <m>] args [-v] [<regex>]

If regex is specified only function arguments with a name matching it will be returned. If -v is specified more information about each function argument will be shown.`},
		{aliases: []string{"locals"}, allowedPrefixes: onPrefix | deferredPrefix, group: dataCmds, cmdFn: locals, helpMsg: `Print local variables.

	[goroutine <n>] [frame <m>] locals [-v] [<regex>]

The name of variables that are shadowed in the current scope will be shown in parenthesis.

If regex is specified only local variables with a name matching it will be returned. If -v is specified more information about each local variable will be shown.`},
		{aliases: []string{"vars"}, cmdFn: vars, group: dataCmds, helpMsg: `Print package variables.

	vars [-v] [<regex>]

If regex is specified only package variables with a name matching it will be returned. If -v is specified more information about each package variable will be shown.`},
		{aliases: []string{"regs"}, cmdFn: regs, group: dataCmds, helpMsg: `Print contents of CPU registers.

	regs [-a]

Argument -a shows more registers.`},
		{aliases: []string{"exit", "quit", "q"}, cmdFn: exitCommand, helpMsg: `Exit the debugger.
		
	exit [-c]
	
When connected to a headless instance started with the --accept-multiclient, pass -c to resume the execution of the target process before disconnecting.`},
		{aliases: []string{"list", "ls", "l"}, cmdFn: listCommand, helpMsg: `Show source code.

	[goroutine <n>] [frame <m>] list [<linespec>]

Show source around current point or provided linespec.

For example:

	frame 1 list 69
	list testvariables.go:10000
	list main.main:30
	list 40`},
		{aliases: []string{"stack", "bt"}, allowedPrefixes: onPrefix, group: stackCmds, cmdFn: stackCommand, helpMsg: `Print stack trace.

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
`},
		{aliases: []string{"frame"},
			group: stackCmds,
			cmdFn: func(t *Term, ctx callContext, arg string) error {
				return c.frameCommand(t, ctx, arg, frameSet)
			},
			helpMsg: `Set the current frame, or execute command on a different frame.

	frame <m>
	frame <m> <command>

The first form sets frame used by subsequent commands such as "print" or "set".
The second form runs the command on the given frame.`},
		{aliases: []string{"up"},
			group: stackCmds,
			cmdFn: func(t *Term, ctx callContext, arg string) error {
				return c.frameCommand(t, ctx, arg, frameUp)
			},
			helpMsg: `Move the current frame up.

	up [<m>]
	up [<m>] <command>

Move the current frame up by <m>. The second form runs the command on the given frame.`},
		{aliases: []string{"down"},
			group: stackCmds,
			cmdFn: func(t *Term, ctx callContext, arg string) error {
				return c.frameCommand(t, ctx, arg, frameDown)
			},
			helpMsg: `Move the current frame down.

	down [<m>]
	down [<m>] <command>

Move the current frame down by <m>. The second form runs the command on the given frame.`},
		{aliases: []string{"deferred"}, group: stackCmds, cmdFn: c.deferredCommand, helpMsg: `Executes command in the context of a deferred call.

	deferred <n> <command>

Executes the specified command (print, args, locals) in the context of the n-th deferred call in the current frame.`},
		{aliases: []string{"source"}, cmdFn: c.sourceCommand, helpMsg: `Executes a file containing a list of delve commands

	source <path>
	
If path ends with the .star extension it will be interpreted as a starlark script. See $GOPATH/src/github.com/go-delve/delve/Documentation/cli/starlark.md for the syntax.

If path is a single '-' character an interactive starlark interpreter will start instead. Type 'exit' to exit.`},
		{aliases: []string{"disassemble", "disass"}, cmdFn: disassCommand, helpMsg: `Disassembler.

	[goroutine <n>] [frame <m>] disassemble [-a <start> <end>] [-l <locspec>]

If no argument is specified the function being executed in the selected stack frame will be executed.

	-a <start> <end>	disassembles the specified address range
	-l <locspec>		disassembles the specified function`},
		{aliases: []string{"on"}, group: breakCmds, cmdFn: c.onCmd, helpMsg: `Executes a command when a breakpoint is hit.

	on <breakpoint name or id> <command>.

Supported commands: print, stack and goroutine)`},
		{aliases: []string{"condition", "cond"}, group: breakCmds, cmdFn: conditionCmd, helpMsg: `Set breakpoint condition.

	condition <breakpoint name or id> <boolean expression>.

Specifies that the breakpoint or tracepoint should break only if the boolean expression is true.`},
		{aliases: []string{"config"}, cmdFn: configureCmd, helpMsg: `Changes configuration parameters.

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

Defines <alias> as an alias to <command> or removes an alias.`},

		{aliases: []string{"edit", "ed"}, cmdFn: edit, helpMsg: `Open where you are in $DELVE_EDITOR or $EDITOR

	edit [locspec]
	
If locspec is omitted edit will open the current source file in the editor, otherwise it will open the specified location.`},
		{aliases: []string{"libraries"}, cmdFn: libraries, helpMsg: `List loaded dynamic libraries`},

		{aliases: []string{"examinemem", "x"}, group: dataCmds, cmdFn: examineMemoryCmd, helpMsg: `Examine memory:

	examinemem [-fmt <format>] [-count|-len <count>] [-size <size>] <address>

Format represents the data format and the value is one of this list (default hex): bin(binary), oct(octal), dec(decimal), hex(hexadecimal), addr(address).
Length is the number of bytes (default 1) and must be less than or equal to 1000.
Address is the memory location of the target to examine. Please note '-len' is deprecated by '-count and -size'.

For example:

    x -fmt hex -count 20 -size 1 0xc00008af38`},

		{aliases: []string{"display"}, group: dataCmds, cmdFn: display, helpMsg: `Print value of an expression every time the program stops.

	display -a <expression>
	display -d <number>

The '-a' option adds an expression to the list of expression printed every time the program stops. The '-d' option removes the specified expression from the list.

If display is called without arguments it will print the value of all expression in the list.`},

		{aliases: []string{"dump"}, cmdFn: dump, helpMsg: `Creates a core dump from the current process state

	dump <output file>

The core dump is always written in ELF, even on systems (windows, macOS) where this is not customary. For environments other than linux/amd64 threads and registers are dumped in a format that only Delve can read back.`},
	}

	addrecorded := client == nil
	if !addrecorded {
		if state, err := client.GetStateNonBlocking(); err == nil {
			addrecorded = state.Recording
			if !addrecorded {
				addrecorded = client.Recorded()
			}
		}
	}

	if addrecorded {
		c.cmds = append(c.cmds,
			command{
				aliases: []string{"rewind", "rw"},
				group:   runCmds,
				cmdFn:   c.rewind,
				helpMsg: "Run backwards until breakpoint or program termination.",
			},
			command{
				aliases: []string{"check", "checkpoint"},
				cmdFn:   checkpoint,
				helpMsg: `Creates a checkpoint at the current position.

	checkpoint [note]

The "note" is arbitrary text that can be used to identify the checkpoint, if it is not specified it defaults to the current filename:line position.`,
			},
			command{
				aliases: []string{"checkpoints"},
				cmdFn:   checkpoints,
				helpMsg: "Print out info for existing checkpoints.",
			},
			command{
				aliases: []string{"clear-checkpoint", "clearcheck"},
				cmdFn:   clearCheckpoint,
				helpMsg: `Deletes checkpoint.

	clear-checkpoint <id>`,
			},
			command{
				aliases: []string{"rev"},
				group:   runCmds,
				cmdFn:   c.revCmd,
				helpMsg: `Reverses the execution of the target program for the command specified.
Currently, only the rev step-instruction command is supported.`,
			})
	}

	sort.Sort(byFirstAlias(c.cmds))
	return c
}

// Register custom commands. Expects cf to be a func of type cmdfunc,
// returning only an error.
func (c *Commands) Register(cmdstr string, cf cmdfunc, helpMsg string) {
	for _, v := range c.cmds {
		if v.match(cmdstr) {
			v.cmdFn = cf
			return
		}
	}

	c.cmds = append(c.cmds, command{aliases: []string{cmdstr}, cmdFn: cf, helpMsg: helpMsg})
}

// Find will look up the command function for the given command input.
// If it cannot find the command it will default to noCmdAvailable().
// If the command is an empty string it will replay the last command.
func (c *Commands) Find(cmdstr string, prefix cmdPrefix) cmdfunc {
	// If <enter> use last command, if there was one.
	if cmdstr == "" {
		return nullCommand
	}

	for _, v := range c.cmds {
		if v.match(cmdstr) {
			if prefix != noPrefix && v.allowedPrefixes&prefix == 0 {
				continue
			}
			return v.cmdFn
		}
	}

	return noCmdAvailable
}

// CallWithContext takes a command and a context that command should be executed in.
func (c *Commands) CallWithContext(cmdstr string, t *Term, ctx callContext) error {
	vals := strings.SplitN(strings.TrimSpace(cmdstr), " ", 2)
	cmdname := vals[0]
	var args string
	if len(vals) > 1 {
		args = strings.TrimSpace(vals[1])
	}
	return c.Find(cmdname, ctx.Prefix)(t, ctx, args)
}

// Call takes a command to execute.
func (c *Commands) Call(cmdstr string, t *Term) error {
	ctx := callContext{Prefix: noPrefix, Scope: api.EvalScope{GoroutineID: -1, Frame: c.frame, DeferredCall: 0}}
	return c.CallWithContext(cmdstr, t, ctx)
}

// Merge takes aliases defined in the config struct and merges them with the default aliases.
func (c *Commands) Merge(allAliases map[string][]string) {
	for i := range c.cmds {
		if c.cmds[i].builtinAliases != nil {
			c.cmds[i].aliases = append(c.cmds[i].aliases[:0], c.cmds[i].builtinAliases...)
		}
	}
	for i := range c.cmds {
		if aliases, ok := allAliases[c.cmds[i].aliases[0]]; ok {
			if c.cmds[i].builtinAliases == nil {
				c.cmds[i].builtinAliases = make([]string, len(c.cmds[i].aliases))
				copy(c.cmds[i].builtinAliases, c.cmds[i].aliases)
			}
			c.cmds[i].aliases = append(c.cmds[i].aliases, aliases...)
		}
	}
}

var noCmdError = errors.New("command not available")

func noCmdAvailable(t *Term, ctx callContext, args string) error {
	return noCmdError
}

func nullCommand(t *Term, ctx callContext, args string) error {
	return nil
}

func (c *Commands) help(t *Term, ctx callContext, args string) error {
	if args != "" {
		for _, cmd := range c.cmds {
			for _, alias := range cmd.aliases {
				if alias == args {
					fmt.Println(cmd.helpMsg)
					return nil
				}
			}
		}
		return noCmdError
	}

	fmt.Println("The following commands are available:")

	for _, cgd := range commandGroupDescriptions {
		fmt.Printf("\n%s:\n", cgd.description)
		w := new(tabwriter.Writer)
		w.Init(os.Stdout, 0, 8, 0, '-', 0)
		for _, cmd := range c.cmds {
			if cmd.group != cgd.group {
				continue
			}
			h := cmd.helpMsg
			if idx := strings.Index(h, "\n"); idx >= 0 {
				h = h[:idx]
			}
			if len(cmd.aliases) > 1 {
				fmt.Fprintf(w, "    %s (alias: %s) \t %s\n", cmd.aliases[0], strings.Join(cmd.aliases[1:], " | "), h)
			} else {
				fmt.Fprintf(w, "    %s \t %s\n", cmd.aliases[0], h)
			}
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}

	fmt.Println()
	fmt.Println("Type help followed by a command for full documentation.")
	return nil
}

type byThreadID []*api.Thread

func (a byThreadID) Len() int           { return len(a) }
func (a byThreadID) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byThreadID) Less(i, j int) bool { return a[i].ID < a[j].ID }

func threads(t *Term, ctx callContext, args string) error {
	threads, err := t.client.ListThreads()
	if err != nil {
		return err
	}
	state, err := t.client.GetState()
	if err != nil {
		return err
	}
	sort.Sort(byThreadID(threads))
	for _, th := range threads {
		prefix := "  "
		if state.CurrentThread != nil && state.CurrentThread.ID == th.ID {
			prefix = "* "
		}
		if th.Function != nil {
			fmt.Printf("%sThread %d at %#v %s:%d %s\n",
				prefix, th.ID, th.PC, t.formatPath(th.File),
				th.Line, th.Function.Name())
		} else {
			fmt.Printf("%sThread %s\n", prefix, t.formatThread(th))
		}
	}
	return nil
}

func thread(t *Term, ctx callContext, args string) error {
	if len(args) == 0 {
		return fmt.Errorf("you must specify a thread")
	}
	tid, err := strconv.Atoi(args)
	if err != nil {
		return err
	}
	oldState, err := t.client.GetState()
	if err != nil {
		return err
	}
	newState, err := t.client.SwitchThread(tid)
	if err != nil {
		return err
	}

	oldThread := "<none>"
	newThread := "<none>"
	if oldState.CurrentThread != nil {
		oldThread = strconv.Itoa(oldState.CurrentThread.ID)
	}
	if newState.CurrentThread != nil {
		newThread = strconv.Itoa(newState.CurrentThread.ID)
	}
	fmt.Printf("Switched from %s to %s\n", oldThread, newThread)
	return nil
}

type byGoroutineID []*api.Goroutine

func (a byGoroutineID) Len() int           { return len(a) }
func (a byGoroutineID) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byGoroutineID) Less(i, j int) bool { return a[i].ID < a[j].ID }

// The number of goroutines we're going to request on each RPC call
const goroutineBatchSize = 10000

type printGoroutinesFlags uint8

const (
	printGoroutinesStack printGoroutinesFlags = 1 << iota
	printGoroutinesLabels
)

func printGoroutines(t *Term, gs []*api.Goroutine, fgl formatGoroutineLoc, flags printGoroutinesFlags, state *api.DebuggerState) error {
	for _, g := range gs {
		prefix := "  "
		if state.SelectedGoroutine != nil && g.ID == state.SelectedGoroutine.ID {
			prefix = "* "
		}
		fmt.Printf("%sGoroutine %s\n", prefix, t.formatGoroutine(g, fgl))
		if flags&printGoroutinesLabels != 0 {
			writeGoroutineLabels(os.Stdout, g, "\t")
		}
		if flags&printGoroutinesStack != 0 {
			stack, err := t.client.Stacktrace(g.ID, 10, 0, nil)
			if err != nil {
				return err
			}
			printStack(t, os.Stdout, stack, "\t", false)
		}
	}
	return nil
}

func goroutines(t *Term, ctx callContext, argstr string) error {
	args := strings.Split(argstr, " ")
	var fgl = fglUserCurrent
	var flags printGoroutinesFlags

	switch len(args) {
	case 0:
		// nothing to do
	case 1, 2:
		for _, arg := range args {
			switch arg {
			case "-u":
				fgl = fglUserCurrent
			case "-r":
				fgl = fglRuntimeCurrent
			case "-g":
				fgl = fglGo
			case "-s":
				fgl = fglStart
			case "-t":
				flags |= printGoroutinesStack
			case "-l":
				flags |= printGoroutinesLabels
			case "":
				// nothing to do
			default:
				return fmt.Errorf("wrong argument: '%s'", arg)
			}
		}
	default:
		return fmt.Errorf("too many arguments")
	}
	state, err := t.client.GetState()
	if err != nil {
		return err
	}
	var (
		start = 0
		gslen = 0
		gs    []*api.Goroutine
	)
	t.longCommandStart()
	for start >= 0 {
		if t.longCommandCanceled() {
			fmt.Printf("interrupted\n")
			return nil
		}
		gs, start, err = t.client.ListGoroutines(start, goroutineBatchSize)
		if err != nil {
			return err
		}
		sort.Sort(byGoroutineID(gs))
		err = printGoroutines(t, gs, fgl, flags, state)
		if err != nil {
			return err
		}
		gslen += len(gs)
	}
	fmt.Printf("[%d goroutines]\n", gslen)
	return nil
}

func selectedGID(state *api.DebuggerState) int {
	if state.SelectedGoroutine == nil {
		return 0
	}
	return state.SelectedGoroutine.ID
}

func split2PartsBySpace(s string) []string {
	v := strings.SplitN(s, " ", 2)
	for i, _ := range v {
		v[i] = strings.TrimSpace(v[i])
	}
	return v
}

func (c *Commands) goroutine(t *Term, ctx callContext, argstr string) error {
	args := split2PartsBySpace(argstr)

	if ctx.Prefix == onPrefix {
		if len(args) != 1 || args[0] != "" {
			return errors.New("too many arguments to goroutine")
		}
		ctx.Breakpoint.Goroutine = true
		return nil
	}

	if len(args) == 1 {
		if args[0] == "" {
			return printscope(t)
		}
		gid, err := strconv.Atoi(argstr)
		if err != nil {
			return err
		}

		oldState, err := t.client.GetState()
		if err != nil {
			return err
		}
		newState, err := t.client.SwitchGoroutine(gid)
		if err != nil {
			return err
		}
		c.frame = 0
		fmt.Printf("Switched from %d to %d (thread %d)\n", selectedGID(oldState), gid, newState.CurrentThread.ID)
		return nil
	}

	var err error
	ctx.Scope.GoroutineID, err = strconv.Atoi(args[0])
	if err != nil {
		return err
	}
	return c.CallWithContext(args[1], t, ctx)
}

// Handle "frame", "up", "down" commands.
func (c *Commands) frameCommand(t *Term, ctx callContext, argstr string, direction frameDirection) error {
	frame := 1
	arg := ""
	if len(argstr) == 0 {
		if direction == frameSet {
			return errors.New("not enough arguments")
		}
	} else {
		args := split2PartsBySpace(argstr)
		var err error
		if frame, err = strconv.Atoi(args[0]); err != nil {
			return err
		}
		if len(args) > 1 {
			arg = args[1]
		}
	}
	switch direction {
	case frameUp:
		frame = c.frame + frame
	case frameDown:
		frame = c.frame - frame
	}
	if len(arg) > 0 {
		ctx.Scope.Frame = frame
		return c.CallWithContext(arg, t, ctx)
	}
	if frame < 0 {
		return fmt.Errorf("Invalid frame %d", frame)
	}
	stack, err := t.client.Stacktrace(ctx.Scope.GoroutineID, frame, 0, nil)
	if err != nil {
		return err
	}
	if frame >= len(stack) {
		return fmt.Errorf("Invalid frame %d", frame)
	}
	c.frame = frame
	state, err := t.client.GetState()
	if err != nil {
		return err
	}
	printcontext(t, state)
	th := stack[frame]
	fmt.Printf("Frame %d: %s:%d (PC: %x)\n", frame, t.formatPath(th.File), th.Line, th.PC)
	printfile(t, th.File, th.Line, true)
	return nil
}

func (c *Commands) deferredCommand(t *Term, ctx callContext, argstr string) error {
	ctx.Prefix = deferredPrefix

	space := strings.Index(argstr, " ")

	var err error
	ctx.Scope.DeferredCall, err = strconv.Atoi(argstr[:space])
	if err != nil {
		return err
	}
	if ctx.Scope.DeferredCall <= 0 {
		return errors.New("argument of deferred must be a number greater than 0 (use 'stack -defer' to see the list of deferred calls)")
	}
	return c.CallWithContext(argstr[space:], t, ctx)
}

func printscope(t *Term) error {
	state, err := t.client.GetState()
	if err != nil {
		return err
	}

	fmt.Printf("Thread %s\n", t.formatThread(state.CurrentThread))
	if state.SelectedGoroutine != nil {
		writeGoroutineLong(t, os.Stdout, state.SelectedGoroutine, "")
	}
	return nil
}

func (t *Term) formatThread(th *api.Thread) string {
	if th == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%d at %s:%d", th.ID, t.formatPath(th.File), th.Line)
}

type formatGoroutineLoc int

const (
	fglRuntimeCurrent = formatGoroutineLoc(iota)
	fglUserCurrent
	fglGo
	fglStart
)

func (t *Term) formatLocation(loc api.Location) string {
	return fmt.Sprintf("%s:%d %s (%#v)", t.formatPath(loc.File), loc.Line, loc.Function.Name(), loc.PC)
}

func (t *Term) formatGoroutine(g *api.Goroutine, fgl formatGoroutineLoc) string {
	if g == nil {
		return "<nil>"
	}
	if g.Unreadable != "" {
		return fmt.Sprintf("(unreadable %s)", g.Unreadable)
	}
	var locname string
	var loc api.Location
	switch fgl {
	case fglRuntimeCurrent:
		locname = "Runtime"
		loc = g.CurrentLoc
	case fglUserCurrent:
		locname = "User"
		loc = g.UserCurrentLoc
	case fglGo:
		locname = "Go"
		loc = g.GoStatementLoc
	case fglStart:
		locname = "Start"
		loc = g.StartLoc
	}

	buf := new(strings.Builder)
	fmt.Fprintf(buf, "%d - %s: %s", g.ID, locname, t.formatLocation(loc))
	if g.ThreadID != 0 {
		fmt.Fprintf(buf, " (thread %d)", g.ThreadID)
	}

	if (g.Status == api.GoroutineWaiting || g.Status == api.GoroutineSyscall) && g.WaitReason != 0 {
		var wr string
		if g.WaitReason > 0 && g.WaitReason < int64(len(waitReasonStrings)) {
			wr = waitReasonStrings[g.WaitReason]
		} else {
			wr = fmt.Sprintf("unknown wait reason %d", g.WaitReason)
		}
		fmt.Fprintf(buf, " [%s", wr)
		if g.WaitSince > 0 {
			fmt.Fprintf(buf, " %s", time.Since(time.Unix(0, g.WaitSince)).String())
		}
		fmt.Fprintf(buf, "]")
	}

	return buf.String()
}

var waitReasonStrings = [...]string{
	"",
	"GC assist marking",
	"IO wait",
	"chan receive (nil chan)",
	"chan send (nil chan)",
	"dumping heap",
	"garbage collection",
	"garbage collection scan",
	"panicwait",
	"select",
	"select (no cases)",
	"GC assist wait",
	"GC sweep wait",
	"GC scavenge wait",
	"chan receive",
	"chan send",
	"finalizer wait",
	"force gc (idle)",
	"semacquire",
	"sleep",
	"sync.Cond.Wait",
	"timer goroutine (idle)",
	"trace reader (blocked)",
	"wait for GC cycle",
	"GC worker (idle)",
	"preempted",
	"debug call",
}

func writeGoroutineLong(t *Term, w io.Writer, g *api.Goroutine, prefix string) {
	fmt.Fprintf(w, "%sGoroutine %d:\n%s\tRuntime: %s\n%s\tUser: %s\n%s\tGo: %s\n%s\tStart: %s\n",
		prefix, g.ID,
		prefix, t.formatLocation(g.CurrentLoc),
		prefix, t.formatLocation(g.UserCurrentLoc),
		prefix, t.formatLocation(g.GoStatementLoc),
		prefix, t.formatLocation(g.StartLoc))
	writeGoroutineLabels(w, g, prefix+"\t")
}

func writeGoroutineLabels(w io.Writer, g *api.Goroutine, prefix string) {
	const maxNumberOfGoroutineLabels = 5

	if len(g.Labels) <= 0 {
		return
	}

	keys := make([]string, 0, len(g.Labels))
	for k := range g.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	more := false
	if len(keys) > maxNumberOfGoroutineLabels {
		more = true
		keys = keys[:maxNumberOfGoroutineLabels]
	}
	fmt.Fprintf(w, "%sLabels: ", prefix)
	for i, k := range keys {
		fmt.Fprintf(w, "%q:%q", k, g.Labels[k])
		if i != len(keys)-1 {
			fmt.Fprintf(w, ", ")
		} else if more {
			fmt.Fprintf(w, "... (%d more)", len(g.Labels)-maxNumberOfGoroutineLabels)
		}
	}
	fmt.Fprintf(w, "\n")
}

func restart(t *Term, ctx callContext, args string) error {
	if t.client.Recorded() {
		return restartRecorded(t, ctx, args)
	}

	return restartLive(t, ctx, args)
}

func restartRecorded(t *Term, ctx callContext, args string) error {
	v := split2PartsBySpace(args)

	rerecord := false
	resetArgs := false
	newArgv := []string{}
	newRedirects := [3]string{}
	restartPos := ""

	if len(v) > 0 {
		if v[0] == "-r" {
			rerecord = true
			if len(v) == 2 {
				var err error
				resetArgs, newArgv, newRedirects, err = parseNewArgv(v[1])
				if err != nil {
					return err
				}
			}
		} else {
			if len(v) > 1 {
				return fmt.Errorf("too many arguments to restart")
			}
			restartPos = v[0]
		}
	}

	if err := restartIntl(t, rerecord, restartPos, resetArgs, newArgv, newRedirects); err != nil {
		return err
	}

	state, err := t.client.GetState()
	if err != nil {
		return err
	}
	printcontext(t, state)
	printfile(t, state.CurrentThread.File, state.CurrentThread.Line, true)
	t.onStop()
	return nil
}

// parseOptionalCount parses an optional count argument.
// If there are not arguments, a value of 1 is returned as the default.
func parseOptionalCount(arg string) (int64, error) {
	if len(arg) == 0 {
		return 1, nil
	}
	return strconv.ParseInt(arg, 0, 64)
}

func restartLive(t *Term, ctx callContext, args string) error {
	resetArgs, newArgv, newRedirects, err := parseNewArgv(args)
	if err != nil {
		return err
	}

	if err := restartIntl(t, false, "", resetArgs, newArgv, newRedirects); err != nil {
		return err
	}

	fmt.Println("Process restarted with PID", t.client.ProcessPid())
	return nil
}

func restartIntl(t *Term, rerecord bool, restartPos string, resetArgs bool, newArgv []string, newRedirects [3]string) error {
	discarded, err := t.client.RestartFrom(rerecord, restartPos, resetArgs, newArgv, newRedirects, false)
	if err != nil {
		return err
	}
	for i := range discarded {
		fmt.Printf("Discarded %s at %s: %v\n", formatBreakpointName(discarded[i].Breakpoint, false), t.formatBreakpointLocation(discarded[i].Breakpoint), discarded[i].Reason)
	}
	return nil
}

func parseNewArgv(args string) (resetArgs bool, newArgv []string, newRedirects [3]string, err error) {
	if args == "" {
		return false, nil, [3]string{}, nil
	}
	v, err := argv.Argv(args,
		func(s string) (string, error) {
			return "", fmt.Errorf("Backtick not supported in '%s'", s)
		},
		nil)
	if err != nil {
		return false, nil, [3]string{}, err
	}
	if len(v) != 1 {
		return false, nil, [3]string{}, fmt.Errorf("illegal commandline '%s'", args)
	}
	w := v[0]
	if len(w) == 0 {
		return false, nil, [3]string{}, nil
	}
	if w[0] == "-noargs" {
		if len(w) > 1 {
			return false, nil, [3]string{}, fmt.Errorf("too many arguments to restart")
		}
		return true, nil, [3]string{}, nil
	}
	redirs := [3]string{}
	for len(w) > 0 {
		var found bool
		var err error
		w, found, err = parseOneRedirect(w, &redirs)
		if err != nil {
			return false, nil, [3]string{}, err
		}
		if !found {
			break
		}
	}
	return true, w, redirs, nil
}

func parseOneRedirect(w []string, redirs *[3]string) ([]string, bool, error) {
	prefixes := []string{"<", ">", "2>"}
	names := []string{"stdin", "stdout", "stderr"}
	if len(w) >= 2 {
		for _, prefix := range prefixes {
			if w[len(w)-2] == prefix {
				w[len(w)-2] += w[len(w)-1]
				w = w[:len(w)-1]
				break
			}
		}
	}
	for i, prefix := range prefixes {
		if strings.HasPrefix(w[len(w)-1], prefix) {
			if redirs[i] != "" {
				return nil, false, fmt.Errorf("redirect error: %s redirected twice", names[i])
			}
			redirs[i] = w[len(w)-1][len(prefix):]
			return w[:len(w)-1], true, nil
		}
	}
	return w, false, nil
}

func printcontextNoState(t *Term) {
	state, _ := t.client.GetState()
	if state == nil || state.CurrentThread == nil {
		return
	}
	printcontext(t, state)
}

func (c *Commands) rebuild(t *Term, ctx callContext, args string) error {
	if ctx.Prefix == revPrefix {
		return c.rewind(t, ctx, args)
	}
	defer t.onStop()
	discarded, err := t.client.Restart(true)
	if len(discarded) > 0 {
		fmt.Printf("not all breakpoints could be restored.")
	}
	return err
}

func (c *Commands) cont(t *Term, ctx callContext, args string) error {
	if args != "" {
		tmp, err := setBreakpoint(t, ctx, false, args)
		if err != nil {
			return err
		}
		defer func() {
			for _, bp := range tmp {
				if _, err := t.client.ClearBreakpoint(bp.ID); err != nil {
					fmt.Printf("failed to clear temporary breakpoint: %d", bp.ID)
				}
			}
		}()
	}
	if ctx.Prefix == revPrefix {
		return c.rewind(t, ctx, args)
	}
	defer t.onStop()
	c.frame = 0
	stateChan := t.client.Continue()
	var state *api.DebuggerState
	for state = range stateChan {
		if state.Err != nil {
			printcontextNoState(t)
			return state.Err
		}
		printcontext(t, state)
	}
	printfile(t, state.CurrentThread.File, state.CurrentThread.Line, true)
	return nil
}

func continueUntilCompleteNext(t *Term, state *api.DebuggerState, op string, shouldPrintFile bool) error {
	defer t.onStop()
	if !state.NextInProgress {
		if shouldPrintFile {
			printfile(t, state.CurrentThread.File, state.CurrentThread.Line, true)
		}
		return nil
	}
	for {
		fmt.Printf("\tbreakpoint hit during %s, continuing...\n", op)
		stateChan := t.client.DirectionCongruentContinue()
		var state *api.DebuggerState
		for state = range stateChan {
			if state.Err != nil {
				printcontextNoState(t)
				return state.Err
			}
			printcontext(t, state)
		}
		if !state.NextInProgress {
			printfile(t, state.CurrentThread.File, state.CurrentThread.Line, true)
			return nil
		}
	}
}

func scopePrefixSwitch(t *Term, ctx callContext) error {
	if ctx.Scope.GoroutineID > 0 {
		_, err := t.client.SwitchGoroutine(ctx.Scope.GoroutineID)
		if err != nil {
			return err
		}
	}
	return nil
}

func exitedToError(state *api.DebuggerState, err error) (*api.DebuggerState, error) {
	if err == nil && state.Exited {
		return nil, fmt.Errorf("Process has exited with status %d", state.ExitStatus)
	}
	return state, err
}

func (c *Commands) step(t *Term, ctx callContext, args string) error {
	if err := scopePrefixSwitch(t, ctx); err != nil {
		return err
	}
	c.frame = 0
	stepfn := t.client.Step
	if ctx.Prefix == revPrefix {
		stepfn = t.client.ReverseStep
	}
	state, err := exitedToError(stepfn())
	if err != nil {
		printcontextNoState(t)
		return err
	}
	printcontext(t, state)
	return continueUntilCompleteNext(t, state, "step", true)
}

var notOnFrameZeroErr = errors.New("not on topmost frame")

func (c *Commands) stepInstruction(t *Term, ctx callContext, args string) error {
	if err := scopePrefixSwitch(t, ctx); err != nil {
		return err
	}
	if c.frame != 0 {
		return notOnFrameZeroErr
	}

	defer t.onStop()

	var fn func() (*api.DebuggerState, error)
	if ctx.Prefix == revPrefix {
		fn = t.client.ReverseStepInstruction
	} else {
		fn = t.client.StepInstruction
	}

	state, err := exitedToError(fn())
	if err != nil {
		printcontextNoState(t)
		return err
	}
	printcontext(t, state)
	printfile(t, state.CurrentThread.File, state.CurrentThread.Line, true)
	return nil
}

func (c *Commands) revCmd(t *Term, ctx callContext, args string) error {
	if len(args) == 0 {
		return errors.New("not enough arguments")
	}

	ctx.Prefix = revPrefix
	return c.CallWithContext(args, t, ctx)
}

func (c *Commands) next(t *Term, ctx callContext, args string) error {
	if err := scopePrefixSwitch(t, ctx); err != nil {
		return err
	}
	if c.frame != 0 {
		return notOnFrameZeroErr
	}

	nextfn := t.client.Next
	if ctx.Prefix == revPrefix {
		nextfn = t.client.ReverseNext
	}

	var count int64
	var err error
	if count, err = parseOptionalCount(args); err != nil {
		return err
	} else if count <= 0 {
		return errors.New("Invalid next count")
	}
	for ; count > 0; count-- {
		state, err := exitedToError(nextfn())
		if err != nil {
			printcontextNoState(t)
			return err
		}
		// If we're about the exit the loop, print the context.
		finishedNext := count == 1
		if finishedNext {
			printcontext(t, state)
		}
		if err := continueUntilCompleteNext(t, state, "next", finishedNext); err != nil {
			return err
		}
	}
	return nil
}

func (c *Commands) stepout(t *Term, ctx callContext, args string) error {
	if err := scopePrefixSwitch(t, ctx); err != nil {
		return err
	}
	if c.frame != 0 {
		return notOnFrameZeroErr
	}

	stepoutfn := t.client.StepOut
	if ctx.Prefix == revPrefix {
		stepoutfn = t.client.ReverseStepOut
	}

	state, err := exitedToError(stepoutfn())
	if err != nil {
		printcontextNoState(t)
		return err
	}
	printcontext(t, state)
	return continueUntilCompleteNext(t, state, "stepout", true)
}

func (c *Commands) call(t *Term, ctx callContext, args string) error {
	if err := scopePrefixSwitch(t, ctx); err != nil {
		return err
	}
	const unsafePrefix = "-unsafe "
	unsafe := false
	if strings.HasPrefix(args, unsafePrefix) {
		unsafe = true
		args = args[len(unsafePrefix):]
	}
	state, err := exitedToError(t.client.Call(ctx.Scope.GoroutineID, args, unsafe))
	c.frame = 0
	if err != nil {
		printcontextNoState(t)
		return err
	}
	printcontext(t, state)
	return continueUntilCompleteNext(t, state, "call", true)
}

func clear(t *Term, ctx callContext, args string) error {
	if len(args) == 0 {
		return fmt.Errorf("not enough arguments")
	}
	id, err := strconv.Atoi(args)
	var bp *api.Breakpoint
	if err == nil {
		bp, err = t.client.ClearBreakpoint(id)
	} else {
		bp, err = t.client.ClearBreakpointByName(args)
	}
	if err != nil {
		return err
	}
	fmt.Printf("%s cleared at %s\n", formatBreakpointName(bp, true), t.formatBreakpointLocation(bp))
	return nil
}

func clearAll(t *Term, ctx callContext, args string) error {
	breakPoints, err := t.client.ListBreakpoints()
	if err != nil {
		return err
	}

	var locPCs map[uint64]struct{}
	if args != "" {
		locs, err := t.client.FindLocation(api.EvalScope{GoroutineID: -1, Frame: 0}, args, true, t.substitutePathRules())
		if err != nil {
			return err
		}
		locPCs = make(map[uint64]struct{})
		for _, loc := range locs {
			for _, pc := range loc.PCs {
				locPCs[pc] = struct{}{}
			}
			locPCs[loc.PC] = struct{}{}
		}
	}

	for _, bp := range breakPoints {
		if locPCs != nil {
			if _, ok := locPCs[bp.Addr]; !ok {
				continue
			}
		}

		if bp.ID < 0 {
			continue
		}

		_, err := t.client.ClearBreakpoint(bp.ID)
		if err != nil {
			fmt.Printf("Couldn't delete %s at %s: %s\n", formatBreakpointName(bp, false), t.formatBreakpointLocation(bp), err)
		}
		fmt.Printf("%s cleared at %s\n", formatBreakpointName(bp, true), t.formatBreakpointLocation(bp))
	}
	return nil
}

// byID sorts breakpoints by ID.
type byID []*api.Breakpoint

func (a byID) Len() int           { return len(a) }
func (a byID) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a byID) Less(i, j int) bool { return a[i].ID < a[j].ID }

func breakpoints(t *Term, ctx callContext, args string) error {
	breakPoints, err := t.client.ListBreakpoints()
	if err != nil {
		return err
	}
	sort.Sort(byID(breakPoints))
	for _, bp := range breakPoints {
		fmt.Printf("%s at %v (%d)\n", formatBreakpointName(bp, true), t.formatBreakpointLocation(bp), bp.TotalHitCount)

		var attrs []string
		if bp.Cond != "" {
			attrs = append(attrs, fmt.Sprintf("\tcond %s", bp.Cond))
		}
		if bp.Stacktrace > 0 {
			attrs = append(attrs, fmt.Sprintf("\tstack %d", bp.Stacktrace))
		}
		if bp.Goroutine {
			attrs = append(attrs, "\tgoroutine")
		}
		if bp.LoadArgs != nil {
			if *(bp.LoadArgs) == longLoadConfig {
				attrs = append(attrs, "\targs -v")
			} else {
				attrs = append(attrs, "\targs")
			}
		}
		if bp.LoadLocals != nil {
			if *(bp.LoadLocals) == longLoadConfig {
				attrs = append(attrs, "\tlocals -v")
			} else {
				attrs = append(attrs, "\tlocals")
			}
		}
		for i := range bp.Variables {
			attrs = append(attrs, fmt.Sprintf("\tprint %s", bp.Variables[i]))
		}
		if len(attrs) > 0 {
			fmt.Printf("%s\n", strings.Join(attrs, "\n"))
		}
	}
	return nil
}

func setBreakpoint(t *Term, ctx callContext, tracepoint bool, argstr string) ([]*api.Breakpoint, error) {
	args := split2PartsBySpace(argstr)

	requestedBp := &api.Breakpoint{}
	spec := ""
	switch len(args) {
	case 1:
		spec = argstr
	case 2:
		if api.ValidBreakpointName(args[0]) == nil {
			requestedBp.Name = args[0]
			spec = args[1]
		} else {
			spec = argstr
		}
	default:
		return nil, fmt.Errorf("address required")
	}

	requestedBp.Tracepoint = tracepoint
	locs, err := t.client.FindLocation(ctx.Scope, spec, true, t.substitutePathRules())
	if err != nil {
		if requestedBp.Name == "" {
			return nil, err
		}
		requestedBp.Name = ""
		spec = argstr
		var err2 error
		locs, err2 = t.client.FindLocation(ctx.Scope, spec, true, t.substitutePathRules())
		if err2 != nil {
			return nil, err
		}
	}
	created := []*api.Breakpoint{}
	for _, loc := range locs {
		requestedBp.Addr = loc.PC
		requestedBp.Addrs = loc.PCs
		if tracepoint {
			requestedBp.LoadArgs = &ShortLoadConfig
		}

		bp, err := t.client.CreateBreakpoint(requestedBp)
		if err != nil {
			return nil, err
		}
		created = append(created, bp)

		fmt.Printf("%s set at %s\n", formatBreakpointName(bp, true), t.formatBreakpointLocation(bp))
	}

	var shouldSetReturnBreakpoints bool
	loc, err := locspec.Parse(spec)
	if err != nil {
		return nil, err
	}
	switch t := loc.(type) {
	case *locspec.NormalLocationSpec:
		shouldSetReturnBreakpoints = t.LineOffset == -1 && t.FuncBase != nil
	case *locspec.RegexLocationSpec:
		shouldSetReturnBreakpoints = true
	}
	if tracepoint && shouldSetReturnBreakpoints && locs[0].Function != nil {
		for i := range locs {
			if locs[i].Function == nil {
				continue
			}
			addrs, err := t.client.(*rpc2.RPCClient).FunctionReturnLocations(locs[0].Function.Name())
			if err != nil {
				return nil, err
			}
			for j := range addrs {
				_, err = t.client.CreateBreakpoint(&api.Breakpoint{
					Addr:        addrs[j],
					TraceReturn: true,
					Line:        -1,
					LoadArgs:    &ShortLoadConfig,
				})
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return created, nil
}

func breakpoint(t *Term, ctx callContext, args string) error {
	_, err := setBreakpoint(t, ctx, false, args)
	return err
}

func tracepoint(t *Term, ctx callContext, args string) error {
	_, err := setBreakpoint(t, ctx, true, args)
	return err
}

func edit(t *Term, ctx callContext, args string) error {
	file, lineno, _, err := getLocation(t, ctx, args, false)
	if err != nil {
		return err
	}

	var editor string
	if editor = os.Getenv("DELVE_EDITOR"); editor == "" {
		if editor = os.Getenv("EDITOR"); editor == "" {
			return fmt.Errorf("Neither DELVE_EDITOR or EDITOR is set")
		}
	}

	cmd := exec.Command(editor, fmt.Sprintf("+%d", lineno), file)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func examineMemoryCmd(t *Term, ctx callContext, args string) error {
	v := strings.FieldsFunc(args, func(c rune) bool {
		return c == ' '
	})

	var (
		address uint64
		err     error
		ok      bool
	)

	// Default value
	priFmt := byte('x')
	count := 1
	size := 1

	for i := 0; i < len(v); i++ {
		switch v[i] {
		case "-fmt":
			i++
			if i >= len(v) {
				return fmt.Errorf("expected argument after -fmt")
			}
			fmtMapToPriFmt := map[string]byte{
				"oct":         'o',
				"octal":       'o',
				"hex":         'x',
				"hexadecimal": 'x',
				"dec":         'd',
				"decimal":     'd',
				"bin":         'b',
				"binary":      'b',
			}
			priFmt, ok = fmtMapToPriFmt[v[i]]
			if !ok {
				return fmt.Errorf("%q is not a valid format", v[i])
			}
		case "-count", "-len":
			i++
			if i >= len(v) {
				return fmt.Errorf("expected argument after -count/-len")
			}
			var err error
			count, err = strconv.Atoi(v[i])
			if err != nil || count <= 0 {
				return fmt.Errorf("count/len must be a positive integer")
			}
		case "-size":
			i++
			if i >= len(v) {
				return fmt.Errorf("expected argument after -size")
			}
			var err error
			size, err = strconv.Atoi(v[i])
			if err != nil || size <= 0 || size > 8 {
				return fmt.Errorf("size must be a positive integer (<=8)")
			}
		default:
			if i != len(v)-1 {
				return fmt.Errorf("unknown option %q", v[i])
			}
			// TODO, maybe we can support expression.
			address, err = strconv.ParseUint(v[len(v)-1], 0, 64)
			if err != nil {
				return fmt.Errorf("convert address into uintptr type failed, %s", err)
			}
		}
	}

	// TODO, maybe configured by user.
	if count*size > 1000 {
		return fmt.Errorf("read memory range (count*size) must be less than or equal to 1000 bytes")
	}

	if address == 0 {
		return fmt.Errorf("no address specified")
	}

	memArea, isLittleEndian, err := t.client.ExamineMemory(address, count*size)
	if err != nil {
		return err
	}
	fmt.Print(api.PrettyExamineMemory(uintptr(address), memArea, isLittleEndian, priFmt, size))
	return nil
}

func printVar(t *Term, ctx callContext, args string) error {
	if len(args) == 0 {
		return fmt.Errorf("not enough arguments")
	}
	if ctx.Prefix == onPrefix {
		ctx.Breakpoint.Variables = append(ctx.Breakpoint.Variables, args)
		return nil
	}
	val, err := t.client.EvalVariable(ctx.Scope, args, t.loadConfig())
	if err != nil {
		return err
	}

	fmt.Println(val.MultilineString(""))
	return nil
}

func whatisCommand(t *Term, ctx callContext, args string) error {
	if len(args) == 0 {
		return fmt.Errorf("not enough arguments")
	}
	val, err := t.client.EvalVariable(ctx.Scope, args, ShortLoadConfig)
	if err != nil {
		return err
	}
	if val.Type != "" {
		fmt.Println(val.Type)
	}
	if val.RealType != val.Type {
		fmt.Printf("Real type: %s\n", val.RealType)
	}
	if val.Kind == reflect.Interface && len(val.Children) > 0 {
		fmt.Printf("Concrete type: %s\n", val.Children[0].Type)
	}
	if t.conf.ShowLocationExpr && val.LocationExpr != "" {
		fmt.Printf("location: %s\n", val.LocationExpr)
	}
	return nil
}

func setVar(t *Term, ctx callContext, args string) error {
	// HACK: in go '=' is not an operator, we detect the error and try to recover from it by splitting the input string
	_, err := parser.ParseExpr(args)
	if err == nil {
		return fmt.Errorf("syntax error '=' not found")
	}

	el, ok := err.(scanner.ErrorList)
	if !ok || el[0].Msg != "expected '==', found '='" {
		return err
	}

	lexpr := args[:el[0].Pos.Offset]
	rexpr := args[el[0].Pos.Offset+1:]
	return t.client.SetVariable(ctx.Scope, lexpr, rexpr)
}

func printFilteredVariables(varType string, vars []api.Variable, filter string, cfg api.LoadConfig) error {
	reg, err := regexp.Compile(filter)
	if err != nil {
		return err
	}
	match := false
	for _, v := range vars {
		if reg == nil || reg.Match([]byte(v.Name)) {
			match = true
			name := v.Name
			if v.Flags&api.VariableShadowed != 0 {
				name = "(" + name + ")"
			}
			if cfg == ShortLoadConfig {
				fmt.Printf("%s = %s\n", name, v.SinglelineString())
			} else {
				fmt.Printf("%s = %s\n", name, v.MultilineString(""))
			}
		}
	}
	if !match {
		fmt.Printf("(no %s)\n", varType)
	}
	return nil
}

func printSortedStrings(v []string, err error) error {
	if err != nil {
		return err
	}
	sort.Strings(v)
	for _, d := range v {
		fmt.Println(d)
	}
	return nil
}

func sources(t *Term, ctx callContext, args string) error {
	return printSortedStrings(t.client.ListSources(args))
}

func funcs(t *Term, ctx callContext, args string) error {
	return printSortedStrings(t.client.ListFunctions(args))
}

func types(t *Term, ctx callContext, args string) error {
	return printSortedStrings(t.client.ListTypes(args))
}

func parseVarArguments(args string, t *Term) (filter string, cfg api.LoadConfig) {
	if v := split2PartsBySpace(args); len(v) >= 1 && v[0] == "-v" {
		if len(v) == 2 {
			return v[1], t.loadConfig()
		} else {
			return "", t.loadConfig()
		}
	}
	return args, ShortLoadConfig
}

func args(t *Term, ctx callContext, args string) error {
	filter, cfg := parseVarArguments(args, t)
	if ctx.Prefix == onPrefix {
		if filter != "" {
			return fmt.Errorf("filter not supported on breakpoint")
		}
		ctx.Breakpoint.LoadArgs = &cfg
		return nil
	}
	vars, err := t.client.ListFunctionArgs(ctx.Scope, cfg)
	if err != nil {
		return err
	}
	return printFilteredVariables("args", vars, filter, cfg)
}

func locals(t *Term, ctx callContext, args string) error {
	filter, cfg := parseVarArguments(args, t)
	if ctx.Prefix == onPrefix {
		if filter != "" {
			return fmt.Errorf("filter not supported on breakpoint")
		}
		ctx.Breakpoint.LoadLocals = &cfg
		return nil
	}
	locals, err := t.client.ListLocalVariables(ctx.Scope, cfg)
	if err != nil {
		return err
	}
	return printFilteredVariables("locals", locals, filter, cfg)
}

func vars(t *Term, ctx callContext, args string) error {
	filter, cfg := parseVarArguments(args, t)
	vars, err := t.client.ListPackageVariables(filter, cfg)
	if err != nil {
		return err
	}
	return printFilteredVariables("vars", vars, filter, cfg)
}

func regs(t *Term, ctx callContext, args string) error {
	includeFp := false
	if args == "-a" {
		includeFp = true
	}
	var regs api.Registers
	var err error
	if ctx.Scope.GoroutineID < 0 && ctx.Scope.Frame == 0 {
		regs, err = t.client.ListThreadRegisters(0, includeFp)
	} else {
		regs, err = t.client.ListScopeRegisters(ctx.Scope, includeFp)
	}
	if err != nil {
		return err
	}
	fmt.Println(regs)
	return nil
}

func stackCommand(t *Term, ctx callContext, args string) error {
	sa, err := parseStackArgs(args)
	if err != nil {
		return err
	}
	if ctx.Prefix == onPrefix {
		ctx.Breakpoint.Stacktrace = sa.depth
		return nil
	}
	var cfg *api.LoadConfig
	if sa.full {
		cfg = &ShortLoadConfig
	}
	stack, err := t.client.Stacktrace(ctx.Scope.GoroutineID, sa.depth, sa.opts, cfg)
	if err != nil {
		return err
	}
	printStack(t, os.Stdout, stack, "", sa.offsets)
	if sa.ancestors > 0 {
		ancestors, err := t.client.Ancestors(ctx.Scope.GoroutineID, sa.ancestors, sa.ancestorDepth)
		if err != nil {
			return err
		}
		for _, ancestor := range ancestors {
			fmt.Printf("Created by Goroutine %d:\n", ancestor.ID)
			if ancestor.Unreadable != "" {
				fmt.Printf("\t%s\n", ancestor.Unreadable)
				continue
			}
			printStack(t, os.Stdout, ancestor.Stack, "\t", false)
		}
	}
	return nil
}

type stackArgs struct {
	depth   int
	full    bool
	offsets bool
	opts    api.StacktraceOptions

	ancestors     int
	ancestorDepth int
}

func parseStackArgs(argstr string) (stackArgs, error) {
	r := stackArgs{
		depth: 50,
		full:  false,
	}
	if argstr != "" {
		args := strings.Split(argstr, " ")
		for i := 0; i < len(args); i++ {
			numarg := func(name string) (int, error) {
				if i >= len(args) {
					return 0, fmt.Errorf("expected number after %s", name)
				}
				n, err := strconv.Atoi(args[i])
				if err != nil {
					return 0, fmt.Errorf("expected number after %s: %v", name, err)
				}
				return n, nil

			}
			switch args[i] {
			case "-full":
				r.full = true
			case "-offsets":
				r.offsets = true
			case "-defer":
				r.opts |= api.StacktraceReadDefers
			case "-mode":
				i++
				if i >= len(args) {
					return stackArgs{}, fmt.Errorf("expected normal, simple or fromg after -mode")
				}
				switch args[i] {
				case "normal":
					r.opts &^= api.StacktraceSimple
					r.opts &^= api.StacktraceG
				case "simple":
					r.opts |= api.StacktraceSimple
				case "fromg":
					r.opts |= api.StacktraceG | api.StacktraceSimple
				default:
					return stackArgs{}, fmt.Errorf("expected normal, simple or fromg after -mode")
				}
			case "-a":
				i++
				n, err := numarg("-a")
				if err != nil {
					return stackArgs{}, err
				}
				r.ancestors = n
			case "-adepth":
				i++
				n, err := numarg("-adepth")
				if err != nil {
					return stackArgs{}, err
				}
				r.ancestorDepth = n
			default:
				n, err := strconv.Atoi(args[i])
				if err != nil {
					return stackArgs{}, fmt.Errorf("depth must be a number")
				}
				r.depth = n
			}
		}
	}
	if r.ancestors > 0 && r.ancestorDepth == 0 {
		r.ancestorDepth = r.depth
	}
	return r, nil
}

// getLocation returns the current location or the locations specified by the argument.
// getLocation is used to process the argument of list and edit commands.
func getLocation(t *Term, ctx callContext, args string, showContext bool) (file string, lineno int, showarrow bool, err error) {
	switch {
	case len(args) == 0 && !ctx.scoped():
		state, err := t.client.GetState()
		if err != nil {
			return "", 0, false, err
		}
		if showContext {
			printcontext(t, state)
		}
		if state.SelectedGoroutine != nil {
			return state.SelectedGoroutine.CurrentLoc.File, state.SelectedGoroutine.CurrentLoc.Line, true, nil
		}
		return state.CurrentThread.File, state.CurrentThread.Line, true, nil

	case len(args) == 0 && ctx.scoped():
		locs, err := t.client.Stacktrace(ctx.Scope.GoroutineID, ctx.Scope.Frame, 0, nil)
		if err != nil {
			return "", 0, false, err
		}
		if ctx.Scope.Frame >= len(locs) {
			return "", 0, false, fmt.Errorf("Frame %d does not exist in goroutine %d", ctx.Scope.Frame, ctx.Scope.GoroutineID)
		}
		loc := locs[ctx.Scope.Frame]
		gid := ctx.Scope.GoroutineID
		if gid < 0 {
			state, err := t.client.GetState()
			if err != nil {
				return "", 0, false, err
			}
			if state.SelectedGoroutine != nil {
				gid = state.SelectedGoroutine.ID
			}
		}
		if showContext {
			fmt.Printf("Goroutine %d frame %d at %s:%d (PC: %#x)\n", gid, ctx.Scope.Frame, loc.File, loc.Line, loc.PC)
		}
		return loc.File, loc.Line, true, nil

	default:
		locs, err := t.client.FindLocation(ctx.Scope, args, false, t.substitutePathRules())
		if err != nil {
			return "", 0, false, err
		}
		if len(locs) > 1 {
			return "", 0, false, locspec.AmbiguousLocationError{Location: args, CandidatesLocation: locs}
		}
		loc := locs[0]
		if showContext {
			fmt.Printf("Showing %s:%d (PC: %#x)\n", loc.File, loc.Line, loc.PC)
		}
		return loc.File, loc.Line, false, nil
	}
}

func listCommand(t *Term, ctx callContext, args string) error {
	file, lineno, showarrow, err := getLocation(t, ctx, args, true)
	if err != nil {
		return err
	}
	return printfile(t, file, lineno, showarrow)
}

func (c *Commands) sourceCommand(t *Term, ctx callContext, args string) error {
	if len(args) == 0 {
		return fmt.Errorf("wrong number of arguments: source <filename>")
	}

	if filepath.Ext(args) == ".star" {
		_, err := t.starlarkEnv.Execute(args, nil, "main", nil)
		return err
	}

	if args == "-" {
		return t.starlarkEnv.REPL()
	}

	return c.executeFile(t, args)
}

var disasmUsageError = errors.New("wrong number of arguments: disassemble [-a <start> <end>] [-l <locspec>]")

func disassCommand(t *Term, ctx callContext, args string) error {
	var cmd, rest string

	if args != "" {
		argv := split2PartsBySpace(args)
		if len(argv) != 2 {
			return disasmUsageError
		}
		cmd = argv[0]
		rest = argv[1]
	}

	flavor := api.IntelFlavour
	if t.conf != nil && t.conf.DisassembleFlavor != nil {
		switch *t.conf.DisassembleFlavor {
		case "go":
			flavor = api.GoFlavour
		case "gnu":
			flavor = api.GNUFlavour
		default:
			flavor = api.IntelFlavour
		}
	}

	var disasm api.AsmInstructions
	var disasmErr error

	switch cmd {
	case "":
		locs, err := t.client.FindLocation(ctx.Scope, "+0", true, t.substitutePathRules())
		if err != nil {
			return err
		}
		disasm, disasmErr = t.client.DisassemblePC(ctx.Scope, locs[0].PC, flavor)
	case "-a":
		v := split2PartsBySpace(rest)
		if len(v) != 2 {
			return disasmUsageError
		}
		startpc, err := strconv.ParseInt(v[0], 0, 64)
		if err != nil {
			return fmt.Errorf("wrong argument: %q is not a number", v[0])
		}
		endpc, err := strconv.ParseInt(v[1], 0, 64)
		if err != nil {
			return fmt.Errorf("wrong argument: %q is not a number", v[1])
		}
		disasm, disasmErr = t.client.DisassembleRange(ctx.Scope, uint64(startpc), uint64(endpc), flavor)
	case "-l":
		locs, err := t.client.FindLocation(ctx.Scope, rest, true, t.substitutePathRules())
		if err != nil {
			return err
		}
		if len(locs) != 1 {
			return errors.New("expression specifies multiple locations")
		}
		disasm, disasmErr = t.client.DisassemblePC(ctx.Scope, locs[0].PC, flavor)
	default:
		return disasmUsageError
	}

	if disasmErr != nil {
		return disasmErr
	}

	disasmPrint(disasm, os.Stdout)

	return nil
}

func libraries(t *Term, ctx callContext, args string) error {
	libs, err := t.client.ListDynamicLibraries()
	if err != nil {
		return err
	}
	d := digits(len(libs))
	for i := range libs {
		fmt.Printf("%"+strconv.Itoa(d)+"d. %#x %s\n", i, libs[i].Address, libs[i].Path)
	}
	return nil
}

func digits(n int) int {
	if n <= 0 {
		return 1
	}
	return int(math.Floor(math.Log10(float64(n)))) + 1
}

const stacktraceTruncatedMessage = "(truncated)"

func printStack(t *Term, out io.Writer, stack []api.Stackframe, ind string, offsets bool) {
	if len(stack) == 0 {
		return
	}

	extranl := offsets
	for i := range stack {
		if extranl {
			break
		}
		extranl = extranl || (len(stack[i].Defers) > 0) || (len(stack[i].Arguments) > 0) || (len(stack[i].Locals) > 0)
	}

	d := digits(len(stack) - 1)
	fmtstr := "%s%" + strconv.Itoa(d) + "d  0x%016x in %s\n"
	s := ind + strings.Repeat(" ", d+2+len(ind))

	for i := range stack {
		if stack[i].Err != "" {
			fmt.Fprintf(out, "%serror: %s\n", s, stack[i].Err)
			continue
		}
		fmt.Fprintf(out, fmtstr, ind, i, stack[i].PC, stack[i].Function.Name())
		fmt.Fprintf(out, "%sat %s:%d\n", s, t.formatPath(stack[i].File), stack[i].Line)

		if offsets {
			fmt.Fprintf(out, "%sframe: %+#x frame pointer %+#x\n", s, stack[i].FrameOffset, stack[i].FramePointerOffset)
		}

		for j, d := range stack[i].Defers {
			deferHeader := fmt.Sprintf("%s    defer %d: ", s, j+1)
			s2 := strings.Repeat(" ", len(deferHeader))
			if d.Unreadable != "" {
				fmt.Fprintf(out, "%s(unreadable defer: %s)\n", deferHeader, d.Unreadable)
				continue
			}
			fmt.Fprintf(out, "%s%#016x in %s\n", deferHeader, d.DeferredLoc.PC, d.DeferredLoc.Function.Name())
			fmt.Fprintf(out, "%sat %s:%d\n", s2, t.formatPath(d.DeferredLoc.File), d.DeferredLoc.Line)
			fmt.Fprintf(out, "%sdeferred by %s at %s:%d\n", s2, d.DeferLoc.Function.Name(), t.formatPath(d.DeferLoc.File), d.DeferLoc.Line)
		}

		for j := range stack[i].Arguments {
			fmt.Fprintf(out, "%s    %s = %s\n", s, stack[i].Arguments[j].Name, stack[i].Arguments[j].SinglelineString())
		}
		for j := range stack[i].Locals {
			fmt.Fprintf(out, "%s    %s = %s\n", s, stack[i].Locals[j].Name, stack[i].Locals[j].SinglelineString())
		}

		if extranl {
			fmt.Fprintln(out)
		}
	}

	if len(stack) > 0 && !stack[len(stack)-1].Bottom {
		fmt.Fprintf(out, "%s"+stacktraceTruncatedMessage+"\n", ind)
	}
}

func printcontext(t *Term, state *api.DebuggerState) {
	for i := range state.Threads {
		if (state.CurrentThread != nil) && (state.Threads[i].ID == state.CurrentThread.ID) {
			continue
		}
		if state.Threads[i].Breakpoint != nil {
			printcontextThread(t, state.Threads[i])
		}
	}

	if state.CurrentThread == nil {
		fmt.Println("No current thread available")
		return
	}

	var th *api.Thread
	if state.SelectedGoroutine == nil {
		th = state.CurrentThread
	} else {
		for i := range state.Threads {
			if state.Threads[i].ID == state.SelectedGoroutine.ThreadID {
				th = state.Threads[i]
				break
			}
		}
		if th == nil {
			printcontextLocation(t, state.SelectedGoroutine.CurrentLoc)
			return
		}
	}

	if th.File == "" {
		fmt.Printf("Stopped at: 0x%x\n", state.CurrentThread.PC)
		_ = colorize.Print(t.stdout, "", bytes.NewReader([]byte("no source available")), 1, 10, 1, nil)
		return
	}

	printcontextThread(t, th)

	if state.When != "" {
		fmt.Println(state.When)
	}
}

func printcontextLocation(t *Term, loc api.Location) {
	fmt.Printf("> %s() %s:%d (PC: %#v)\n", loc.Function.Name(), t.formatPath(loc.File), loc.Line, loc.PC)
	if loc.Function != nil && loc.Function.Optimized {
		fmt.Println(optimizedFunctionWarning)
	}
}

func printReturnValues(th *api.Thread) {
	if th.ReturnValues == nil {
		return
	}
	fmt.Println("Values returned:")
	for _, v := range th.ReturnValues {
		fmt.Printf("\t%s: %s\n", v.Name, v.MultilineString("\t"))
	}
	fmt.Println()
}

func printcontextThread(t *Term, th *api.Thread) {
	fn := th.Function

	if th.Breakpoint == nil {
		printcontextLocation(t, api.Location{PC: th.PC, File: th.File, Line: th.Line, Function: th.Function})
		printReturnValues(th)
		return
	}

	args := ""
	var hasReturnValue bool
	if th.BreakpointInfo != nil && th.Breakpoint.LoadArgs != nil && *th.Breakpoint.LoadArgs == ShortLoadConfig {
		var arg []string
		for _, ar := range th.BreakpointInfo.Arguments {
			// For AI compatibility return values are included in the
			// argument list. This is a relic of the dark ages when the
			// Go debug information did not distinguish between the two.
			// Filter them out here instead, so during trace operations
			// they are not printed as an argument.
			if (ar.Flags & api.VariableArgument) != 0 {
				arg = append(arg, ar.SinglelineString())
			}
			if (ar.Flags & api.VariableReturnArgument) != 0 {
				hasReturnValue = true
			}
		}
		args = strings.Join(arg, ", ")
	}

	bpname := ""
	if th.Breakpoint.Name != "" {
		bpname = fmt.Sprintf("[%s] ", th.Breakpoint.Name)
	}

	if th.Breakpoint.Tracepoint || th.Breakpoint.TraceReturn {
		printTracepoint(t, th, bpname, fn, args, hasReturnValue)
		return
	}

	if hitCount, ok := th.Breakpoint.HitCount[strconv.Itoa(th.GoroutineID)]; ok {
		fmt.Printf("> %s%s(%s) %s:%d (hits goroutine(%d):%d total:%d) (PC: %#v)\n",
			bpname,
			fn.Name(),
			args,
			t.formatPath(th.File),
			th.Line,
			th.GoroutineID,
			hitCount,
			th.Breakpoint.TotalHitCount,
			th.PC)
	} else {
		fmt.Printf("> %s%s(%s) %s:%d (hits total:%d) (PC: %#v)\n",
			bpname,
			fn.Name(),
			args,
			t.formatPath(th.File),
			th.Line,
			th.Breakpoint.TotalHitCount,
			th.PC)
	}
	if th.Function != nil && th.Function.Optimized {
		fmt.Println(optimizedFunctionWarning)
	}

	printReturnValues(th)
	printBreakpointInfo(t, th, false)
}

func printBreakpointInfo(t *Term, th *api.Thread, tracepointOnNewline bool) {
	if th.BreakpointInfo == nil {
		return
	}
	bp := th.Breakpoint
	bpi := th.BreakpointInfo

	if bp.TraceReturn {
		return
	}

	didprintnl := tracepointOnNewline
	tracepointnl := func() {
		if !bp.Tracepoint || didprintnl {
			return
		}
		didprintnl = true
		fmt.Println()
	}

	if bpi.Goroutine != nil {
		tracepointnl()
		writeGoroutineLong(t, os.Stdout, bpi.Goroutine, "\t")
	}

	for _, v := range bpi.Variables {
		tracepointnl()
		fmt.Printf("\t%s: %s\n", v.Name, v.MultilineString("\t"))
	}

	for _, v := range bpi.Locals {
		tracepointnl()
		if *bp.LoadLocals == longLoadConfig {
			fmt.Printf("\t%s: %s\n", v.Name, v.MultilineString("\t"))
		} else {
			fmt.Printf("\t%s: %s\n", v.Name, v.SinglelineString())
		}
	}

	if bp.LoadArgs != nil && *bp.LoadArgs == longLoadConfig {
		for _, v := range bpi.Arguments {
			tracepointnl()
			fmt.Printf("\t%s: %s\n", v.Name, v.MultilineString("\t"))
		}
	}

	if bpi.Stacktrace != nil {
		tracepointnl()
		fmt.Printf("\tStack:\n")
		printStack(t, os.Stdout, bpi.Stacktrace, "\t\t", false)
	}
}

func printTracepoint(t *Term, th *api.Thread, bpname string, fn *api.Function, args string, hasReturnValue bool) {
	if th.Breakpoint.Tracepoint {
		fmt.Fprintf(os.Stderr, "> goroutine(%d): %s%s(%s)", th.GoroutineID, bpname, fn.Name(), args)
		if !hasReturnValue {
			fmt.Println()
		}
		printBreakpointInfo(t, th, !hasReturnValue)
	}
	if th.Breakpoint.TraceReturn {
		retVals := make([]string, 0, len(th.ReturnValues))
		for _, v := range th.ReturnValues {
			retVals = append(retVals, v.SinglelineString())
		}
		fmt.Fprintf(os.Stderr, " => (%s)\n", strings.Join(retVals, ","))
	}
	if th.Breakpoint.TraceReturn || !hasReturnValue {
		if th.BreakpointInfo != nil && th.BreakpointInfo.Stacktrace != nil {
			fmt.Fprintf(os.Stderr, "\tStack:\n")
			printStack(t, os.Stderr, th.BreakpointInfo.Stacktrace, "\t\t", false)
		}
	}
}

func printfile(t *Term, filename string, line int, showArrow bool) error {
	if filename == "" {
		return nil
	}

	lineCount := t.conf.GetSourceListLineCount()
	arrowLine := 0
	if showArrow {
		arrowLine = line
	}

	file, err := os.Open(t.substitutePath(filename))
	if err != nil {
		return err
	}
	defer file.Close()

	fi, _ := file.Stat()
	lastModExe := t.client.LastModified()
	if fi.ModTime().After(lastModExe) {
		fmt.Println("Warning: listing may not match stale executable")
	}

	return colorize.Print(t.stdout, file.Name(), file, line-lineCount, line+lineCount+1, arrowLine, t.colorEscapes)
}

// ExitRequestError is returned when the user
// exits Delve.
type ExitRequestError struct{}

func (ere ExitRequestError) Error() string {
	return ""
}

func exitCommand(t *Term, ctx callContext, args string) error {
	if args == "-c" {
		if !t.client.IsMulticlient() {
			return errors.New("not connected to an --accept-multiclient server")
		}
		t.quitContinue = true
	}
	return ExitRequestError{}
}

func getBreakpointByIDOrName(t *Term, arg string) (*api.Breakpoint, error) {
	if id, err := strconv.Atoi(arg); err == nil {
		return t.client.GetBreakpoint(id)
	}
	return t.client.GetBreakpointByName(arg)
}

func (c *Commands) onCmd(t *Term, ctx callContext, argstr string) error {
	args := split2PartsBySpace(argstr)

	if len(args) < 2 {
		return errors.New("not enough arguments")
	}

	bp, err := getBreakpointByIDOrName(t, args[0])
	if err != nil {
		return err
	}

	ctx.Prefix = onPrefix
	ctx.Breakpoint = bp
	err = c.CallWithContext(args[1], t, ctx)
	if err != nil {
		return err
	}
	return t.client.AmendBreakpoint(ctx.Breakpoint)
}

func conditionCmd(t *Term, ctx callContext, argstr string) error {
	args := split2PartsBySpace(argstr)

	if len(args) < 2 {
		return fmt.Errorf("not enough arguments")
	}

	bp, err := getBreakpointByIDOrName(t, args[0])
	if err != nil {
		return err
	}
	bp.Cond = args[1]

	return t.client.AmendBreakpoint(bp)
}

func (c *Commands) executeFile(t *Term, name string) error {
	fh, err := os.Open(name)
	if err != nil {
		return err
	}
	defer fh.Close()

	scanner := bufio.NewScanner(fh)
	lineno := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		lineno++

		if line == "" || line[0] == '#' {
			continue
		}

		if err := c.Call(line, t); err != nil {
			if _, isExitRequest := err.(ExitRequestError); isExitRequest {
				return err
			}
			fmt.Printf("%s:%d: %v\n", name, lineno, err)
		}
	}

	return scanner.Err()
}

func (c *Commands) rewind(t *Term, ctx callContext, args string) error {
	c.frame = 0
	stateChan := t.client.Rewind()
	var state *api.DebuggerState
	for state = range stateChan {
		if state.Err != nil {
			return state.Err
		}
		printcontext(t, state)
	}
	printfile(t, state.CurrentThread.File, state.CurrentThread.Line, true)
	return nil
}

func checkpoint(t *Term, ctx callContext, args string) error {
	if args == "" {
		state, err := t.client.GetState()
		if err != nil {
			return err
		}
		var loc api.Location = api.Location{PC: state.CurrentThread.PC, File: state.CurrentThread.File, Line: state.CurrentThread.Line, Function: state.CurrentThread.Function}
		if state.SelectedGoroutine != nil {
			loc = state.SelectedGoroutine.CurrentLoc
		}
		args = fmt.Sprintf("%s() %s:%d (%#x)", loc.Function.Name(), loc.File, loc.Line, loc.PC)
	}

	cpid, err := t.client.Checkpoint(args)
	if err != nil {
		return err
	}

	fmt.Printf("Checkpoint c%d created.\n", cpid)
	return nil
}

func checkpoints(t *Term, ctx callContext, args string) error {
	cps, err := t.client.ListCheckpoints()
	if err != nil {
		return err
	}
	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 4, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tWhen\tNote")
	for _, cp := range cps {
		fmt.Fprintf(w, "c%d\t%s\t%s\n", cp.ID, cp.When, cp.Where)
	}
	w.Flush()
	return nil
}

func clearCheckpoint(t *Term, ctx callContext, args string) error {
	if len(args) == 0 {
		return errors.New("not enough arguments to clear-checkpoint")
	}
	if args[0] != 'c' {
		return errors.New("clear-checkpoint argument must be a checkpoint ID")
	}
	id, err := strconv.Atoi(args[1:])
	if err != nil {
		return errors.New("clear-checkpoint argument must be a checkpoint ID")
	}
	return t.client.ClearCheckpoint(id)
}

func display(t *Term, ctx callContext, args string) error {
	const (
		addOption = "-a "
		delOption = "-d "
	)
	switch {
	case args == "":
		t.printDisplays()

	case strings.HasPrefix(args, addOption):
		args = strings.TrimSpace(args[len(addOption):])
		if args == "" {
			return fmt.Errorf("not enough arguments")
		}
		t.addDisplay(args)
		t.printDisplay(len(t.displays) - 1)

	case strings.HasPrefix(args, delOption):
		args = strings.TrimSpace(args[len(delOption):])
		n, err := strconv.Atoi(args)
		if err != nil {
			return fmt.Errorf("%q is not a number", args)
		}
		return t.removeDisplay(n)

	default:
		return fmt.Errorf("wrong arguments")
	}
	return nil
}

func dump(t *Term, ctx callContext, args string) error {
	if args == "" {
		return fmt.Errorf("not enough arguments")
	}
	dumpState, err := t.client.CoreDumpStart(args)
	if err != nil {
		return err
	}
	for {
		if dumpState.ThreadsDone != dumpState.ThreadsTotal {
			fmt.Printf("\rDumping threads %d / %d...", dumpState.ThreadsDone, dumpState.ThreadsTotal)
		} else {
			fmt.Printf("\rDumping memory %d / %d...", dumpState.MemDone, dumpState.MemTotal)
		}
		if !dumpState.Dumping {
			break
		}
		dumpState = t.client.CoreDumpWait(1000)
	}
	fmt.Printf("\n")
	if dumpState.Err != "" {
		fmt.Printf("error dumping: %s\n", dumpState.Err)
	} else if !dumpState.AllDone {
		fmt.Printf("canceled\n")
	} else if dumpState.MemDone != dumpState.MemTotal {
		fmt.Printf("Core dump could be incomplete\n")
	}
	return nil
}

func formatBreakpointName(bp *api.Breakpoint, upcase bool) string {
	thing := "breakpoint"
	if bp.Tracepoint {
		thing = "tracepoint"
	}
	if upcase {
		thing = strings.Title(thing)
	}
	id := bp.Name
	if id == "" {
		id = strconv.Itoa(bp.ID)
	}
	return fmt.Sprintf("%s %s", thing, id)
}

func (t *Term) formatBreakpointLocation(bp *api.Breakpoint) string {
	var out bytes.Buffer
	if len(bp.Addrs) > 0 {
		for i, addr := range bp.Addrs {
			if i == 0 {
				fmt.Fprintf(&out, "%#x", addr)
			} else {
				fmt.Fprintf(&out, ",%#x", addr)
			}
		}
	} else {
		// In case we are connecting to an older version of delve that does not return the Addrs field.
		fmt.Fprintf(&out, "%#x", bp.Addr)
	}
	fmt.Fprintf(&out, " for ")
	p := t.formatPath(bp.File)
	if bp.FunctionName != "" {
		fmt.Fprintf(&out, "%s() ", bp.FunctionName)
	}
	fmt.Fprintf(&out, "%s:%d", p, bp.Line)
	return out.String()
}
