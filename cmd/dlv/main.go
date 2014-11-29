package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"runtime"
	"strings"
	"syscall"

	"github.com/derekparker/delve/command"
	"github.com/derekparker/delve/proctl"
	"golang.org/x/crypto/ssh/terminal"
)

const version string = "0.2.0.beta"

const historyFile string = ".dbg_history"

func init() {
	// We must ensure here that we are running on the same thread during
	// the execution of dbg. This is due to the fact that ptrace(2) expects
	// all commands after PTRACE_ATTACH to come from the same thread.
	runtime.LockOSThread()
}

func main() {
	var (
		pid     int
		run     bool
		printv  bool
		err     error
		dbgproc *proctl.DebuggedProcess
		cmds    = command.DebugCommands()
	)

	flag.IntVar(&pid, "pid", 0, "Pid of running process to attach to.")
	flag.BoolVar(&run, "run", false, "Compile program and begin debug session.")
	flag.BoolVar(&printv, "v", false, "Print version number and exit.")
	flag.Parse()

	if flag.NFlag() == 0 && len(flag.Args()) == 0 {
		flag.Usage()
		os.Exit(0)
	}

	if printv {
		fmt.Printf("Delve version: %s\n", version)
		os.Exit(0)
	}

	switch {
	case run:
		const debugname = "debug"
		cmd := exec.Command("go", "build", "-o", debugname, "-gcflags", "-N -l")
		err := cmd.Run()
		if err != nil {
			die(1, "Could not compile program:", err)
		}
		defer os.Remove(debugname)

		dbgproc, err = proctl.Launch(append([]string{"./" + debugname}, flag.Args()...))
		if err != nil {
			die(1, "Could not launch program:", err)
		}
	case pid != 0:
		dbgproc, err = proctl.Attach(pid)
		if err != nil {
			die(1, "Could not attach to process:", err)
		}
	default:
		dbgproc, err = proctl.Launch(flag.Args())
		if err != nil {
			die(1, "Could not launch program:", err)
		}
	}

	t := newTerm()
	if t.term != nil {
		completer := &autoCompletion{proc: dbgproc, cmds: command.DebugCommands()}
		t.term.AutoCompleteCallback = completer.complete
	}
	defer t.Restore()

	fmt.Println("Type 'help' for list of commands.")

	for {
		cmdstr, err := t.promptForInput()
		if err != nil {
			if err == io.EOF {
				handleExit(t, dbgproc, 0)
			}
			die(1, "Prompt for input failed.\n")
		}

		cmdstr, args := parseCommand(cmdstr)

		if cmdstr == "exit" {
			handleExit(t, dbgproc, 0)
		}

		cmd := cmds.Find(cmdstr)
		err = cmd(dbgproc, args...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Command failed: %s\n", err)
		}
	}
}
func handleExit(t *term, dbp *proctl.DebuggedProcess, status int) {
	t.Restore()
	fmt.Println("\nWould you like to kill the process? [y/n]")
	answer, err := t.stdin.ReadString('\n')
	if err != nil {
		die(2, err)
	}

	for pc := range dbp.BreakPoints {
		if _, err := dbp.Clear(pc); err != nil {
			fmt.Printf("Can't clear breakpoint @%x: %s\n", pc, err)
		}
	}

	fmt.Println("Detaching from process...")
	err = syscall.PtraceDetach(dbp.Process.Pid)
	if err != nil {
		die(2, "Could not detach", err)
	}

	if answer == "y\n" {
		fmt.Println("Killing process", dbp.Process.Pid)

		err := dbp.Process.Kill()
		if err != nil {
			fmt.Println("Could not kill process", err)
		}
	}

	die(status, "Hope I was of service hunting your bug!")
}

func die(status int, args ...interface{}) {
	fmt.Fprint(os.Stderr, args)
	fmt.Fprint(os.Stderr, "\n")
	os.Exit(status)
}

type term struct {
	stdin    *bufio.Reader
	term     *terminal.Terminal
	oldState *terminal.State
}

func newTerm() *term {
	t := term{stdin: bufio.NewReader(os.Stdin)}
	fd := int(os.Stdin.Fd())
	if terminal.IsTerminal(fd) {
		if state, err := terminal.MakeRaw(fd); err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing terminal: %s\n", err)
		} else {
			t.term = terminal.NewTerminal(os.Stdin, "dlv> ")
			t.oldState = state
		}
	} else {
		fmt.Fprintln(os.Stderr, "Warning stdin is not a terminal")
	}
	return &t
}

func parseCommand(cmdstr string) (string, []string) {
	vals := strings.Split(cmdstr, " ")
	return vals[0], vals[1:]
}

func (t *term) promptForInput() (string, error) {
	if t.term == nil {
		fmt.Print("dlv> ")
		line, err := t.stdin.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimSuffix(line, "\n"), nil
	} else {
		return t.term.ReadLine()
	}
}

func (t *term) Restore() {
	if t.term != nil {
		terminal.Restore(int(os.Stdin.Fd()), t.oldState)
	}
}

type autoCompletion struct {
	proc       *proctl.DebuggedProcess
	cmds       *command.Commands
	lastKey    rune
	candidates []string
	index      int
}

func (a *autoCompletion) complete(line string, pos int, key rune) (newLine string, newPos int, ok bool) {

	if key != '\t' {
		a.lastKey = key
		return "", -1, false
	}

	prefix := line[:pos]
	if a.lastKey != '\t' {
		// first completion with this prefix
		a.index = 0

		space := strings.Index(prefix, " ")
		if space == -1 {
			// complete command names
			a.candidates = a.cmds.FindPrefix(prefix)
		} else {
			var candidates []string
			cmd := prefix[:space]

			for space < len(prefix) && prefix[space] == ' ' {
				space += 1
			}
			arg := prefix[space:]

			switch cmd {
			case "clear":
				for _, bp := range a.proc.BreakPoints {
					if strings.HasPrefix(bp.FunctionName, arg) {
						candidates = append(candidates, cmd+" "+bp.FunctionName)
					}
				}
			case "break":
				for _, fn := range a.proc.GoSymTable.Funcs {
					if strings.Contains(fn.Sym.Name, arg) {
						candidates = append(candidates, cmd+" "+fn.Sym.Name)
					}
				}
			}

			a.candidates = candidates
		}
	} else {
		// cycle through candidates with old prefix
		a.index += 1
	}
	a.lastKey = key

	if len(a.candidates) == 0 {
		return "", -1, false
	}

	comp := a.candidates[a.index%len(a.candidates)]
	return comp + line[pos:], len(comp), true
}
