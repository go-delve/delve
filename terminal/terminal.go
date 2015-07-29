package terminal

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"path"
	"strings"

	"github.com/peterh/liner"
	sys "golang.org/x/sys/unix"

	"github.com/derekparker/delve/service"
)

const configDir string = ".dlv"
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

	err := createConfigPath()
	if err != nil {
		fmt.Printf("Could not create config directory: %v.")
	}

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
	fullHistoryFile, err := getConfigFilePath(historyFile)
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
				return handleExit(t.client, t)
			}
			err, status = fmt.Errorf("Prompt for input failed.\n"), 1
			break
		}

		cmdstr, args := parseCommand(cmdstr)
		if isExitCommand(cmdstr) {
			err, status = handleExit(t.client, t)
			break
		}

		cmd := cmds.Find(cmdstr)
		if err := cmd(t.client, args...); err != nil {
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

func handleExit(client service.Client, t *Term) (error, int) {
	fullHistoryFile, err := getConfigFilePath(historyFile)
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
	if client.AttachedToExistingProcess() {
		answer, err := t.line.Prompt("Would you like to kill the process? [Y/n] ")
		if err != nil {
			return io.EOF, 2
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		kill = (answer != "n" && answer != "no")
	}
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

func createConfigPath() error {
	path, err := getConfigFilePath("")
	if err != nil {
		return err
	}
	return os.MkdirAll(path, 0700)
}

func getConfigFilePath(file string) (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", err
	}
	return path.Join(usr.HomeDir, configDir, file), nil
}
