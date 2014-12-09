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
	"github.com/derekparker/delve/goreadline"
	"github.com/derekparker/delve/proctl"
)

const version string = "0.2.0.beta"

type term struct {
	stdin *bufio.Reader
}

const historyFile string = ".dbg_history"

func init() {
	// We must ensure here that we are running on the same thread during
	// the execution of dbg. This is due to the fact that ptrace(2) expects
	// all commands after PTRACE_ATTACH to come from the same thread.
	runtime.LockOSThread()
}

func main() {
	var (
		pid    int
		run    bool
		printv bool
		err    error
		dbp    *proctl.DebuggedProcess
		t      = newTerm()
		cmds   = command.DebugCommands()
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

		dbp, err = proctl.Launch(append([]string{"./" + debugname}, flag.Args()...))
		if err != nil {
			die(1, "Could not launch program:", err)
		}
	case pid != 0:
		dbp, err = proctl.Attach(pid)
		if err != nil {
			die(1, "Could not attach to process:", err)
		}
	default:
		dbp, err = proctl.Launch(flag.Args())
		if err != nil {
			die(1, "Could not launch program:", err)
		}
	}

	goreadline.LoadHistoryFromFile(historyFile)
	fmt.Println("Type 'help' for list of commands.")

	for {
		cmdstr, err := t.promptForInput()
		if err != nil {
			if err == io.EOF {
				handleExit(t, dbp, 0)
			}
			die(1, "Prompt for input failed.\n")
		}

		cmdstr, args := parseCommand(cmdstr)

		if cmdstr == "exit" {
			handleExit(t, dbp, 0)
		}

		cmd := cmds.Find(cmdstr)
		err = cmd(dbp, args...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Command failed: %s\n", err)
		}
	}
}

func handleExit(t *term, dbp *proctl.DebuggedProcess, status int) {
	errno := goreadline.WriteHistoryToFile(historyFile)
	fmt.Println("readline:", errno)

	fmt.Println("Would you like to kill the process? [y/n]")
	answer, err := t.stdin.ReadString('\n')
	if err != nil {
		die(2, err.Error())
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

func newTerm() *term {
	return &term{
		stdin: bufio.NewReader(os.Stdin),
	}
}

func parseCommand(cmdstr string) (string, []string) {
	vals := strings.Split(cmdstr, " ")
	return vals[0], vals[1:]
}

func (t *term) promptForInput() (string, error) {
	prompt := "(dlv) "
	linep := goreadline.ReadLine(&prompt)
	if linep == nil {
		return "", io.EOF
	}
	line := strings.TrimSuffix(*linep, "\n")
	if line != "" {
		goreadline.AddHistory(line)
	}

	return line, nil
}
