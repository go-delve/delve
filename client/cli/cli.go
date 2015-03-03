package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/command"
	"github.com/derekparker/delve/proctl"

	"github.com/peterh/liner"
)

const historyFile string = ".dbg_history"

func Run(run bool, pid int, args []string) {
	var (
		dbp *proctl.DebuggedProcess
		err error
		t   = &Terminator{line: liner.NewLiner()}
	)
	defer t.line.Close()

	switch {
	case run:
		const debugname = "debug"
		cmd := exec.Command("go", "build", "-o", debugname, "-gcflags", "-N -l")
		err := cmd.Run()
		if err != nil {
			t.die(1, "Could not compile program:", err)
		}
		defer os.Remove(debugname)

		dbp, err = proctl.Launch(append([]string{"./" + debugname}, args...))
		if err != nil {
			t.die(1, "Could not launch program:", err)
		}
	case pid != 0:
		dbp, err = proctl.Attach(pid)
		if err != nil {
			t.die(1, "Could not attach to process:", err)
		}
	default:
		dbp, err = proctl.Launch(args)
		if err != nil {
			t.die(1, "Could not launch program:", err)
		}
	}

	ch := make(chan os.Signal)
	signal.Notify(ch, sys.SIGINT)
	go func() {
		for _ = range ch {
			if dbp.Running() {
				dbp.RequestManualStop()
			}
		}
	}()

	cmds := command.DebugCommands()
	names := cmds.Names()

	// set command completer
	t.line.SetWordCompleter(func(line string, pos int) (head string, completions []string, tail string) {
		start := strings.LastIndex(line[:pos], " ")
		match := ""
		if start < 0 { // this is the command to match
			head = ""
			tail = line[pos:]
			match = line
		} else if strings.HasPrefix(line, "help ") {
			head = line[:start+1]
			match = line[start+1:]
			tail = line[pos:]
		} else {
			return
		}
		for _, n := range names {
			if strings.HasPrefix(n, strings.ToLower(match)) {
				completions = append(completions, n)
			}
		}
		return
	})

	f, err := os.Open(historyFile)
	if err != nil {
		f, _ = os.Create(historyFile)
	}
	t.line.ReadHistory(f)
	f.Close()
	fmt.Println("Type 'help' for list of commands.")

	for {
		cmdstr, err := promptForInput(t)
		if err != nil {
			if err == io.EOF {
				handleExit(dbp, t, 0)
			}
			t.die(1, "Prompt for input failed.\n")
		}

		cmdstr, args := parseCommand(cmdstr)

		if cmdstr == "exit" {
			handleExit(dbp, t, 0)
		}

		cmd := cmds.Find(cmdstr)
		err = cmd(dbp, args...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Command failed: %s\n", err)
		}
	}
}

func handleExit(dbp *proctl.DebuggedProcess, t *Terminator, status int) {
	if f, err := os.OpenFile(historyFile, os.O_RDWR, 0666); err == nil {
		_, err := t.line.WriteHistory(f)
		if err != nil {
			fmt.Println("readline history error: ", err)
		}
		f.Close()
	}

	answer, err := t.line.Prompt("Would you like to kill the process? [y/n]")
	if err != nil {
		t.die(2, io.EOF)
	}
	answer = strings.TrimSuffix(answer, "\n")

	for _, bp := range dbp.HWBreakPoints {
		if bp == nil {
			continue
		}
		if _, err := dbp.Clear(bp.Addr); err != nil {
			fmt.Printf("Can't clear breakpoint @%x: %s\n", bp.Addr, err)
		}
	}

	for pc := range dbp.BreakPoints {
		if _, err := dbp.Clear(pc); err != nil {
			fmt.Printf("Can't clear breakpoint @%x: %s\n", pc, err)
		}
	}

	fmt.Println("Detaching from process...")
	err = sys.PtraceDetach(dbp.Process.Pid)
	if err != nil {
		t.die(2, "Could not detach", err)
	}

	if answer == "y" {
		fmt.Println("Killing process", dbp.Process.Pid)

		err := dbp.Process.Kill()
		if err != nil {
			fmt.Println("Could not kill process", err)
		}
	}

	t.die(status, "Hope I was of service hunting your bug!")
}

type Terminator struct {
	line *liner.State
}

func (t *Terminator) die(status int, args ...interface{}) {
	if t.line != nil {
		t.line.Close()
	}

	fmt.Fprint(os.Stderr, args)
	fmt.Fprint(os.Stderr, "\n")
	os.Exit(status)
}

func parseCommand(cmdstr string) (string, []string) {
	vals := strings.Split(cmdstr, " ")
	return vals[0], vals[1:]
}

func promptForInput(t *Terminator) (string, error) {
	l, err := t.line.Prompt("(dlv) ")
	if err != nil {
		return "", err
	}

	l = strings.TrimSuffix(l, "\n")
	if l != "" {
		t.line.AppendHistory(l)
	}

	return l, nil
}
