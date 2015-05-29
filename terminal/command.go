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
		{aliases: []string{"break", "b"}, cmdFn: breakpoint, helpMsg: "Set break point at the entry point of a function, or at a specific file/line. Example: break foo.go:13"},
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
		{aliases: []string{"info"}, cmdFn: info, helpMsg: "Provides info about args, funcs, locals, sources, or vars."},
		{aliases: []string{"exit"}, cmdFn: nullCommand, helpMsg: "Exit the debugger."},
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
// If it cannot find the command it will defualt to noCmdAvailable().
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
		var fname string
		if g.Function != nil {
			fname = g.Function.Name
		}
		fmt.Printf("Goroutine %d - %s:%d %s\n", g.ID, g.File, g.Line, fname)
	}
	return nil
}

func cont(client service.Client, args ...string) error {
	state, err := client.Continue()
	if err != nil {
		return err
	}
	printcontext(state)
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

	bp, err := client.ClearBreakPoint(id)
	if err != nil {
		return err
	}
	fmt.Printf("Breakpoint %d cleared at %#v for %s %s:%d\n", bp.ID, bp.Addr, bp.FunctionName, bp.File, bp.Line)
	return nil
}

func clearAll(client service.Client, args ...string) error {
	breakPoints, err := client.ListBreakPoints()
	if err != nil {
		return err
	}
	for _, bp := range breakPoints {
		_, err := client.ClearBreakPoint(bp.ID)
		if err != nil {
			fmt.Printf("Couldn't delete breakpoint %d at %#v %s:%d: %s\n", bp.ID, bp.Addr, bp.File, bp.Line, err)
		}
		fmt.Printf("Breakpoint %d cleared at %#v for %s %s:%d\n", bp.ID, bp.Addr, bp.FunctionName, bp.File, bp.Line)
	}
	return nil
}

type ById []*api.BreakPoint

func (a ById) Len() int           { return len(a) }
func (a ById) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ById) Less(i, j int) bool { return a[i].ID < a[j].ID }

func breakpoints(client service.Client, args ...string) error {
	breakPoints, err := client.ListBreakPoints()
	if err != nil {
		return err
	}
	sort.Sort(ById(breakPoints))
	for _, bp := range breakPoints {
		fmt.Printf("Breakpoint %d at %#v %s:%d\n", bp.ID, bp.Addr, bp.File, bp.Line)
	}

	return nil
}

func breakpoint(client service.Client, args ...string) error {
	if len(args) != 1 {
		return fmt.Errorf("argument must be either a function name or <file:line>")
	}
	requestedBp := &api.BreakPoint{}
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

	bp, err := client.CreateBreakPoint(requestedBp)
	if err != nil {
		return err
	}

	fmt.Printf("Breakpoint %d set at %#v for %s %s:%d\n", bp.ID, bp.Addr, bp.FunctionName, bp.File, bp.Line)
	return nil
}

func printVar(client service.Client, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("not enough arguments")
	}

	val, err := client.EvalSymbol(args[0])
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
		return fmt.Errorf("unsupported info type, must be args, funcs, locals, sources, or vars")
	}

	// sort and output data
	sort.Sort(sort.StringSlice(data))

	for _, d := range data {
		fmt.Println(d)
	}
	return nil
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

	fn := ""
	if state.CurrentThread.Function != nil {
		fn = state.CurrentThread.Function.Name
	}
	fmt.Printf("current loc: %s %s:%d\n", fn, state.CurrentThread.File, state.CurrentThread.Line)

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

		context = append(context, fmt.Sprintf("\033[34m%s %d\033[0m: %s", arrow, i, line))
	}

	fmt.Println(strings.Join(context, ""))

	return nil
}
