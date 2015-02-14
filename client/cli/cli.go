package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"

	sys "golang.org/x/sys/unix"

	. "github.com/derekparker/delve/client/internal/common"
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
			Die(1, "Could not compile program:", err)
		}
		defer os.Remove(debugname)

		dbp, err = proctl.Launch(append([]string{"./" + debugname}, args...))
		if err != nil {
			Die(1, "Could not launch program:", err)
		}
	case pid != 0:
		dbp, err = proctl.Attach(pid)
		if err != nil {
			Die(1, "Could not attach to process:", err)
		}
	default:
		dbp, err = proctl.Launch(args)
		if err != nil {
			Die(1, "Could not launch program:", err)
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
	goreadline.LoadHistoryFromFile(historyFile)
	fmt.Println("Type 'help' for list of commands.")

	for {
		cmdstr, err := promptForInput()
		if err != nil {
			if err == io.EOF {
				handleExit(dbp, 0)
			}
			Die(1, "Prompt for input failed.\n")
		}

		cmdstr, args := ParseCommand(cmdstr)

		if cmdstr == "exit" {
			handleExit(dbp, 0)
		}

		cmd := cmds.Find(cmdstr)
		output := cmd(dbp, args...)
		if output.Err != nil {
			fmt.Fprintf(os.Stderr, "Command failed: %s\n", output.Err)
		} else {
			fmt.Fprintf(os.Stdout, output.Out)
		}
	}
}

func handleExit(dbp *proctl.DebuggedProcess, status int) {
	errno := goreadline.WriteHistoryToFile(historyFile)
	if errno != 0 {
		fmt.Println("readline:", errno)
	}

	prompt := "Would you like to kill the process? [y/n]"
	answerp := goreadline.ReadLine(&prompt)
	if answerp == nil {
		Die(2, io.EOF)
	}
	answer := strings.TrimSuffix(*answerp, "\n")

	fmt.Print(HandleExit(dbp, answer == "y"))

	Die(status, "Hope I was of service hunting your bug!")
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
