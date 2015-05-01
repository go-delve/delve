package terminal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/peterh/liner"
	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/proctl"
	"github.com/derekparker/delve/service"
)

const historyFile string = ".dbg_history"

type Term struct {
	client service.Client
	prompt string
	line   *liner.State
}

func New(client service.Client) *Term {
	return &Term{
		prompt: "(dlv) ",
		line:   liner.NewLiner(),
		client: client,
	}
}

func (t *Term) Run() (error, int) {
	defer t.line.Close()

	// Send the debugger a halt command on SIGINT
	ch := make(chan os.Signal)
	signal.Notify(ch, sys.SIGINT)
	go func() {
		for range ch {
			_, err := t.client.Halt()
			if err != nil {
				fmt.Println(err)
			}
		}
	}()

	cmds := DebugCommands(t.client)
	f, err := os.Open(historyFile)
	if err != nil {
		f, _ = os.Create(historyFile)
	}
	t.line.ReadHistory(f)
	f.Close()
	fmt.Println("Type 'help' for list of commands.")

	var status int

	for {
		cmdstr, err := t.promptForInput()
		if err != nil {
			if err == io.EOF {
				err, status = handleExit(t.client, t)
			}
			err, status = errors.New("Prompt for input failed.\n"), 1
			break
		}

		cmdstr, args := parseCommand(cmdstr)
		if cmdstr == "exit" {
			err, status = handleExit(t.client, t)
			break
		}

		cmd := cmds.Find(cmdstr)
		if err := cmd(t.client, args...); err != nil {
			switch err.(type) {
			case proctl.ProcessExitedError:
				pe := err.(proctl.ProcessExitedError)
				fmt.Fprintf(os.Stderr, "Process exited with status %d\n", pe.Status)
			default:
				fmt.Fprintf(os.Stderr, "Command failed: %s\n", err)
			}
		}
	}

	return nil, status
}

func (t *Term) promptForInput() (string, error) {
	l, err := t.line.Prompt(t.prompt)
	if err != nil {
		return "", err
	}

	l = strings.TrimSuffix(l, "\n")
	if l != "" {
		t.line.AppendHistory(l)
	}

	return l, nil
}

func handleExit(client service.Client, t *Term) (error, int) {
	if f, err := os.OpenFile(historyFile, os.O_RDWR, 0666); err == nil {
		_, err := t.line.WriteHistory(f)
		if err != nil {
			fmt.Println("readline history error: ", err)
		}
		f.Close()
	}

	answer, err := t.line.Prompt("Would you like to kill the process? [y/n] ")
	if err != nil {
		return io.EOF, 2
	}
	answer = strings.TrimSuffix(answer, "\n")

	kill := (answer == "y")
	err = client.Detach(kill)
	if err != nil {
		return err, 1
	}
	return nil, 0
}

func parseCommand(cmdstr string) (string, []string) {
	vals := strings.Split(cmdstr, " ")
	return vals[0], vals[1:]
}
