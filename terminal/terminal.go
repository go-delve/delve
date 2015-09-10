package terminal

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/peterh/liner"
	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/config"
	"github.com/derekparker/delve/service"
)

const historyFile string = ".dbg_history"

type Term struct {
	client service.Client
	prompt string
	line   *liner.State
	conf   *config.Config
}

func New(client service.Client, conf *config.Config) *Term {
	return &Term{
		prompt: "(dlv) ",
		line:   liner.NewLiner(),
		client: client,
		conf:   conf,
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
	if t.conf != nil && t.conf.Aliases != nil {
		cmds.Merge(t.conf.Aliases)
	}
	t.line.SetCompleter(func(line string) (c []string) {
		for _, cmd := range cmds.cmds {
			for _, alias := range cmd.aliases {
				if strings.HasPrefix(alias, strings.ToLower(line)) {
					c = append(c, alias)
				}
			}
		}
		return
	})

	fullHistoryFile, err := config.GetConfigFilePath(historyFile)
	if err != nil {
		fmt.Printf("Unable to load history file: %v.", err)
	}

	f, err := os.Open(fullHistoryFile)
	if err != nil {
		f, err = os.Create(fullHistoryFile)
		if err != nil {
			fmt.Printf("Unable to open history file: %v. History will not be saved for this session.", err)
		}
	}

	t.line.ReadHistory(f)
	f.Close()
	fmt.Println("Type 'help' for list of commands.")

	var status int
	for {
		cmdstr, err := t.promptForInput()
		if err != nil {
			if err == io.EOF {
				fmt.Println("exit")
				return t.handleExit()
			}
			err, status = fmt.Errorf("Prompt for input failed.\n"), 1
			break
		}

		cmdstr, args := parseCommand(cmdstr)
		cmd := cmds.Find(cmdstr)
		if err := cmd(t.client, args...); err != nil {
			if _, ok := err.(ExitRequestError); ok {
				return t.handleExit()
			}
			// The type information gets lost in serialization / de-serialization,
			// so we do a string compare on the error message to see if the process
			// has exited, or if the command actually failed.
			if strings.Contains(err.Error(), "exited") {
				fmt.Fprintln(os.Stderr, err.Error())
			} else {
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

func (t *Term) handleExit() (error, int) {
	fullHistoryFile, err := config.GetConfigFilePath(historyFile)
	if err != nil {
		fmt.Println("Error saving history file:", err)
	} else {
		if f, err := os.OpenFile(fullHistoryFile, os.O_RDWR, 0666); err == nil {
			_, err := t.line.WriteHistory(f)
			if err != nil {
				fmt.Println("readline history error: ", err)
			}
			f.Close()
		}
	}

	kill := true
	if t.client.AttachedToExistingProcess() {
		answer, err := t.line.Prompt("Would you like to kill the process? [Y/n] ")
		if err != nil {
			return io.EOF, 2
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		kill = (answer != "n" && answer != "no")
	}
	err = t.client.Detach(kill)
	if err != nil {
		return err, 1
	}
	return nil, 0
}

func parseCommand(cmdstr string) (string, []string) {
	vals := strings.Split(cmdstr, " ")
	return vals[0], vals[1:]
}
