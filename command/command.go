// Package command implements functions for responding to user
// input and dispatching to appropriate backend commands.
package command

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/derekparker/delve/proctl"
)

type cmdfunc func(proc *proctl.DebuggedProcess, args ...string) error

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
}

// Returns the list of available command names
func (c *Commands) Names() (names []string) {
	for _, cc := range c.cmds {
		names = append(names, cc.aliases[0])
	}

	return
}

// Returns a Commands struct with default commands defined.
func DebugCommands() *Commands {
	c := &Commands{}

	c.cmds = []command{
		command{aliases: []string{"help"}, cmdFn: c.help, helpMsg: "Prints the help message."},
		command{aliases: []string{"break", "b"}, cmdFn: breakpoint, helpMsg: "Set break point at the entry point of a function, or at a specific file/line. Example: break foo.go:13"},
		command{aliases: []string{"continue", "c"}, cmdFn: cont, helpMsg: "Run until breakpoint or program termination."},
		command{aliases: []string{"step", "si"}, cmdFn: step, helpMsg: "Single step through program."},
		command{aliases: []string{"next", "n"}, cmdFn: next, helpMsg: "Step over to next source line."},
		command{aliases: []string{"threads"}, cmdFn: threads, helpMsg: "Print out info for every traced thread."},
		command{aliases: []string{"thread", "t"}, cmdFn: thread, helpMsg: "Switch to the specified thread."},
		command{aliases: []string{"clear"}, cmdFn: clear, helpMsg: "Deletes breakpoint."},
		command{aliases: []string{"goroutines"}, cmdFn: goroutines, helpMsg: "Print out info for every goroutine."},
		command{aliases: []string{"breakpoints", "bp"}, cmdFn: breakpoints, helpMsg: "Print out info for active breakpoints."},
		command{aliases: []string{"print", "p"}, cmdFn: printVar, helpMsg: "Evaluate a variable."},
		command{aliases: []string{"info"}, cmdFn: info, helpMsg: "Provides info about args, funcs, locals, sources, or vars."},
		command{aliases: []string{"exit"}, cmdFn: nullCommand, helpMsg: "Exit the debugger."},
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
	return func(p *proctl.DebuggedProcess, args ...string) error {
		return fn()
	}
}

func noCmdAvailable(p *proctl.DebuggedProcess, ars ...string) error {
	return fmt.Errorf("command not available")
}

func nullCommand(p *proctl.DebuggedProcess, ars ...string) error {
	return nil
}

func (c *Commands) help(p *proctl.DebuggedProcess, ars ...string) error {
	fmt.Println("The following commands are available:")
	for _, cmd := range c.cmds {
		fmt.Printf("\t%s - %s\n", strings.Join(cmd.aliases, "|"), cmd.helpMsg)
	}
	return nil
}

func threads(p *proctl.DebuggedProcess, ars ...string) error {
	for _, th := range p.Threads {
		prefix := "  "
		if th == p.CurrentThread {
			prefix = "* "
		}
		pc, err := th.CurrentPC()
		if err != nil {
			return err
		}
		f, l, fn := th.Process.GoSymTable.PCToLine(pc)
		if fn != nil {
			fmt.Printf("%sThread %d at %#v %s:%d %s\n", prefix, th.Id, pc, f, l, fn.Name)
		} else {
			fmt.Printf("%sThread %d at %#v\n", prefix, th.Id, pc)
		}
	}
	return nil
}

func thread(p *proctl.DebuggedProcess, ars ...string) error {
	oldTid := p.CurrentThread.Id
	tid, err := strconv.Atoi(ars[0])
	if err != nil {
		return err
	}

	err = p.SwitchThread(tid)
	if err != nil {
		return err
	}

	fmt.Printf("Switched from %d to %d\n", oldTid, tid)
	return nil
}

func goroutines(p *proctl.DebuggedProcess, ars ...string) error {
	return p.PrintGoroutinesInfo()
}

func cont(p *proctl.DebuggedProcess, ars ...string) error {
	err := p.Continue()
	if err != nil {
		return err
	}

	return printcontext(p)
}

func step(p *proctl.DebuggedProcess, args ...string) error {
	err := p.Step()
	if err != nil {
		return err
	}

	return printcontext(p)
}

func next(p *proctl.DebuggedProcess, args ...string) error {
	err := p.Next()
	if err != nil {
		return err
	}

	return printcontext(p)
}

func clear(p *proctl.DebuggedProcess, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("not enough arguments")
	}

	bp, err := p.ClearByLocation(args[0])
	if err != nil {
		return err
	}

	fmt.Printf("Breakpoint %d cleared at %#v for %s %s:%d\n", bp.ID, bp.Addr, bp.FunctionName, bp.File, bp.Line)

	return nil
}

type ById []*proctl.BreakPoint

func (a ById) Len() int           { return len(a) }
func (a ById) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ById) Less(i, j int) bool { return a[i].ID < a[j].ID }

func breakpoints(p *proctl.DebuggedProcess, args ...string) error {
	bps := make([]*proctl.BreakPoint, 0, len(p.BreakPoints)+4)

	for _, bp := range p.HWBreakPoints {
		if bp == nil {
			continue
		}
		bps = append(bps, bp)
	}

	for _, bp := range p.BreakPoints {
		if bp.Temp {
			continue
		}
		bps = append(bps, bp)
	}

	sort.Sort(ById(bps))
	for _, bp := range bps {
		fmt.Println(bp)
	}

	return nil
}

func breakpoint(p *proctl.DebuggedProcess, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("not enough arguments")
	}

	bp, err := p.BreakByLocation(args[0])
	if err != nil {
		return err
	}

	fmt.Printf("Breakpoint %d set at %#v for %s %s:%d\n", bp.ID, bp.Addr, bp.FunctionName, bp.File, bp.Line)

	return nil
}

func printVar(p *proctl.DebuggedProcess, args ...string) error {
	if len(args) == 0 {
		return fmt.Errorf("not enough arguments")
	}

	val, err := p.EvalSymbol(args[0])
	if err != nil {
		return err
	}

	fmt.Println(val.Value)
	return nil
}

func filterVariables(vars []*proctl.Variable, filter *regexp.Regexp) []string {
	data := make([]string, 0, len(vars))
	for _, v := range vars {
		if v == nil {
			continue
		}
		if filter == nil || filter.Match([]byte(v.Name)) {
			data = append(data, fmt.Sprintf("%s = %s", v.Name, v.Value))
		}
	}
	return data
}

func info(p *proctl.DebuggedProcess, args ...string) error {
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
		data = make([]string, 0, len(p.GoSymTable.Files))
		for f := range p.GoSymTable.Files {
			if filter == nil || filter.Match([]byte(f)) {
				data = append(data, f)
			}
		}

	case "funcs":
		data = make([]string, 0, len(p.GoSymTable.Funcs))
		for _, f := range p.GoSymTable.Funcs {
			if f.Sym != nil && (filter == nil || filter.Match([]byte(f.Name))) {
				data = append(data, f.Name)
			}
		}

	case "args":
		vars, err := p.CurrentThread.FunctionArguments()
		if err != nil {
			return nil
		}
		data = filterVariables(vars, filter)

	case "locals":
		vars, err := p.CurrentThread.LocalVariables()
		if err != nil {
			return nil
		}
		data = filterVariables(vars, filter)

	case "vars":
		vars, err := p.CurrentThread.PackageVariables()
		if err != nil {
			return nil
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

func printcontext(p *proctl.DebuggedProcess) error {
	var context []string

	regs, err := p.Registers()
	if err != nil {
		return err
	}

	f, l, fn := p.GoSymTable.PCToLine(regs.PC())

	if fn != nil {
		fmt.Printf("current loc: %s %s:%d\n", fn.Name, f, l)
		file, err := os.Open(f)
		if err != nil {
			return err
		}
		defer file.Close()

		buf := bufio.NewReader(file)
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
	} else {
		fmt.Printf("Stopped at: 0x%x\n", regs.PC())
		context = append(context, "\033[34m=>\033[0m    no source available")
	}

	fmt.Println(strings.Join(context, ""))

	return nil
}
