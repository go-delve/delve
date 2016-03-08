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
type filteringFunc func(t *Term, ctx callContext, args string) ([]string, error)

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

// DebugCommands returns a Commands struct with default commands defined.
func DebugCommands(client service.Client) *Commands {
	c := &Commands{client: client}

	c.cmds = []command{
		{aliases: []string{"help", "h"}, cmdFn: c.help, helpMsg: "Prints the help message."},
		{aliases: []string{"break", "b"}, cmdFn: breakpoint, helpMsg: "break [name] <linespec>"},
		{aliases: []string{"trace", "t"}, cmdFn: tracepoint, helpMsg: "Set tracepoint, takes the same arguments as break."},
		{aliases: []string{"restart", "r"}, cmdFn: restart, helpMsg: "Restart process."},
		{aliases: []string{"continue", "c"}, cmdFn: cont, helpMsg: "Run until breakpoint or program termination."},
		{aliases: []string{"step", "s"}, cmdFn: step, helpMsg: "Single step through program."},
		{aliases: []string{"step-instruction", "si"}, cmdFn: stepInstruction, helpMsg: "Single step a single cpu instruction."},
		{aliases: []string{"next", "n"}, cmdFn: next, helpMsg: "Step over to next source line."},
		{aliases: []string{"threads"}, cmdFn: threads, helpMsg: "Print out info for every traced thread."},
		{aliases: []string{"thread", "tr"}, cmdFn: thread, helpMsg: "Switch to the specified thread."},
		{aliases: []string{"clear"}, cmdFn: clear, helpMsg: "Deletes breakpoint."},
		{aliases: []string{"clearall"}, cmdFn: clearAll, helpMsg: "clearall [<linespec>]. Deletes all breakpoints. If <linespec> is provided, only matching breakpoints will be deleted."},
		{aliases: []string{"goroutines"}, cmdFn: goroutines, helpMsg: "goroutines [-u (default: user location)|-r (runtime location)|-g (go statement location)] Print out info for every goroutine."},
		{aliases: []string{"goroutine"}, allowedPrefixes: onPrefix | scopePrefix, cmdFn: c.goroutine, helpMsg: "Sets current goroutine."},
		{aliases: []string{"breakpoints", "bp"}, cmdFn: breakpoints, helpMsg: "Print out info for active breakpoints."},
		{aliases: []string{"print", "p"}, allowedPrefixes: onPrefix | scopePrefix, cmdFn: printVar, helpMsg: "Evaluate a variable."},
		{aliases: []string{"set"}, allowedPrefixes: scopePrefix, cmdFn: setVar, helpMsg: "Changes the value of a variable."},
		{aliases: []string{"sources"}, cmdFn: filterSortAndOutput(sources), helpMsg: "Print list of source files, optionally filtered by a regexp."},
		{aliases: []string{"funcs"}, cmdFn: filterSortAndOutput(funcs), helpMsg: "Print list of functions, optionally filtered by a regexp."},
		{aliases: []string{"types"}, cmdFn: filterSortAndOutput(types), helpMsg: "Print list of types, optionally filtered by a regexp."},
		{aliases: []string{"args"}, allowedPrefixes: scopePrefix, cmdFn: filterSortAndOutput(args), helpMsg: "Print function arguments, optionally filtered by a regexp."},
		{aliases: []string{"locals"}, allowedPrefixes: scopePrefix, cmdFn: filterSortAndOutput(locals), helpMsg: "Print function locals, optionally filtered by a regexp."},
		{aliases: []string{"vars"}, cmdFn: filterSortAndOutput(vars), helpMsg: "Print package variables, optionally filtered by a regexp."},
		{aliases: []string{"regs"}, cmdFn: regs, helpMsg: "Print contents of CPU registers."},
		{aliases: []string{"exit", "quit", "q"}, cmdFn: exitCommand, helpMsg: "Exit the debugger."},
		{aliases: []string{"list", "ls"}, allowedPrefixes: scopePrefix, cmdFn: listCommand, helpMsg: "list <linespec>.  Show source around current point or provided linespec."},
		{aliases: []string{"stack", "bt"}, allowedPrefixes: scopePrefix | onPrefix, cmdFn: stackCommand, helpMsg: "stack [<depth>] [-full]. Prints stack."},
		{aliases: []string{"frame"}, allowedPrefixes: scopePrefix, cmdFn: c.frame, helpMsg: "Sets current stack frame (0 is the top of the stack)"},
		{aliases: []string{"source"}, cmdFn: c.sourceCommand, helpMsg: "Executes a file containing a list of delve commands"},
		{aliases: []string{"disassemble", "disass"}, allowedPrefixes: scopePrefix, cmdFn: disassCommand, helpMsg: "Displays disassembly of specific function or address range: disassemble [-a <start> <end>] [-l <locspec>]"},
		{aliases: []string{"on"}, cmdFn: c.onCmd, helpMsg: "on <breakpoint name or id> <command>. Executes command when the specified breakpoint is hit (supported commands: print <expression>, stack [<depth>] [-full] and goroutine)"},
		{aliases: []string{"condition", "cond"}, cmdFn: conditionCmd, helpMsg: "cond <breakpoint name or id> <boolean expression>. Specifies that the breakpoint or tracepoint should break only if the boolean expression is true."},
	}

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

func noCmdAvailable(t *Term, ctx callContext, args string) error {
	return fmt.Errorf("command not available")
}

func nullCommand(t *Term, ctx callContext, args string) error {
	return nil
}

func (c *Commands) help(t *Term, ctx callContext, args string) error {
	fmt.Println("The following commands are available:")
	w := new(tabwriter.Writer)
	w.Init(os.Stdout, 0, 8, 0, '-', 0)
	for _, cmd := range c.cmds {
		if len(cmd.aliases) > 1 {
			fmt.Fprintf(w, "    %s (alias: %s) \t %s\n", cmd.aliases[0], strings.Join(cmd.aliases[1:], " | "), cmd.helpMsg)
		} else {
			fmt.Fprintf(w, "    %s \t %s\n", cmd.aliases[0], cmd.helpMsg)
		}
	}
	return w.Flush()
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
	return fmt.Sprintf("%d - %s: %s", g.ID, locname, formatLocation(loc))
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
	stateChan := t.client.Continue()
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

func step(t *Term, ctx callContext, args string) error {
	state, err := t.client.Step()
	if err != nil {
		return err
	}
	printcontext(t, state)
	printfile(t, state.CurrentThread.File, state.CurrentThread.Line, true)
	return nil
}

func stepInstruction(t *Term, ctx callContext, args string) error {
	state, err := t.client.StepInstruction()
	if err != nil {
		return err
	}
	printcontext(t, state)
	printfile(t, state.CurrentThread.File, state.CurrentThread.Line, true)
	return nil
}

func next(t *Term, ctx callContext, args string) error {
	state, err := t.client.Next()
	if err != nil {
		return err
	}
	printcontext(t, state)
	printfile(t, state.CurrentThread.File, state.CurrentThread.Line, true)
	return nil
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
	val, err := t.client.EvalVariable(ctx.Scope, args)
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

func filterVariables(vars []api.Variable, filter string) []string {
	reg, err := regexp.Compile(filter)
	if err != nil {
		fmt.Fprintf(os.Stderr, err.Error())
		return nil
	}
	data := make([]string, 0, len(vars))
	for _, v := range vars {
		if reg == nil || reg.Match([]byte(v.Name)) {
			data = append(data, fmt.Sprintf("%s = %s", v.Name, v.SinglelineString()))
		}
	}
	return data
}

func sources(t *Term, ctx callContext, filter string) ([]string, error) {
	return t.client.ListSources(filter)
}

func funcs(t *Term, ctx callContext, filter string) ([]string, error) {
	return t.client.ListFunctions(filter)
}

func types(t *Term, ctx callContext, filter string) ([]string, error) {
	return t.client.ListTypes(filter)
}

func args(t *Term, ctx callContext, filter string) ([]string, error) {
	vars, err := t.client.ListFunctionArgs(ctx.Scope)
	if err != nil {
		return nil, err
	}
	return describeNoVars("args", filterVariables(vars, filter)), nil
}

func locals(t *Term, ctx callContext, filter string) ([]string, error) {
	locals, err := t.client.ListLocalVariables(ctx.Scope)
	if err != nil {
		return nil, err
	}
	return describeNoVars("locals", filterVariables(locals, filter)), nil
}

func vars(t *Term, ctx callContext, filter string) ([]string, error) {
	vars, err := t.client.ListPackageVariables(filter)
	if err != nil {
		return nil, err
	}
	return describeNoVars("vars", filterVariables(vars, filter)), nil
}

func regs(t *Term, ctx callContext, args string) error {
	regs, err := t.client.ListRegisters()
	if err != nil {
		return err
	}
	fmt.Println(regs)
	return nil
}

func filterSortAndOutput(fn filteringFunc) cmdfunc {
	return func(t *Term, ctx callContext, args string) error {
		var filter string
		if len(args) > 0 {
			if _, err := regexp.Compile(args); err != nil {
				return fmt.Errorf("invalid filter argument: %s", err.Error())
			}
			filter = args
		}
		data, err := fn(t, ctx, filter)
		if err != nil {
			return err
		}
		sort.Sort(sort.StringSlice(data))
		for _, d := range data {
			fmt.Println(d)
		}
		return nil
	}
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
	stack, err := t.client.Stacktrace(ctx.Scope.GoroutineID, depth, full)
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
		locs, err := t.client.Stacktrace(ctx.Scope.GoroutineID, ctx.Scope.Frame, false)
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
	s := strings.Repeat(" ", d+2+len(ind))

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
	if th.Breakpoint.Tracepoint && th.BreakpointInfo != nil {
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
		bpi := th.BreakpointInfo

		if bpi.Goroutine != nil {
			writeGoroutineLong(os.Stdout, bpi.Goroutine, "\t")
		}

		if len(bpi.Variables) > 0 {
			ss := make([]string, len(bpi.Variables))
			for i, v := range bpi.Variables {
				ss[i] = fmt.Sprintf("%s: %s", v.Name, v.MultilineString(""))
			}
			fmt.Printf("\t%s\n", strings.Join(ss, ", "))
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

func describeNoVars(varType string, data []string) []string {
	if len(data) == 0 {
		return []string{fmt.Sprintf("(no %s)", varType)}
	}
	return data
}
