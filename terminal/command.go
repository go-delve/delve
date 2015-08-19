// Package command implements functions for responding to user
// input and dispatching to appropriate backend commands.
package terminal

import (
	"bufio"
	"fmt"
	"io"
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

type cmdfunc func(client service.Client, args ...string) error

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
		{aliases: []string{"clearall"}, cmdFn: clearAll, helpMsg: "Deletes all breakpoints."},
		{aliases: []string{"goroutines"}, cmdFn: goroutines, helpMsg: "Print out info for every goroutine."},
		{aliases: []string{"breakpoints", "bp"}, cmdFn: breakpoints, helpMsg: "Print out info for active breakpoints."},
		{aliases: []string{"print", "p"}, cmdFn: printVar, helpMsg: "Evaluate a variable."},
		{aliases: []string{"sources"}, cmdFn: filterSortAndOutput(sources), helpMsg: "Print list of source files, optionally filtered by a regexp."},
		{aliases: []string{"funcs"}, cmdFn: filterSortAndOutput(funcs), helpMsg: "Print list of functions, optionally filtered by a regexp."},
		{aliases: []string{"args"}, cmdFn: filterSortAndOutput(args), helpMsg: "Print function arguments, optionally filtered by a regexp."},
		{aliases: []string{"locals"}, cmdFn: filterSortAndOutput(locals), helpMsg: "Print function locals, optionally filtered by a regexp."},
		{aliases: []string{"vars"}, cmdFn: filterSortAndOutput(vars), helpMsg: "Print package variables, optionally filtered by a regexp."},
		{aliases: []string{"regs"}, cmdFn: regs, helpMsg: "Print contents of CPU registers."},
		{aliases: []string{"exit", "quit", "q"}, cmdFn: exitCommand, helpMsg: "Exit the debugger."},
		{aliases: []string{"stack", "bt"}, cmdFn: stackCommand, helpMsg: "stack [<depth> [<goroutine id>]]. Prints stack."},
		{aliases: []string{"list", "ls"}, cmdFn: listCommand, helpMsg: "list <linespec>.  Show source around current point or provided linespec."},
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

func CommandFunc(fn func() error) cmdfunc {
	return func(client service.Client, args ...string) error {
		return fn()
	}
}

func noCmdAvailable(client service.Client, args ...string) error {
	return fmt.Errorf("command not available")
}

func nullCommand(client service.Client, args ...string) error {
	return nil
}

func (c *Commands) help(client service.Client, args ...string) error {
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

func threads(client service.Client, args ...string) error {
	threads, err := client.ListThreads()
	if err != nil {
		return err
	}
	state, err := client.GetState()
	if err != nil {
		return err
	}
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
			fmt.Printf("%sThread %d at %s:%d\n", prefix, th.ID, shortenFilePath(th.File), th.Line)
		}
	}
	return nil
}

func thread(client service.Client, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("you must specify a thread")
	}
	tid, err := strconv.Atoi(args[0])
	if err != nil {
		return err
	}
	oldState, err := client.GetState()
	if err != nil {
		return err
	}
	newState, err := client.SwitchThread(tid)
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

func goroutines(client service.Client, args ...string) error {
	gs, err := client.ListGoroutines()
	if err != nil {
		return err
	}
	fmt.Printf("[%d goroutines]\n", len(gs))
	for _, g := range gs {
		fmt.Printf("Goroutine %s\n", formatGoroutine(g))
	}
	return nil
}

func formatGoroutine(g *api.Goroutine) string {
	fname := ""
	if g.Function != nil {
		fname = g.Function.Name
	}
	return fmt.Sprintf("%d - %s:%d %s (%#v)", g.ID, shortenFilePath(g.File), g.Line, fname, g.PC)
}

func restart(client service.Client, args ...string) error {
	if err := client.Restart(); err != nil {
		return err
	}
	fmt.Println("Process restarted with PID", client.ProcessPid())
	return nil
}

func cont(client service.Client, args ...string) error {
	stateChan := client.Continue()
	for state := range stateChan {
		if state.Err != nil {
			return state.Err
		}
		printcontext(state)
	}
	return nil
}

func step(client service.Client, args ...string) error {
	state, err := client.Step()
	if err != nil {
		return err
	}
	printcontext(state)
	return nil
}

func next(client service.Client, args ...string) error {
	state, err := client.Next()
	if err != nil {
		return err
	}
	printcontext(state)
	return nil
}

func clear(client service.Client, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("not enough arguments")
	}
	id, err := strconv.Atoi(args[0])
	if err != nil {
		return err
	}
	bp, err := client.ClearBreakpoint(id)
	if err != nil {
		return err
	}
	fmt.Printf("Breakpoint %d cleared at %#v for %s %s:%d\n", bp.ID, bp.Addr, bp.FunctionName, shortenFilePath(bp.File), bp.Line)
	return nil
}

func clearAll(client service.Client, args ...string) error {
	breakPoints, err := client.ListBreakpoints()
	if err != nil {
		return err
	}
	for _, bp := range breakPoints {
		_, err := client.ClearBreakpoint(bp.ID)
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

func breakpoints(client service.Client, args ...string) error {
	breakPoints, err := client.ListBreakpoints()
	if err != nil {
		return err
	}
	sort.Sort(ById(breakPoints))
	for _, bp := range breakPoints {
		thing := "Breakpoint"
		if bp.Tracepoint {
			thing = "Tracepoint"
		}
		fmt.Printf("%s %d at %#v %s:%d\n", thing, bp.ID, bp.Addr, shortenFilePath(bp.File), bp.Line)

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

func setBreakpoint(client service.Client, tracepoint bool, args ...string) error {
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
	locs, err := client.FindLocation(args[0])
	if err != nil {
		return err
	}
	thing := "Breakpoint"
	if tracepoint {
		thing = "Tracepoint"
	}
	for _, loc := range locs {
		requestedBp.Addr = loc.PC

		bp, err := client.CreateBreakpoint(requestedBp)
		if err != nil {
			return err
		}

		fmt.Printf("%s %d set at %#v for %s %s:%d\n", thing, bp.ID, bp.Addr, bp.FunctionName, shortenFilePath(bp.File), bp.Line)
	}
	return nil
}

func breakpoint(client service.Client, args ...string) error {
	return setBreakpoint(client, false, args...)
}

func tracepoint(client service.Client, args ...string) error {
	return setBreakpoint(client, true, args...)
}

func printVar(client service.Client, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("not enough arguments")
	}
	val, err := client.EvalVariable(args[0])
	if err != nil {
		return err
	}
	fmt.Println(val.Value)
	return nil
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
			data = append(data, fmt.Sprintf("%s = %s", v.Name, v.Value))
		}
	}
	return data
}

func sources(client service.Client, filter string) ([]string, error) {
	return client.ListSources(filter)
}

func funcs(client service.Client, filter string) ([]string, error) {
	return client.ListFunctions(filter)
}

func args(client service.Client, filter string) ([]string, error) {
	vars, err := client.ListFunctionArgs()
	if err != nil {
		return nil, err
	}
	return filterVariables(vars, filter), nil
}

func locals(client service.Client, filter string) ([]string, error) {
	locals, err := client.ListLocalVariables()
	if err != nil {
		return nil, err
	}
	return filterVariables(locals, filter), nil
}

func vars(client service.Client, filter string) ([]string, error) {
	vars, err := client.ListPackageVariables(filter)
	if err != nil {
		return nil, err
	}
	return filterVariables(vars, filter), nil
}

func regs(client service.Client, args ...string) error {
	regs, err := client.ListRegisters()
	if err != nil {
		return err
	}
	fmt.Println(regs)
	return nil
}

func filterSortAndOutput(fn func(client service.Client, filter string) ([]string, error)) cmdfunc {
	return func(client service.Client, args ...string) error {
		var filter string
		if len(args) == 1 {
			if _, err := regexp.Compile(args[0]); err != nil {
				return fmt.Errorf("invalid filter argument: %s", err.Error())
			}
			filter = args[0]
		}
		data, err := fn(client, filter)
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

func stackCommand(client service.Client, args ...string) error {
	var err error

	goroutineid := -1
	depth := 10

	switch len(args) {
	case 0:
		// nothing to do
	case 2:
		goroutineid, err = strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("Wrong argument: expected integer")
		}
		fallthrough
	case 1:
		depth, err = strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("Wrong argument: expected integer")
		}

	default:
		return fmt.Errorf("Wrong number of arguments to stack")
	}

	stack, err := client.Stacktrace(goroutineid, depth)
	if err != nil {
		return err
	}
	printStack(stack, "")
	return nil
}

func listCommand(client service.Client, args ...string) error {
	if len(args) == 0 {
		state, err := client.GetState()
		if err != nil {
			return err
		}
		printcontext(state)
		return nil
	}

	locs, err := client.FindLocation(args[0])
	if err != nil {
		return err
	}
	if len(locs) > 1 {
		return debugger.AmbiguousLocationError{Location: args[0], CandidatesLocation: locs}
	}
	printfile(locs[0].File, locs[0].Line, false)
	return nil
}

func printStack(stack []api.Location, ind string) {
	for i := range stack {
		name := "(nil)"
		if stack[i].Function != nil {
			name = stack[i].Function.Name
		}
		fmt.Printf("%s%d. %s %s:%d (%#v)\n", ind, i, name, shortenFilePath(stack[i].File), stack[i].Line, stack[i].PC)
	}
}

func printcontext(state *api.DebuggerState) error {
	if state.CurrentThread == nil {
		fmt.Println("No current thread available")
		return nil
	}
	if len(state.CurrentThread.File) == 0 {
		fmt.Printf("Stopped at: 0x%x\n", state.CurrentThread.PC)
		fmt.Printf("\033[34m=>\033[0m    no source available\n")
		return nil
	}
	var fn *api.Function
	if state.CurrentThread.Function != nil {
		fn = state.CurrentThread.Function
	}
	if state.Breakpoint != nil && state.Breakpoint.Tracepoint {
		var args []string
		for _, arg := range state.CurrentThread.Function.Args {
			args = append(args, arg.Value)
		}
		fmt.Printf("> %s(%s) %s:%d\n", fn.Name, strings.Join(args, ", "), shortenFilePath(state.CurrentThread.File), state.CurrentThread.Line)
	} else {
		fmt.Printf("> %s() %s:%d\n", fn.Name, shortenFilePath(state.CurrentThread.File), state.CurrentThread.Line)
	}

	if state.BreakpointInfo != nil {
		bpi := state.BreakpointInfo

		if bpi.Goroutine != nil {
			fmt.Printf("\tGoroutine %s\n", formatGoroutine(bpi.Goroutine))
		}

		ss := make([]string, len(bpi.Variables))
		for i, v := range bpi.Variables {
			ss[i] = fmt.Sprintf("%s: <%v>", v.Name, v.Value)
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
	return printfile(state.CurrentThread.File, state.CurrentThread.Line, true)
}

func printfile(filename string, line int, showArrow bool) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	var context []string
	buf := bufio.NewReader(file)
	l := line
	for i := 1; i < l-5; i++ {
		_, err := buf.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
	}

	s := l - 5
	if s < 1 {
		s = 1
	}

	for i := s; i <= l+5; i++ {
		line, err := buf.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				return err
			}

			if err == io.EOF {
				break
			}
		}

		var arrow string
		if showArrow {
			arrow = "  "
			if i == l {
				arrow = "=>"
			}
		}

		var lineNum string
		if i < 10 {
			lineNum = fmt.Sprintf("\033[34m%s  %d\033[0m:\t", arrow, i)
		} else {
			lineNum = fmt.Sprintf("\033[34m%s %d\033[0m:\t", arrow, i)
		}
		context = append(context, lineNum+line)
	}

	fmt.Println(strings.Join(context, ""))

	return nil
}

type ExitRequestError struct{}

func (ere ExitRequestError) Error() string {
	return ""
}

func exitCommand(client service.Client, args ...string) error {
	return ExitRequestError{}
}

func shortenFilePath(fullPath string) string {
	workingDir, _ := os.Getwd()
	return strings.Replace(fullPath, workingDir, ".", 1)
}
