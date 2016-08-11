// Package terminal implements functions for responding to user
// input and dispatching to appropriate backend commands.
package terminal

import (
	"bufio"
	"errors"
	"fmt"
	"go/parser"
	"go/scanner"
	"io"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
	"github.com/derekparker/delve/service/debugger"
)

type cmdPrefix int

const (
	noPrefix    = cmdPrefix(0)
	scopePrefix = cmdPrefix(1 << iota)
	onPrefix
)

type callContext struct {
	Prefix     cmdPrefix
	Scope      api.EvalScope
	Breakpoint *api.Breakpoint
}

type cmdfunc func(t *Term, ctx callContext, args string) error

type command struct {
	aliases         []string
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
	cmds    []command
	lastCmd cmdfunc
	client  service.Client
}

var (
	LongLoadConfig  = api.LoadConfig{true, 1, 64, 64, -1}
	ShortLoadConfig = api.LoadConfig{false, 0, 64, 0, 3}
)

type ByFirstAlias []command

func (a ByFirstAlias) Len() int           { return len(a) }
func (a ByFirstAlias) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByFirstAlias) Less(i, j int) bool { return a[i].aliases[0] < a[j].aliases[0] }

// DebugCommands returns a Commands struct with default commands defined.
func DebugCommands(client service.Client) *Commands {
	c := &Commands{client: client}

	c.cmds = []command{
		{aliases: []string{"help", "h"}, cmdFn: c.help, helpMsg: `Prints the help message.

	help [command]
	
Type "help" followed by the name of a command for more information about it.`},
		{aliases: []string{"break", "b"}, cmdFn: breakpoint, helpMsg: `Sets a breakpoint.

	break [name] <linespec>

See $GOPATH/src/github.com/derekparker/delve/Documentation/cli/locspec.md for the syntax of linespec.

See also: "help on", "help cond" and "help clear"`},
		{aliases: []string{"trace", "t"}, cmdFn: tracepoint, helpMsg: `Set tracepoint.

	trace [name] <linespec>
	
A tracepoint is a breakpoint that does not stop the execution of the program, instead when the tracepoint is hit a notification is displayed. See $GOPATH/src/github.com/derekparker/delve/Documentation/cli/locspec.md for the syntax of linespec.

See also: "help on", "help cond" and "help clear"`},
		{aliases: []string{"restart", "r"}, cmdFn: restart, helpMsg: "Restart process."},
		{aliases: []string{"continue", "c"}, cmdFn: cont, helpMsg: "Run until breakpoint or program termination."},
		{aliases: []string{"step", "s"}, allowedPrefixes: scopePrefix, cmdFn: step, helpMsg: "Single step through program."},
		{aliases: []string{"step-instruction", "si"}, allowedPrefixes: scopePrefix, cmdFn: stepInstruction, helpMsg: "Single step a single cpu instruction."},
		{aliases: []string{"next", "n"}, allowedPrefixes: scopePrefix, cmdFn: next, helpMsg: "Step over to next source line."},
		{aliases: []string{"threads"}, cmdFn: threads, helpMsg: "Print out info for every traced thread."},
		{aliases: []string{"thread", "tr"}, cmdFn: thread, helpMsg: `Switch to the specified thread.

	thread <id>`},
		{aliases: []string{"clear"}, cmdFn: clear, helpMsg: `Deletes breakpoint.

	clear <breakpoint name or id>`},
		{aliases: []string{"clearall"}, cmdFn: clearAll, helpMsg: `Deletes multiple breakpoints.

	clearall [<linespec>]
	
If called with the linespec argument it will delete all the breakpoints matching the linespec. If linespec is omitted all breakpoints are deleted.`},
		{aliases: []string{"goroutines"}, cmdFn: goroutines, helpMsg: `List program goroutines.

	goroutines [-u (default: user location)|-r (runtime location)|-g (go statement location)]

Print out info for every goroutine. The flag controls what information is shown along with each goroutine:

	-u	displays location of topmost stackframe in user code
	-r	displays location of topmost stackframe (including frames inside private runtime functions)
	-g	displays location of go instruction that created the goroutine
	
If no flag is specified the default is -u.`},
		{aliases: []string{"goroutine"}, allowedPrefixes: onPrefix | scopePrefix, cmdFn: c.goroutine, helpMsg: `Shows or changes current goroutine

	goroutine
	goroutine <id>
	goroutine <id> <command>

Called without arguments it will show information about the current goroutine.
Called with a single argument it will switch to the specified goroutine.
Called with more arguments it will execute a command on the specified goroutine.`},
		{aliases: []string{"breakpoints", "bp"}, cmdFn: breakpoints, helpMsg: "Print out info for active breakpoints."},
		{aliases: []string{"print", "p"}, allowedPrefixes: onPrefix | scopePrefix, cmdFn: printVar, helpMsg: `Evaluate an expression.

	[goroutine <n>] [frame <m>] print <expression>

See $GOPATH/src/github.com/derekparker/delve/Documentation/cli/expr.md for a description of supported expressions.`},
		{aliases: []string{"set"}, allowedPrefixes: scopePrefix, cmdFn: setVar, helpMsg: `Changes the value of a variable.

	[goroutine <n>] [frame <m>] set <variable> = <value>

See $GOPATH/src/github.com/derekparker/delve/Documentation/cli/expr.md for a description of supported expressions. Only numerical variables and pointers can be changed.`},
		{aliases: []string{"sources"}, cmdFn: sources, helpMsg: `Print list of source files.

	sources [<regex>]

If regex is specified only the source files matching it will be returned.`},
		{aliases: []string{"funcs"}, cmdFn: funcs, helpMsg: `Print list of functions.

	funcs [<regex>]

If regex is specified only the functions matching it will be returned.`},
		{aliases: []string{"types"}, cmdFn: types, helpMsg: `Print list of types

	types [<regex>]

If regex is specified only the functions matching it will be returned.`},
		{aliases: []string{"args"}, allowedPrefixes: scopePrefix | onPrefix, cmdFn: args, helpMsg: `Print function arguments.

	[goroutine <n>] [frame <m>] args [-v] [<regex>]

If regex is specified only function arguments with a name matching it will be returned. If -v is specified more information about each function argument will be shown.`},
		{aliases: []string{"locals"}, allowedPrefixes: scopePrefix | onPrefix, cmdFn: locals, helpMsg: `Print local variables.

	[goroutine <n>] [frame <m>] locals [-v] [<regex>]

If regex is specified only local variables with a name matching it will be returned. If -v is specified more information about each local variable will be shown.`},
		{aliases: []string{"vars"}, cmdFn: vars, helpMsg: `Print package variables.

	vars [-v] [<regex>]

If regex is specified only package variables with a name matching it will be returned. If -v is specified more information about each package variable will be shown.`},
		{aliases: []string{"regs"}, cmdFn: regs, helpMsg: "Print contents of CPU registers."},
		{aliases: []string{"exit", "quit", "q"}, cmdFn: exitCommand, helpMsg: "Exit the debugger."},
		{aliases: []string{"list", "ls"}, allowedPrefixes: scopePrefix, cmdFn: listCommand, helpMsg: `Show source code.

	[goroutine <n>] [frame <m>] list [<linespec>]

Show source around current point or provided linespec.`},
		{aliases: []string{"stack", "bt"}, allowedPrefixes: scopePrefix | onPrefix, cmdFn: stackCommand, helpMsg: `Print stack trace.

	[goroutine <n>] [frame <m>] stack [<depth>] [-full]

If -full is specified every stackframe will be decorated by the value of its local variables and function arguments.`},
		{aliases: []string{"frame"}, allowedPrefixes: scopePrefix, cmdFn: c.frame, helpMsg: `Executes command on a different frame.

	frame <frame index> <command>.`},
		{aliases: []string{"source"}, cmdFn: c.sourceCommand, helpMsg: `Executes a file containing a list of delve commands

	source <path>`},
		{aliases: []string{"disassemble", "disass"}, allowedPrefixes: scopePrefix, cmdFn: disassCommand, helpMsg: `Disassembler.

	[goroutine <n>] [frame <m>] disassemble [-a <start> <end>] [-l <locspec>]

If no argument is specified the function being executed in the selected stack frame will be executed.
	
	-a <start> <end>	disassembles the specified address range
	-l <locspec>		disassembles the specified function`},
		{aliases: []string{"on"}, cmdFn: c.onCmd, helpMsg: `Executes a command when a breakpoint is hit.

	on <breakpoint name or id> <command>.
	
Supported commands: print, stack and goroutine)`},
		{aliases: []string{"condition", "cond"}, cmdFn: conditionCmd, helpMsg: `Set breakpoint condition.

	condition <breakpoint name or id> <boolean expression>.
	
Specifies that the breakpoint or tracepoint should break only if the boolean expression is true.`},
		{aliases: []string{"signal", "sig"}, cmdFn: signalCmd, helpMsg: `Run then signal the program.

	signal [NUMBER]

Without an argument, this doesn't run the program; instead, it lists all available signals.`},
	}

	sort.Sort(ByFirstAlias(c.cmds))
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
		if c.lastCmd != nil {
			return c.lastCmd
		}
		return nullCommand
	}

	for _, v := range c.cmds {
		if v.match(cmdstr) {
			if prefix != noPrefix && v.allowedPrefixes&prefix == 0 {
				continue
			}
			c.lastCmd = v.cmdFn
			return v.cmdFn
		}
	}

	return noCmdAvailable
}

func (c *Commands) CallWithContext(cmdstr, args string, t *Term, ctx callContext) error {
	return c.Find(cmdstr, ctx.Prefix)(t, ctx, args)
}

func (c *Commands) Call(cmdstr, args string, t *Term) error {
	ctx := callContext{Prefix: noPrefix, Scope: api.EvalScope{GoroutineID: -1, Frame: 0}}
	return c.CallWithContext(cmdstr, args, t, ctx)
}

// Merge takes aliases defined in the config struct and merges them with the default aliases.
func (c *Commands) Merge(allAliases map[string][]string) {
	for i := range c.cmds {
		if aliases, ok := allAliases[c.cmds[i].aliases[0]]; ok {
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
	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 0, '-', 0)
	for _, cmd := range c.cmds {
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
				prefix, th.ID, th.PC, ShortenFilePath(th.File),
				th.Line, th.Function.Name)
		} else {
			fmt.Printf("%sThread %s\n", prefix, formatThread(th))
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

func goroutines(t *Term, ctx callContext, argstr string) error {
	args := strings.Split(argstr, " ")
	var fgl = fglUserCurrent

	switch len(args) {
	case 0:
		// nothing to do
	case 1:
		switch args[0] {
		case "-u":
			fgl = fglUserCurrent
		case "-r":
			fgl = fglRuntimeCurrent
		case "-g":
			fgl = fglGo
		case "":
			// nothing to do
		default:
			return fmt.Errorf("wrong argument: '%s'", args[0])
		}
	default:
		return fmt.Errorf("too many arguments")
	}
	state, err := t.client.GetState()
	if err != nil {
		return err
	}
	gs, err := t.client.ListGoroutines()
	if err != nil {
		return err
	}
	sort.Sort(byGoroutineID(gs))
	fmt.Printf("[%d goroutines]\n", len(gs))
	for _, g := range gs {
		prefix := "  "
		if state.SelectedGoroutine != nil && g.ID == state.SelectedGoroutine.ID {
			prefix = "* "
		}
		fmt.Printf("%sGoroutine %s\n", prefix, formatGoroutine(g, fgl))
	}
	return nil
}

func (c *Commands) goroutine(t *Term, ctx callContext, argstr string) error {
	args := strings.SplitN(argstr, " ", 3)

	if ctx.Prefix == onPrefix {
		if len(args) != 1 || args[0] != "" {
			return errors.New("too many arguments to goroutine")
		}
		ctx.Breakpoint.Goroutine = true
		return nil
	}

	switch len(args) {
	case 1:
		if ctx.Prefix == scopePrefix {
			return errors.New("no command passed to goroutine")
		}
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

		fmt.Printf("Switched from %d to %d (thread %d)\n", oldState.SelectedGoroutine.ID, gid, newState.CurrentThread.ID)
		return nil
	case 2:
		args = append(args, "")
	}

	var err error
	ctx.Prefix = scopePrefix
	ctx.Scope.GoroutineID, err = strconv.Atoi(args[0])
	if err != nil {
		return err
	}
	return c.CallWithContext(args[1], args[2], t, ctx)
}

func (c *Commands) frame(t *Term, ctx callContext, args string) error {
	v := strings.SplitN(args, " ", 3)

	switch len(v) {
	case 0, 1:
		return errors.New("not enough arguments")
	case 2:
		v = append(v, "")
	}

	var err error
	ctx.Prefix = scopePrefix
	ctx.Scope.Frame, err = strconv.Atoi(v[0])
	if err != nil {
		return err
	}
	return c.CallWithContext(v[1], v[2], t, ctx)
}

func printscope(t *Term) error {
	state, err := t.client.GetState()
	if err != nil {
		return err
	}

	fmt.Printf("Thread %s\n", formatThread(state.CurrentThread))
	if state.SelectedGoroutine != nil {
		writeGoroutineLong(os.Stdout, state.SelectedGoroutine, "")
	}
	return nil
}

func formatThread(th *api.Thread) string {
	if th == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%d at %s:%d", th.ID, ShortenFilePath(th.File), th.Line)
}

type formatGoroutineLoc int

const (
	fglRuntimeCurrent = formatGoroutineLoc(iota)
	fglUserCurrent
	fglGo
)

func formatLocation(loc api.Location) string {
	fname := ""
	if loc.Function != nil {
		fname = loc.Function.Name
	}
	return fmt.Sprintf("%s:%d %s (%#v)", ShortenFilePath(loc.File), loc.Line, fname, loc.PC)
}

func formatGoroutine(g *api.Goroutine, fgl formatGoroutineLoc) string {
	if g == nil {
		return "<nil>"
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
	}
	thread := ""
	if g.ThreadID != 0 {
		thread = fmt.Sprintf(" (thread %d)", g.ThreadID)
	}
	return fmt.Sprintf("%d - %s: %s%s", g.ID, locname, formatLocation(loc), thread)
}

func writeGoroutineLong(w io.Writer, g *api.Goroutine, prefix string) {
	fmt.Fprintf(w, "%sGoroutine %d:\n%s\tRuntime: %s\n%s\tUser: %s\n%s\tGo: %s\n",
		prefix, g.ID,
		prefix, formatLocation(g.CurrentLoc),
		prefix, formatLocation(g.UserCurrentLoc),
		prefix, formatLocation(g.GoStatementLoc))
}

func restart(t *Term, ctx callContext, args string) error {
	if err := t.client.Restart(); err != nil {
		return err
	}
	fmt.Println("Process restarted with PID", t.client.ProcessPid())
	return nil
}

func cont(t *Term, ctx callContext, args string) error {
	return sigcont(t, ctx, 0)
}

func sigcont(t *Term, ctx callContext, sig int) error {
	stateChan := t.client.Continue(sig)
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

func continueUntilCompleteNext(t *Term, state *api.DebuggerState, op string) error {
	if !state.NextInProgress {
		printfile(t, state.CurrentThread.File, state.CurrentThread.Line, true)
		return nil
	}
	for {
		stateChan := t.client.Continue(0)
		var state *api.DebuggerState
		for state = range stateChan {
			if state.Err != nil {
				return state.Err
			}
			printcontext(t, state)
		}
		if !state.NextInProgress {
			printfile(t, state.CurrentThread.File, state.CurrentThread.Line, true)
			return nil
		}
		fmt.Printf("\tbreakpoint hit during %s, continuing...\n", op)
	}
}

func scopePrefixSwitch(t *Term, ctx callContext) error {
	if ctx.Prefix != scopePrefix {
		return nil
	}
	if ctx.Scope.Frame != 0 {
		return errors.New("frame prefix not accepted")
	}
	if ctx.Scope.GoroutineID > 0 {
		_, err := t.client.SwitchGoroutine(ctx.Scope.GoroutineID)
		if err != nil {
			return err
		}
	}
	return nil
}

func step(t *Term, ctx callContext, args string) error {
	if err := scopePrefixSwitch(t, ctx); err != nil {
		return err
	}
	state, err := t.client.Step()
	if err != nil {
		return err
	}
	printcontext(t, state)
	return continueUntilCompleteNext(t, state, "step")
}

func stepInstruction(t *Term, ctx callContext, args string) error {
	if err := scopePrefixSwitch(t, ctx); err != nil {
		return err
	}
	state, err := t.client.StepInstruction()
	if err != nil {
		return err
	}
	printcontext(t, state)
	printfile(t, state.CurrentThread.File, state.CurrentThread.Line, true)
	return nil
}

func next(t *Term, ctx callContext, args string) error {
	if err := scopePrefixSwitch(t, ctx); err != nil {
		return err
	}
	state, err := t.client.Next()
	if err != nil {
		return err
	}
	printcontext(t, state)
	return continueUntilCompleteNext(t, state, "next")
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
	fmt.Printf("%s cleared at %s\n", formatBreakpointName(bp, true), formatBreakpointLocation(bp))
	return nil
}

func clearAll(t *Term, ctx callContext, args string) error {
	breakPoints, err := t.client.ListBreakpoints()
	if err != nil {
		return err
	}

	var locPCs map[uint64]struct{}
	if args != "" {
		locs, err := t.client.FindLocation(api.EvalScope{GoroutineID: -1, Frame: 0}, args)
		if err != nil {
			return err
		}
		locPCs = make(map[uint64]struct{})
		for _, loc := range locs {
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
			fmt.Printf("Couldn't delete %s at %s: %s\n", formatBreakpointName(bp, false), formatBreakpointLocation(bp), err)
		}
		fmt.Printf("%s cleared at %s\n", formatBreakpointName(bp, true), formatBreakpointLocation(bp))
	}
	return nil
}

// ByID sorts breakpoints by ID.
type ByID []*api.Breakpoint

func (a ByID) Len() int           { return len(a) }
func (a ByID) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByID) Less(i, j int) bool { return a[i].ID < a[j].ID }

func breakpoints(t *Term, ctx callContext, args string) error {
	breakPoints, err := t.client.ListBreakpoints()
	if err != nil {
		return err
	}
	sort.Sort(ByID(breakPoints))
	for _, bp := range breakPoints {
		fmt.Printf("%s at %v (%d)\n", formatBreakpointName(bp, true), formatBreakpointLocation(bp), bp.TotalHitCount)

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
			if *(bp.LoadArgs) == LongLoadConfig {
				attrs = append(attrs, "\targs -v")
			} else {
				attrs = append(attrs, "\targs")
			}
		}
		if bp.LoadLocals != nil {
			if *(bp.LoadLocals) == LongLoadConfig {
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

func setBreakpoint(t *Term, tracepoint bool, argstr string) error {
	args := strings.SplitN(argstr, " ", 2)

	requestedBp := &api.Breakpoint{}
	locspec := ""
	switch len(args) {
	case 1:
		locspec = argstr
	case 2:
		if api.ValidBreakpointName(args[0]) == nil {
			requestedBp.Name = args[0]
			locspec = args[1]
		} else {
			locspec = argstr
		}
	default:
		return fmt.Errorf("address required")
	}

	requestedBp.Tracepoint = tracepoint
	locs, err := t.client.FindLocation(api.EvalScope{GoroutineID: -1, Frame: 0}, locspec)
	if err != nil {
		if requestedBp.Name == "" {
			return err
		}
		requestedBp.Name = ""
		locspec = argstr
		var err2 error
		locs, err2 = t.client.FindLocation(api.EvalScope{GoroutineID: -1, Frame: 0}, locspec)
		if err2 != nil {
			return err
		}
	}
	for _, loc := range locs {
		requestedBp.Addr = loc.PC

		bp, err := t.client.CreateBreakpoint(requestedBp)
		if err != nil {
			return err
		}

		fmt.Printf("%s set at %s\n", formatBreakpointName(bp, true), formatBreakpointLocation(bp))
	}
	return nil
}

func breakpoint(t *Term, ctx callContext, args string) error {
	return setBreakpoint(t, false, args)
}

func tracepoint(t *Term, ctx callContext, args string) error {
	return setBreakpoint(t, true, args)
}

func printVar(t *Term, ctx callContext, args string) error {
	if len(args) == 0 {
		return fmt.Errorf("not enough arguments")
	}
	if ctx.Prefix == onPrefix {
		ctx.Breakpoint.Variables = append(ctx.Breakpoint.Variables, args)
		return nil
	}
	val, err := t.client.EvalVariable(ctx.Scope, args, LongLoadConfig)
	if err != nil {
		return err
	}

	fmt.Println(val.MultilineString(""))
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
			if cfg == ShortLoadConfig {
				fmt.Printf("%s = %s\n", v.Name, v.SinglelineString())
			} else {
				fmt.Printf("%s = %s\n", v.Name, v.MultilineString(""))
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

func parseVarArguments(args string) (filter string, cfg api.LoadConfig) {
	if v := strings.SplitN(args, " ", 2); len(v) >= 1 && v[0] == "-v" {
		if len(v) == 2 {
			return v[1], LongLoadConfig
		} else {
			return "", LongLoadConfig
		}
	}
	return args, ShortLoadConfig
}

func args(t *Term, ctx callContext, args string) error {
	filter, cfg := parseVarArguments(args)
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
	filter, cfg := parseVarArguments(args)
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
	filter, cfg := parseVarArguments(args)
	vars, err := t.client.ListPackageVariables(filter, cfg)
	if err != nil {
		return err
	}
	return printFilteredVariables("vars", vars, filter, cfg)
}

func regs(t *Term, ctx callContext, args string) error {
	regs, err := t.client.ListRegisters()
	if err != nil {
		return err
	}
	fmt.Println(regs)
	return nil
}

func stackCommand(t *Term, ctx callContext, args string) error {
	depth, full, err := parseStackArgs(args)
	if err != nil {
		return err
	}
	if ctx.Prefix == onPrefix {
		ctx.Breakpoint.Stacktrace = depth
		return nil
	}
	var cfg *api.LoadConfig
	if full {
		cfg = &ShortLoadConfig
	}
	stack, err := t.client.Stacktrace(ctx.Scope.GoroutineID, depth, cfg)
	if err != nil {
		return err
	}
	printStack(stack, "")
	return nil
}

func parseStackArgs(argstr string) (int, bool, error) {
	var (
		depth = 10
		full  = false
	)
	if argstr != "" {
		args := strings.Split(argstr, " ")
		for i := range args {
			if args[i] == "-full" {
				full = true
			} else {
				n, err := strconv.Atoi(args[i])
				if err != nil {
					return 0, false, fmt.Errorf("depth must be a number")
				}
				depth = n
			}
		}
	}
	return depth, full, nil
}

func listCommand(t *Term, ctx callContext, args string) error {
	if ctx.Prefix == scopePrefix {
		locs, err := t.client.Stacktrace(ctx.Scope.GoroutineID, ctx.Scope.Frame, nil)
		if err != nil {
			return err
		}
		if ctx.Scope.Frame >= len(locs) {
			return fmt.Errorf("Frame %d does not exist in goroutine %d", ctx.Scope.Frame, ctx.Scope.GoroutineID)
		}
		loc := locs[ctx.Scope.Frame]
		return printfile(t, loc.File, loc.Line, true)
	}

	if len(args) == 0 {
		state, err := t.client.GetState()
		if err != nil {
			return err
		}
		printcontext(t, state)
		printfile(t, state.CurrentThread.File, state.CurrentThread.Line, true)
		return nil
	}

	locs, err := t.client.FindLocation(api.EvalScope{GoroutineID: -1, Frame: 0}, args)
	if err != nil {
		return err
	}
	if len(locs) > 1 {
		return debugger.AmbiguousLocationError{Location: args, CandidatesLocation: locs}
	}
	printfile(t, locs[0].File, locs[0].Line, false)
	return nil
}

func (c *Commands) sourceCommand(t *Term, ctx callContext, args string) error {
	if len(args) == 0 {
		return fmt.Errorf("wrong number of arguments: source <filename>")
	}

	return c.executeFile(t, args)
}

var disasmUsageError = errors.New("wrong number of arguments: disassemble [-a <start> <end>] [-l <locspec>]")

func disassCommand(t *Term, ctx callContext, args string) error {
	var cmd, rest string

	if args != "" {
		argv := strings.SplitN(args, " ", 2)
		if len(argv) != 2 {
			return disasmUsageError
		}
		cmd = argv[0]
		rest = argv[1]
	}

	var disasm api.AsmInstructions
	var disasmErr error

	switch cmd {
	case "":
		locs, err := t.client.FindLocation(ctx.Scope, "+0")
		if err != nil {
			return err
		}
		disasm, disasmErr = t.client.DisassemblePC(ctx.Scope, locs[0].PC, api.IntelFlavour)
	case "-a":
		v := strings.SplitN(rest, " ", 2)
		if len(v) != 2 {
			return disasmUsageError
		}
		startpc, err := strconv.ParseInt(v[0], 0, 64)
		if err != nil {
			return fmt.Errorf("wrong argument: %s is not a number", v[0])
		}
		endpc, err := strconv.ParseInt(v[1], 0, 64)
		if err != nil {
			return fmt.Errorf("wrong argument: %s is not a number", v[1])
		}
		disasm, disasmErr = t.client.DisassembleRange(ctx.Scope, uint64(startpc), uint64(endpc), api.IntelFlavour)
	case "-l":
		locs, err := t.client.FindLocation(ctx.Scope, rest)
		if err != nil {
			return err
		}
		if len(locs) != 1 {
			return errors.New("expression specifies multiple locations")
		}
		disasm, disasmErr = t.client.DisassemblePC(ctx.Scope, locs[0].PC, api.IntelFlavour)
	default:
		return disasmUsageError
	}

	if disasmErr != nil {
		return disasmErr
	}

	fmt.Printf("printing\n")
	DisasmPrint(disasm, os.Stdout)

	return nil
}

func digits(n int) int {
	if n <= 0 {
		return 1
	}
	return int(math.Floor(math.Log10(float64(n)))) + 1
}

func printStack(stack []api.Stackframe, ind string) {
	if len(stack) == 0 {
		return
	}
	d := digits(len(stack) - 1)
	fmtstr := "%s%" + strconv.Itoa(d) + "d  0x%016x in %s\n"
	s := ind + strings.Repeat(" ", d+2+len(ind))

	for i := range stack {
		name := "(nil)"
		if stack[i].Function != nil {
			name = stack[i].Function.Name
		}
		fmt.Printf(fmtstr, ind, i, stack[i].PC, name)
		fmt.Printf("%sat %s:%d\n", s, ShortenFilePath(stack[i].File), stack[i].Line)

		for j := range stack[i].Arguments {
			fmt.Printf("%s    %s = %s\n", s, stack[i].Arguments[j].Name, stack[i].Arguments[j].SinglelineString())
		}
		for j := range stack[i].Locals {
			fmt.Printf("%s    %s = %s\n", s, stack[i].Locals[j].Name, stack[i].Locals[j].SinglelineString())
		}
	}
}

func printcontext(t *Term, state *api.DebuggerState) error {
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
		return nil
	}
	if len(state.CurrentThread.File) == 0 {
		fmt.Printf("Stopped at: 0x%x\n", state.CurrentThread.PC)
		t.Println("=>", "no source available")
		return nil
	}

	printcontextThread(t, state.CurrentThread)

	return nil
}

func printcontextThread(t *Term, th *api.Thread) {
	fn := th.Function

	if th.Breakpoint == nil {
		fmt.Printf("> %s() %s:%d (PC: %#v)\n", fn.Name, ShortenFilePath(th.File), th.Line, th.PC)
		return
	}

	args := ""
	if th.BreakpointInfo != nil && th.Breakpoint.LoadArgs != nil && *th.Breakpoint.LoadArgs == ShortLoadConfig {
		var arg []string
		for _, ar := range th.BreakpointInfo.Arguments {
			arg = append(arg, ar.SinglelineString())
		}
		args = strings.Join(arg, ", ")
	}

	bpname := ""
	if th.Breakpoint.Name != "" {
		bpname = fmt.Sprintf("[%s] ", th.Breakpoint.Name)
	}

	if hitCount, ok := th.Breakpoint.HitCount[strconv.Itoa(th.GoroutineID)]; ok {
		fmt.Printf("> %s%s(%s) %s:%d (hits goroutine(%d):%d total:%d) (PC: %#v)\n",
			bpname,
			fn.Name,
			args,
			ShortenFilePath(th.File),
			th.Line,
			th.GoroutineID,
			hitCount,
			th.Breakpoint.TotalHitCount,
			th.PC)
	} else {
		fmt.Printf("> %s%s(%s) %s:%d (hits total:%d) (PC: %#v)\n",
			bpname,
			fn.Name,
			args,
			ShortenFilePath(th.File),
			th.Line,
			th.Breakpoint.TotalHitCount,
			th.PC)
	}

	if th.BreakpointInfo != nil {
		bp := th.Breakpoint
		bpi := th.BreakpointInfo

		if bpi.Goroutine != nil {
			writeGoroutineLong(os.Stdout, bpi.Goroutine, "\t")
		}

		for _, v := range bpi.Variables {
			fmt.Printf("\t%s: %s\n", v.Name, v.MultilineString("\t"))
		}

		for _, v := range bpi.Locals {
			if *bp.LoadLocals == LongLoadConfig {
				fmt.Printf("\t%s: %s\n", v.Name, v.MultilineString("\t"))
			} else {
				fmt.Printf("\t%s: %s\n", v.Name, v.SinglelineString())
			}
		}

		if bp.LoadArgs != nil && *bp.LoadArgs == LongLoadConfig {
			for _, v := range bpi.Arguments {
				fmt.Printf("\t%s: %s\n", v.Name, v.MultilineString("\t"))
			}
		}

		if bpi.Stacktrace != nil {
			fmt.Printf("\tStack:\n")
			printStack(bpi.Stacktrace, "\t\t")
		}
	}
}

func printfile(t *Term, filename string, line int, showArrow bool) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	buf := bufio.NewScanner(file)
	l := line
	for i := 1; i < l-5; i++ {
		if !buf.Scan() {
			return nil
		}
	}

	s := l - 5
	if s < 1 {
		s = 1
	}

	for i := s; i <= l+5; i++ {
		if !buf.Scan() {
			return nil
		}

		var prefix string
		if showArrow {
			prefix = "  "
			if i == l {
				prefix = "=>"
			}
		}

		prefix = fmt.Sprintf("%s%4d:\t", prefix, i)
		t.Println(prefix, buf.Text())
	}
	return nil
}

// ExitRequestError is returned when the user
// exits Delve.
type ExitRequestError struct{}

func (ere ExitRequestError) Error() string {
	return ""
}

func exitCommand(t *Term, ctx callContext, args string) error {
	return ExitRequestError{}
}

func getBreakpointByIDOrName(t *Term, arg string) (*api.Breakpoint, error) {
	if id, err := strconv.Atoi(arg); err == nil {
		return t.client.GetBreakpoint(id)
	}
	return t.client.GetBreakpointByName(arg)
}

func (c *Commands) onCmd(t *Term, ctx callContext, argstr string) error {
	args := strings.SplitN(argstr, " ", 3)

	if len(args) < 2 {
		return errors.New("not enough arguments")
	}

	if len(args) < 3 {
		args = append(args, "")
	}

	bp, err := getBreakpointByIDOrName(t, args[0])
	if err != nil {
		return err
	}

	ctx.Prefix = onPrefix
	ctx.Breakpoint = bp
	err = c.CallWithContext(args[1], args[2], t, ctx)
	if err != nil {
		return err
	}
	return t.client.AmendBreakpoint(ctx.Breakpoint)
}

func conditionCmd(t *Term, ctx callContext, argstr string) error {
	args := strings.SplitN(argstr, " ", 2)

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

// ShortenFilePath take a full file path and attempts to shorten
// it by replacing the current directory to './'.
func ShortenFilePath(fullPath string) string {
	workingDir, _ := os.Getwd()
	return strings.Replace(fullPath, workingDir, ".", 1)
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

		cmdstr, args := parseCommand(line)

		if err := c.Call(cmdstr, args, t); err != nil {
			fmt.Printf("%s:%d: %v\n", name, lineno, err)
		}
	}

	return scanner.Err()
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

func formatBreakpointLocation(bp *api.Breakpoint) string {
	p := ShortenFilePath(bp.File)
	if bp.FunctionName != "" {
		return fmt.Sprintf("%#v for %s() %s:%d", bp.Addr, bp.FunctionName, p, bp.Line)
	}
	return fmt.Sprintf("%#v for %s:%d", bp.Addr, p, bp.Line)
}
