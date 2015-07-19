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

	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
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
		{aliases: []string{"break", "b"}, cmdFn: breakpoint, helpMsg: "break <address> [-stack <n>|-goroutine|<variable name>]*\nSet break point at the entry point of a function, or at a specific file/line.\nWhen the breakpoint is reached the value of the specified variables will be printed, if -stack is specified the stack trace of the current goroutine will be printed, if -goroutine is specified informations about the current goroutine will be printed. Example: break foo.go:13"},
		{aliases: []string{"trace", "t"}, cmdFn: tracepoint, helpMsg: "Set tracepoint, takes the same arguments as break"},
		{aliases: []string{"restart", "r"}, cmdFn: restart, helpMsg: "Restart process."},
		{aliases: []string{"continue", "c"}, cmdFn: cont, helpMsg: "Run until breakpoint or program termination."},
		{aliases: []string{"step", "si"}, cmdFn: step, helpMsg: "Single step through program."},
		{aliases: []string{"next", "n"}, cmdFn: next, helpMsg: "Step over to next source line."},
		{aliases: []string{"threads"}, cmdFn: threads, helpMsg: "Print out info for every traced thread."},
		{aliases: []string{"thread", "t"}, cmdFn: thread, helpMsg: "Switch to the specified thread."},
		{aliases: []string{"clear"}, cmdFn: clear, helpMsg: "Deletes breakpoint."},
		{aliases: []string{"clearall"}, cmdFn: clearAll, helpMsg: "Deletes all breakpoints."},
		{aliases: []string{"goroutines"}, cmdFn: goroutines, helpMsg: "Print out info for every goroutine."},
		{aliases: []string{"breakpoints", "bp"}, cmdFn: breakpoints, helpMsg: "Print out info for active breakpoints."},
		{aliases: []string{"print", "p"}, cmdFn: printVar, helpMsg: "Evaluate a variable."},
		{aliases: []string{"info"}, cmdFn: info, helpMsg: "Subcommands: args, funcs, locals, sources, vars, or regs."},
		{aliases: []string{"exit"}, cmdFn: nullCommand, helpMsg: "Exit the debugger."},
		{aliases: []string{"stack"}, cmdFn: stackCommand, helpMsg: "stack [<depth> [<goroutine id>]]. Prints stack."},
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
	for _, cmd := range c.cmds {
		fmt.Printf("\t%s - %s\n", strings.Join(cmd.aliases, "|"), cmd.helpMsg)
	}
	return nil
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
				prefix, th.ID, th.PC, th.File,
				th.Line, th.Function.Name)
		} else {
			fmt.Printf("%sThread %d at %s:%d\n", prefix, th.ID, th.File, th.Line)
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
	return fmt.Sprintf("%d - %s:%d %s (%#v)\n", g.ID, g.File, g.Line, fname, g.PC)
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
	fmt.Printf("Breakpoint %d cleared at %#v for %s %s:%d\n", bp.ID, bp.Addr, bp.FunctionName, bp.File, bp.Line)
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
			fmt.Printf("Couldn't delete breakpoint %d at %#v %s:%d: %s\n", bp.ID, bp.Addr, bp.File, bp.Line, err)
		}
		fmt.Printf("Breakpoint %d cleared at %#v for %s %s:%d\n", bp.ID, bp.Addr, bp.FunctionName, bp.File, bp.Line)
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
		fmt.Printf("%s %d at %#v %s:%d\n", thing, bp.ID, bp.Addr, bp.File, bp.Line)

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
	tokens := strings.Split(args[0], ":")
	switch {
	case len(tokens) == 1:
		requestedBp.FunctionName = args[0]
	case len(tokens) == 2:
		file := tokens[0]
		line, err := strconv.Atoi(tokens[1])
		if err != nil {
			return err
		}
		requestedBp.File = file
		requestedBp.Line = line
	default:
		return fmt.Errorf("invalid line reference")
	}

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

	bp, err := client.CreateBreakpoint(requestedBp)
	if err != nil {
		return err
	}

	thing := "Breakpoint"
	if tracepoint {
		thing = "Tracepoint"
	}

	fmt.Printf("%s %d set at %#v for %s %s:%d\n", thing, bp.ID, bp.Addr, bp.FunctionName, bp.File, bp.Line)
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

func filterVariables(vars []api.Variable, filter *regexp.Regexp) []string {
	data := make([]string, 0, len(vars))
	for _, v := range vars {
		if filter == nil || filter.Match([]byte(v.Name)) {
			data = append(data, fmt.Sprintf("%s = %s", v.Name, v.Value))
		}
	}
	return data
}

func info(client service.Client, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("not enough arguments. expected info type [regex].")
	}

	// Allow for optional regex
	var filter *regexp.Regexp
	if len(args) >= 2 {
		var err error
		if filter, err = regexp.Compile(args[1]); err != nil {
			return fmt.Errorf("invalid filter argument: %s", err.Error())
		}
	}

	var data []string

	switch args[0] {
	case "sources":
		regex := ""
		if len(args) >= 2 && len(args[1]) > 0 {
			regex = args[1]
		}
		sources, err := client.ListSources(regex)
		if err != nil {
			return err
		}
		data = sources

	case "funcs":
		regex := ""
		if len(args) >= 2 && len(args[1]) > 0 {
			regex = args[1]
		}
		funcs, err := client.ListFunctions(regex)
		if err != nil {
			return err
		}
		data = funcs

	case "regs":
		regs, err := client.ListRegisters()
		if err != nil {
			return err
		}
		data = append(data, regs)

	case "args":
		args, err := client.ListFunctionArgs()
		if err != nil {
			return err
		}
		data = filterVariables(args, filter)

	case "locals":
		locals, err := client.ListLocalVariables()
		if err != nil {
			return err
		}
		data = filterVariables(locals, filter)

	case "vars":
		regex := ""
		if len(args) >= 2 && len(args[1]) > 0 {
			regex = args[1]
		}
		vars, err := client.ListPackageVariables(regex)
		if err != nil {
			return err
		}
		data = filterVariables(vars, filter)

	default:
		return fmt.Errorf("unsupported info type, must be args, funcs, locals, sources or vars")
	}

	// sort and output data
	sort.Sort(sort.StringSlice(data))

	for _, d := range data {
		fmt.Println(d)
	}
	return nil
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

func printStack(stack []api.Location, ind string) {
	for i := range stack {
		name := "(nil)"
		if stack[i].Function != nil {
			name = stack[i].Function.Name
		}
		fmt.Printf("%s%d. %s %s:%d (%#v)\n", ind, i, name, stack[i].File, stack[i].Line, stack[i].PC)
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

	var context []string

	var fn *api.Function
	if state.CurrentThread.Function != nil {
		fn = state.CurrentThread.Function
	}
	if state.Breakpoint != nil && state.Breakpoint.Tracepoint {
		var args []string
		for _, arg := range state.CurrentThread.Function.Args {
			args = append(args, arg.Value)
		}
		fmt.Printf("> %s(%s) %s:%d\n", fn.Name, strings.Join(args, ", "), state.CurrentThread.File, state.CurrentThread.Line)
	} else {
		fmt.Printf("> %s() %s:%d\n", fn.Name, state.CurrentThread.File, state.CurrentThread.Line)
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

	file, err := os.Open(state.CurrentThread.File)
	if err != nil {
		return err
	}
	defer file.Close()

	buf := bufio.NewReader(file)
	l := state.CurrentThread.Line
	for i := 1; i < l-5; i++ {
		_, err := buf.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
	}

	for i := l - 5; i <= l+5; i++ {
		line, err := buf.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				return err
			}

			if err == io.EOF {
				break
			}
		}

		arrow := "  "
		if i == l {
			arrow = "=>"
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
