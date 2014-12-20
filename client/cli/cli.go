package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/derekparker/delve/command"
	"github.com/derekparker/delve/goreadline"
	"github.com/derekparker/delve/proctl"
)

const historyFile string = ".dbg_history"

func Run(run bool, pid int, args []string) {
	var (
		dbp *proctl.DebuggedProcess
		err error
	)

	switch {
	case run:
		const debugname = "debug"
		cmd := exec.Command("go", "build", "-o", debugname, "-gcflags", "-N -l")
		err := cmd.Run()
		if err != nil {
			die(1, "Could not compile program:", err)
		}
		defer os.Remove(debugname)

		dbp, err = proctl.Launch(append([]string{"./" + debugname}, args...))
		if err != nil {
			die(1, "Could not launch program:", err)
		}
	case pid != 0:
		dbp, err = proctl.Attach(pid)
		if err != nil {
			die(1, "Could not attach to process:", err)
		}
	default:
		dbp, err = proctl.Launch(args)
		if err != nil {
			die(1, "Could not launch program:", err)
		}
	}

	cmds := command.DebugCommands()
	goreadline.LoadHistoryFromFile(historyFile)
	fmt.Println("Type 'help' for list of commands.")

	for {
		cmdstr, err := promptForInput()
		if err != nil {
			if err == io.EOF {
				handleExit(dbp, 0)
			}
			die(1, "Prompt for input failed.\n")
		}

		cmdstr, args := parseCommand(cmdstr)

		if cmdstr == "exit" {
			handleExit(dbp, 0)
		}

		cmd := cmds.Find(cmdstr)
		err = cmd(dbp, args...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Command failed: %s\n", err)
		}
	}
}

func handleExit(dbp *proctl.DebuggedProcess, status int) {
	errno := goreadline.WriteHistoryToFile(historyFile)
	fmt.Println("readline:", errno)

	prompt := "Would you like to kill the process? [y/n]"
	answerp := goreadline.ReadLine(&prompt)
	if answerp == nil {
		die(2, io.EOF)
	}
	answer := strings.TrimSuffix(*answerp, "\n")

	for pc := range dbp.BreakPoints {
		if _, err := dbp.Clear(pc); err != nil {
			fmt.Printf("Can't clear breakpoint @%x: %s\n", pc, err)
		}
	}

	fmt.Println("Detaching from process...")
	err := syscall.PtraceDetach(dbp.Process.Pid)
	if err != nil {
		die(2, "Could not detach", err)
	}

	if answer == "y" {
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

func parseCommand(cmdstr string) (string, []string) {
	vals := strings.Split(cmdstr, " ")
	return vals[0], vals[1:]
}

func promptForInput() (string, error) {
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
