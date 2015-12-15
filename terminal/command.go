// Package terminal implements functions for responding to user
// input and dispatching to appropriate backend commands.
package terminal

import (
	"bufio"
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

type cmdfunc func(t *Term, args string) error
type scopedCmdfunc func(t *Term, scope api.EvalScope, args string) error

type filteringFunc func(t *Term, filter string) ([]string, error)
type scopedFilteringFunc func(t *Term, scope api.EvalScope, filter string) ([]string, error)

type command struct {
	aliases []string
	helpMsg string
	cmdFn   cmdfunc
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

type Commands struct {
	cmds    []command
	lastCmd cmdfunc
	client  service.Client
}

// Returns a Commands struct with default commands defined.
func DebugCommands(client service.Client) *Commands {
	c := &Commands{client: client}

	c.cmds = []command{
		{aliases: []string{"help"}, cmdFn: c.help, helpMsg: "Prints the help message."},
		{aliases: []string{"break", "b"}, cmdFn: breakpoint, helpMsg: "break <linespec> [-stack <n>|-goroutine|<variable name>]*"},
		{aliases: []string{"trace", "t"}, cmdFn: tracepoint, helpMsg: "Set tracepoint, takes the same arguments as break."},
		{aliases: []string{"restart", "r"}, cmdFn: restart, helpMsg: "Restart process."},
		{aliases: []string{"continue", "c"}, cmdFn: cont, helpMsg: "Run until breakpoint or program termination."},
		{aliases: []string{"step", "si"}, cmdFn: step, helpMsg: "Single step through program."},
		{aliases: []string{"next", "n"}, cmdFn: next, helpMsg: "Step over to next source line."},
		{aliases: []string{"threads"}, cmdFn: threads, helpMsg: "Print out info for every traced thread."},
		{aliases: []string{"thread", "tr"}, cmdFn: thread, helpMsg: "Switch to the specified thread."},
		{aliases: []string{"clear"}, cmdFn: clear, helpMsg: "Deletes breakpoint."},
		{aliases: []string{"clearall"}, cmdFn: clearAll, helpMsg: "clearall [<linespec>]. Deletes all breakpoints. If <linespec> is provided, only matching breakpoints will be deleted."},
		{aliases: []string{"goroutines"}, cmdFn: goroutines, helpMsg: "goroutines [-u (default: user location)|-r (runtime location)|-g (go statement location)] Print out info for every goroutine."},
		{aliases: []string{"goroutine"}, cmdFn: goroutine, helpMsg: "Sets current goroutine."},
		{aliases: []string{"breakpoints", "bp"}, cmdFn: breakpoints, helpMsg: "Print out info for active breakpoints."},
		{aliases: []string{"print", "p"}, cmdFn: g0f0(printVar), helpMsg: "Evaluate a variable."},
		{aliases: []string{"set"}, cmdFn: g0f0(setVar), helpMsg: "Changes the value of a variable."},
		{aliases: []string{"sources"}, cmdFn: filterSortAndOutput(sources), helpMsg: "Print list of source files, optionally filtered by a regexp."},
		{aliases: []string{"funcs"}, cmdFn: filterSortAndOutput(funcs), helpMsg: "Print list of functions, optionally filtered by a regexp."},
		{aliases: []string{"args"}, cmdFn: filterSortAndOutput(g0f0filter(args)), helpMsg: "Print function arguments, optionally filtered by a regexp."},
		{aliases: []string{"locals"}, cmdFn: filterSortAndOutput(g0f0filter(locals)), helpMsg: "Print function locals, optionally filtered by a regexp."},
		{aliases: []string{"vars"}, cmdFn: filterSortAndOutput(vars), helpMsg: "Print package variables, optionally filtered by a regexp."},
		{aliases: []string{"regs"}, cmdFn: regs, helpMsg: "Print contents of CPU registers."},
		{aliases: []string{"exit", "quit", "q"}, cmdFn: exitCommand, helpMsg: "Exit the debugger."},
		{aliases: []string{"list", "ls"}, cmdFn: listCommand, helpMsg: "list <linespec>.  Show source around current point or provided linespec."},
		{aliases: []string{"stack", "bt"}, cmdFn: stackCommand, helpMsg: "stack [<depth>] [-full]. Prints stack."},
		{aliases: []string{"frame"}, cmdFn: frame, helpMsg: "Sets current stack frame (0 is the top of the stack)"},
		{aliases: []string{"source"}, cmdFn: c.sourceCommand, helpMsg: "Executes a file containing a list of delve commands"},
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
func (c *Commands) Find(cmdstr string) cmdfunc {
	// If <enter> use last command, if there was one.
	if cmdstr == "" {
		if c.lastCmd != nil {
			return c.lastCmd
		}
		return nullCommand
	}

	for _, v := range c.cmds {
		if v.match(cmdstr) {
			c.lastCmd = v.cmdFn
			return v.cmdFn
		}
	}

	return noCmdAvailable
}

// Merge takes aliases defined in the config struct and merges them with the default aliases.
func (c *Commands) Merge(allAliases map[string][]string) {
	for i := range c.cmds {
		if aliases, ok := allAliases[c.cmds[i].aliases[0]]; ok {
			c.cmds[i].aliases = append(c.cmds[i].aliases, aliases...)
		}
	}
}

func CommandFunc(fn func() error) cmdfunc {
	return func(t *Term, args string) error {
		return fn()
	}
}

func noCmdAvailable(t *Term, args string) error {
	return fmt.Errorf("command not available")
}

func nullCommand(t *Term, args string) error {
	return nil
}

func (c *Commands) help(t *Term, args string) error {
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

func threads(t *Term, args string) error {
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
				prefix, th.ID, th.PC, shortenFilePath(th.File),
				th.Line, th.Function.Name)
		} else {
			fmt.Printf("%sThread %s\n", prefix, formatThread(th))
		}
	}
	return nil
}

func thread(t *Term, args string) error {
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

func goroutines(t *Term, argstr string) error {
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
		default:
			fmt.Errorf("wrong argument: '%s'", args[0])
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

func goroutine(t *Term, argstr string) error {
	if argstr == "" {
		return printscope(t)
	}

	if strings.Index(argstr, " ") < 0 {
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
	}

	return scopePrefix(t, "goroutine "+argstr)
}

func frame(t *Term, args string) error {
	return scopePrefix(t, "frame "+args)
}

func scopePrefix(t *Term, cmdstr string) error {
	scope := api.EvalScope{-1, 0}
	lastcmd := ""
	rest := cmdstr

	nexttok := func() string {
		v := strings.SplitN(rest, " ", 2)
		if len(v) > 1 {
			rest = v[1]
		} else {
			rest = ""
		}
		return v[0]
	}

	callFilterSortAndOutput := func(fn scopedFilteringFunc, fnargs string) error {
		outfn := filterSortAndOutput(func(t *Term, filter string) ([]string, error) {
			return fn(t, scope, filter)
		})
		return outfn(t, fnargs)
	}

	for {
		cmd := nexttok()
		if cmd == "" && rest == "" {
			break
		}
		switch cmd {
		case "goroutine":
			if rest == "" {
				return fmt.Errorf("goroutine command needs an argument")
			}
			n, err := strconv.Atoi(nexttok())
			if err != nil {
				return fmt.Errorf("invalid argument to goroutine, expected integer")
			}
			scope.GoroutineID = int(n)
		case "frame":
			if rest == "" {
				return fmt.Errorf("frame command needs an argument")
			}
			n, err := strconv.Atoi(nexttok())
			if err != nil {
				return fmt.Errorf("invalid argument to frame, expected integer")
			}
			scope.Frame = int(n)
		case "list", "ls":
			frame, gid := scope.Frame, scope.GoroutineID
			locs, err := t.client.Stacktrace(gid, frame, false)
			if err != nil {
				return err
			}
			if frame >= len(locs) {
				return fmt.Errorf("Frame %d does not exist in goroutine %d", frame, gid)
			}
			loc := locs[frame]
			return printfile(t, loc.File, loc.Line, true)
		case "stack", "bt":
			depth, full, err := parseStackArgs(rest)
			if err != nil {
				return err
			}
			stack, err := t.client.Stacktrace(scope.GoroutineID, depth, full)
			if err != nil {
				return err
			}
			printStack(stack, "")
			return nil
		case "locals":
			return callFilterSortAndOutput(locals, rest)
		case "args":
			return callFilterSortAndOutput(args, rest)
		case "print", "p":
			return printVar(t, scope, rest)
		case "set":
			return setVar(t, scope, rest)
		default:
			return fmt.Errorf("unknown command %s", cmd)
		}
		lastcmd = cmd
	}

	return fmt.Errorf("no command passed to %s", lastcmd)
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
	return fmt.Sprintf("%d at %s:%d", th.ID, shortenFilePath(th.File), th.Line)
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
	return fmt.Sprintf("%s:%d %s (%#v)", shortenFilePath(loc.File), loc.Line, fname, loc.PC)
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

func restart(t *Term, args string) error {
	if err := t.client.Restart(); err != nil {
		return err
	}
	fmt.Println("Process restarted with PID", t.client.ProcessPid())
	return nil
}

func cont(t *Term, args string) error {
	stateChan := t.client.Continue()
	for state := range stateChan {
		if state.Err != nil {
			return state.Err
		}
		printcontext(t, state)
	}
	return nil
}

func step(t *Term, args string) error {
	state, err := t.client.Step()
	if err != nil {
		return err
	}
	printcontext(t, state)
	return nil
}

func next(t *Term, args string) error {
	state, err := t.client.Next()
	if err != nil {
		return err
	}
	printcontext(t, state)
	return nil
}

func clear(t *Term, args string) error {
	if len(args) == 0 {
		return fmt.Errorf("not enough arguments")
	}
	id, err := strconv.Atoi(args)
	if err != nil {
		return err
	}
	bp, err := t.client.ClearBreakpoint(id)
	if err != nil {
		return err
	}
	fmt.Printf("Breakpoint %d cleared at %#v for %s %s:%d\n", bp.ID, bp.Addr, bp.FunctionName, shortenFilePath(bp.File), bp.Line)
	return nil
}

func clearAll(t *Term, args string) error {
	breakPoints, err := t.client.ListBreakpoints()
	if err != nil {
		return err
	}

	var locPCs map[uint64]struct{}
	if args != "" {
		locs, err := t.client.FindLocation(api.EvalScope{-1, 0}, args)
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

		_, err := t.client.ClearBreakpoint(bp.ID)
		if err != nil {
			fmt.Printf("Couldn't delete breakpoint %d at %#v %s:%d: %s\n", bp.ID, bp.Addr, shortenFilePath(bp.File), bp.Line, err)
		}
		fmt.Printf("Breakpoint %d cleared at %#v for %s %s:%d\n", bp.ID, bp.Addr, bp.FunctionName, shortenFilePath(bp.File), bp.Line)
	}
	return nil
}

type ById []*api.Breakpoint

func (a ById) Len() int           { return len(a) }
func (a ById) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ById) Less(i, j int) bool { return a[i].ID < a[j].ID }

func breakpoints(t *Term, args string) error {
	breakPoints, err := t.client.ListBreakpoints()
	if err != nil {
		return err
	}
	sort.Sort(ById(breakPoints))
	for _, bp := range breakPoints {
		thing := "Breakpoint"
		if bp.Tracepoint {
			thing = "Tracepoint"
		}
		fmt.Printf("%s %d at %#v %s:%d (%d)\n", thing, bp.ID, bp.Addr, shortenFilePath(bp.File), bp.Line, bp.TotalHitCount)

		var attrs []string
		if bp.Stacktrace > 0 {
			attrs = append(attrs, "-stack")
			attrs = append(attrs, strconv.Itoa(bp.Stacktrace))
		}
		if bp.Goroutine {
			attrs = append(attrs, "-goroutine")
		}
		for i := range bp.Variables {
			attrs = append(attrs, bp.Variables[i])
		}
		if len(attrs) > 0 {
			fmt.Printf("\t%s\n", strings.Join(attrs, " "))
		}
	}
	return nil
}

func setBreakpoint(t *Term, tracepoint bool, argstr string) error {
	args := strings.Split(argstr, " ")
	if len(args) < 1 {
		return fmt.Errorf("address required, specify either a function name or <file:line>")
	}
	requestedBp := &api.Breakpoint{}

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-stack":
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return fmt.Errorf("argument of -stack must be a number")
			}
			requestedBp.Stacktrace = n
		case "-goroutine":
			requestedBp.Goroutine = true
		default:
			requestedBp.Variables = append(requestedBp.Variables, args[i])
		}
	}

	requestedBp.Tracepoint = tracepoint
	locs, err := t.client.FindLocation(api.EvalScope{-1, 0}, args[0])
	if err != nil {
		return err
	}
	thing := "Breakpoint"
	if tracepoint {
		thing = "Tracepoint"
	}
	for _, loc := range locs {
		requestedBp.Addr = loc.PC

		bp, err := t.client.CreateBreakpoint(requestedBp)
		if err != nil {
			return err
		}

		fmt.Printf("%s %d set at %#v for %s %s:%d\n", thing, bp.ID, bp.Addr, bp.FunctionName, shortenFilePath(bp.File), bp.Line)
	}
	return nil
}

func breakpoint(t *Term, args string) error {
	return setBreakpoint(t, false, args)
}

func tracepoint(t *Term, args string) error {
	return setBreakpoint(t, true, args)
}

func g0f0(fn scopedCmdfunc) cmdfunc {
	return func(t *Term, args string) error {
		return fn(t, api.EvalScope{-1, 0}, args)
	}
}

func g0f0filter(fn scopedFilteringFunc) filteringFunc {
	return func(t *Term, filter string) ([]string, error) {
		return fn(t, api.EvalScope{-1, 0}, filter)
	}
}

func printVar(t *Term, scope api.EvalScope, args string) error {
	if len(args) == 0 {
		return fmt.Errorf("not enough arguments")
	}
	val, err := t.client.EvalVariable(scope, args)
	if err != nil {
		return err
	}

	fmt.Println(val.MultilineString(""))
	return nil
}

func setVar(t *Term, scope api.EvalScope, args string) error {
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
	return t.client.SetVariable(scope, lexpr, rexpr)
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

func sources(t *Term, filter string) ([]string, error) {
	return t.client.ListSources(filter)
}

func funcs(t *Term, filter string) ([]string, error) {
	return t.client.ListFunctions(filter)
}

func args(t *Term, scope api.EvalScope, filter string) ([]string, error) {
	vars, err := t.client.ListFunctionArgs(scope)
	if err != nil {
		return nil, err
	}
	return filterVariables(vars, filter), nil
}

func locals(t *Term, scope api.EvalScope, filter string) ([]string, error) {
	locals, err := t.client.ListLocalVariables(scope)
	if err != nil {
		return nil, err
	}
	return filterVariables(locals, filter), nil
}

func vars(t *Term, filter string) ([]string, error) {
	vars, err := t.client.ListPackageVariables(filter)
	if err != nil {
		return nil, err
	}
	return filterVariables(vars, filter), nil
}

func regs(t *Term, args string) error {
	regs, err := t.client.ListRegisters()
	if err != nil {
		return err
	}
	fmt.Println(regs)
	return nil
}

func filterSortAndOutput(fn filteringFunc) cmdfunc {
	return func(t *Term, args string) error {
		var filter string
		if len(args) > 0 {
			if _, err := regexp.Compile(args); err != nil {
				return fmt.Errorf("invalid filter argument: %s", err.Error())
			}
			filter = args
		}
		data, err := fn(t, filter)
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

func stackCommand(t *Term, args string) error {
	var (
		err         error
		goroutineid = -1
	)
	depth, full, err := parseStackArgs(args)
	if err != nil {
		return err
	}
	stack, err := t.client.Stacktrace(goroutineid, depth, full)
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

func listCommand(t *Term, args string) error {
	if len(args) == 0 {
		state, err := t.client.GetState()
		if err != nil {
			return err
		}
		printcontext(t, state)
		return nil
	}

	locs, err := t.client.FindLocation(api.EvalScope{-1, 0}, args)
	if err != nil {
		return err
	}
	if len(locs) > 1 {
		return debugger.AmbiguousLocationError{Location: args, CandidatesLocation: locs}
	}
	printfile(t, locs[0].File, locs[0].Line, false)
	return nil
}

func (cmds *Commands) sourceCommand(t *Term, args string) error {
	if len(args) == 0 {
		return fmt.Errorf("wrong number of arguments: source <filename>")
	}

	return cmds.executeFile(t, args)
}

func digits(n int) int {
	return int(math.Floor(math.Log10(float64(n)))) + 1
}

func printStack(stack []api.Stackframe, ind string) {
	d := digits(len(stack) - 1)
	fmtstr := "%s%" + strconv.Itoa(d) + "d  0x%016x in %s\n"
	s := strings.Repeat(" ", d+2+len(ind))

	for i := range stack {
		name := "(nil)"
		if stack[i].Function != nil {
			name = stack[i].Function.Name
		}
		fmt.Printf(fmtstr, ind, i, stack[i].PC, name)
		fmt.Printf("%sat %s:%d\n", s, shortenFilePath(stack[i].File), stack[i].Line)

		for j := range stack[i].Arguments {
			fmt.Printf("%s    %s = %s\n", s, stack[i].Arguments[j].Name, stack[i].Arguments[j].SinglelineString())
		}
		for j := range stack[i].Locals {
			fmt.Printf("%s    %s = %s\n", s, stack[i].Locals[j].Name, stack[i].Locals[j].SinglelineString())
		}
	}
}

func printcontext(t *Term, state *api.DebuggerState) error {
	if state.CurrentThread == nil {
		fmt.Println("No current thread available")
		return nil
	}
	if len(state.CurrentThread.File) == 0 {
		fmt.Printf("Stopped at: 0x%x\n", state.CurrentThread.PC)
		t.Println("=>", "no source available")
		return nil
	}
	var fn *api.Function
	if state.CurrentThread.Function != nil {
		fn = state.CurrentThread.Function
	}

	if state.Breakpoint != nil {
		args := ""
		if state.Breakpoint.Tracepoint {
			var arg []string
			for _, ar := range state.CurrentThread.Function.Args {
				arg = append(arg, ar.SinglelineString())
			}
			args = strings.Join(arg, ", ")
		}

		if hitCount, ok := state.Breakpoint.HitCount[strconv.Itoa(state.SelectedGoroutine.ID)]; ok {
			fmt.Printf("> %s(%s) %s:%d (hits goroutine(%d):%d total:%d)\n",
				fn.Name,
				args,
				shortenFilePath(state.CurrentThread.File),
				state.CurrentThread.Line,
				state.SelectedGoroutine.ID,
				hitCount,
				state.Breakpoint.TotalHitCount)
		} else {
			fmt.Printf("> %s(%s) %s:%d (hits total:%d)\n",
				fn.Name,
				args,
				shortenFilePath(state.CurrentThread.File),
				state.CurrentThread.Line,
				state.Breakpoint.TotalHitCount)
		}
	} else {
		fmt.Printf("> %s() %s:%d\n", fn.Name, shortenFilePath(state.CurrentThread.File), state.CurrentThread.Line)
	}

	if state.BreakpointInfo != nil {
		bpi := state.BreakpointInfo

		if bpi.Goroutine != nil {
			writeGoroutineLong(os.Stdout, bpi.Goroutine, "\t")
		}

		ss := make([]string, len(bpi.Variables))
		for i, v := range bpi.Variables {
			ss[i] = fmt.Sprintf("%s: %v", v.Name, v.MultilineString(""))
		}
		fmt.Printf("\t%s\n", strings.Join(ss, ", "))

		if bpi.Stacktrace != nil {
			fmt.Printf("\tStack:\n")
			printStack(bpi.Stacktrace, "\t\t")
		}
	}
	if state.Breakpoint != nil && state.Breakpoint.Tracepoint {
		return nil
	}
	return printfile(t, state.CurrentThread.File, state.CurrentThread.Line, true)
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

type ExitRequestError struct{}

func (ere ExitRequestError) Error() string {
	return ""
}

func exitCommand(t *Term, args string) error {
	return ExitRequestError{}
}

func shortenFilePath(fullPath string) string {
	workingDir, _ := os.Getwd()
	return strings.Replace(fullPath, workingDir, ".", 1)
}

func (cmds *Commands) executeFile(t *Term, name string) error {
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
		cmd := cmds.Find(cmdstr)
		err := cmd(t, args)

		if err != nil {
			fmt.Printf("%s:%d: %v\n", name, lineno, err)
		}
	}

	return scanner.Err()
}
