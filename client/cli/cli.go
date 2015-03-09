package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/command"
	"github.com/derekparker/delve/proctl"

	"github.com/peterh/liner"
)

const historyFile string = ".dbg_history"

func Run(args []string) {
	var (
		dbp  *proctl.DebuggedProcess
		err  error
		line = liner.NewLiner()
	)
	defer line.Close()

	switch args[0] {
	case "run":
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
	case "test":
		wd, err := os.Getwd()
		if err != nil {
			die(1, err)
		}
		base := filepath.Base(wd)
		cmd := exec.Command("go", "test", "-c", "-gcflags", "-N -l")
		err = cmd.Run()
		if err != nil {
			die(1, "Could not compile program:", err)
		}
		debugname := "./" + base + ".test"
		defer os.Remove(debugname)

		dbp, err = proctl.Launch(append([]string{debugname}, args...))
		if err != nil {
			die(1, "Could not launch program:", err)
		}
	case "attach":
		pid, err := strconv.Atoi(args[1])
		if err != nil {
			die(1, "Invalid pid", args[1])
		}
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
	f, err := os.Open(historyFile)
	if err != nil {
		f, _ = os.Create(historyFile)
	}
	line.ReadHistory(f)
	f.Close()
	fmt.Println("Type 'help' for list of commands.")

	for {
		cmdstr, err := promptForInput(line)
		if err != nil {
			if err == io.EOF {
				handleExit(dbp, line, 0)
			}
			die(1, "Prompt for input failed.\n")
		}

		cmdstr, args := parseCommand(cmdstr)

		if cmdstr == "exit" {
			handleExit(dbp, line, 0)
		}

		cmd := cmds.Find(cmdstr)
		if err := cmd(dbp, args...); err != nil {
			switch err.(type) {
			case proctl.ProcessExitedError:
				pe := err.(proctl.ProcessExitedError)
				fmt.Fprintf(os.Stderr, "Process exited with status %d\n", pe.Status)
			default:
				fmt.Fprintf(os.Stderr, "Command failed: %s\n", err)
			}
		}
	}
}

func handleExit(dbp *proctl.DebuggedProcess, line *liner.State, status int) {
	if f, err := os.OpenFile(historyFile, os.O_RDWR, 0666); err == nil {
		_, err := line.WriteHistory(f)
		if err != nil {
			fmt.Println("readline history error: ", err)
		}
		f.Close()
	}

	answer, err := line.Prompt("Would you like to kill the process? [y/n]")
	if err != nil {
		die(2, io.EOF)
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

func promptForInput(line *liner.State) (string, error) {
	l, err := line.Prompt("(dlv) ")
	if err != nil {
		return "", err
	}

	l = strings.TrimSuffix(l, "\n")
	if l != "" {
		line.AppendHistory(l)
	}

	return l, nil
}
