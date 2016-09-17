package terminal

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"runtime"

	"syscall"

	"github.com/peterh/liner"

	"github.com/derekparker/delve/config"
	"github.com/derekparker/delve/service"
)

const (
	historyFile             string = ".dbg_history"
	terminalBlueEscapeCode  string = "\033[34m"
	terminalResetEscapeCode string = "\033[0m"
)

// Term represents the terminal running dlv.
type Term struct {
	client       service.Client
	prompt       string
	line         *liner.State
	cmds         *Commands
	dumb         bool
	stdout       io.Writer
	subPathRules config.SubstitutePathRules
	InitFile     string
}

// New returns a new Term.
func New(client service.Client, conf *config.Config) *Term {
	var substitutePathRules config.SubstitutePathRules
	cmds := DebugCommands(client)
	if conf != nil {
		substitutePathRules = conf.SubstitutePath
		if conf.Aliases != nil {
			cmds.Merge(conf.Aliases)
		}
	}

	var w io.Writer

	dumb := strings.ToLower(os.Getenv("TERM")) == "dumb"
	if dumb {
		w = os.Stdout
	} else {
		w = getColorableWriter()
	}

	return &Term{
		client:       client,
		prompt:       "(dlv) ",
		line:         liner.NewLiner(),
		cmds:         cmds,
		dumb:         dumb,
		stdout:       w,
		subPathRules: substitutePathRules,
	}
}

// Close returns the terminal to its previous mode.
func (t *Term) Close() {
	t.line.Close()
}

// Run begins running dlv in the terminal.
func (t *Term) Run() (int, error) {
	defer t.Close()

	// Send the debugger a halt command on SIGINT
	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGINT)
	go func() {
		for range ch {
			fmt.Printf("received SIGINT, stopping process (will not forward signal)")
			_, err := t.client.Halt()
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v", err)
			}
		}
	}()

	t.line.SetCompleter(func(line string) (c []string) {
		for _, cmd := range t.cmds.cmds {
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

	if t.InitFile != "" {
		err := t.cmds.executeFile(t, t.InitFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error executing init file: %s\n", err)
		}
	}

	for {
		cmdstr, err := t.promptForInput()
		if err != nil {
			if err == io.EOF {
				fmt.Println("exit")
				return t.handleExit()
			}
			return 1, fmt.Errorf("Prompt for input failed.\n")
		}

		cmdstr, args := parseCommand(cmdstr)
		if err := t.cmds.Call(cmdstr, args, t); err != nil {
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
}

// Println prints a line to the terminal.
func (t *Term) Println(prefix, str string) {
	if !t.dumb {
		prefix = fmt.Sprintf("%s%s%s", terminalBlueEscapeCode, prefix, terminalResetEscapeCode)
	}
	fmt.Fprintf(t.stdout, "%s%s\n", prefix, str)
}

// Substitues directory to source file.
//
// Ensures that only directory is substitued, for example:
// substitute from `/dir/subdir`, substitute to `/new`
// for file path `/dir/subdir/file` will return file path `/new/file`.
// for file path `/dir/subdir-2/file` substitution will not be applied.
//
// If more than one substitution rule is defined, the rules are applied
// in the order they are defined, first rule that matches is used for
// substitution.
func (t *Term) substitutePath(path string) string {
	path = crossPlatformPath(path)
	separator := string(os.PathSeparator)
	for _, r := range t.subPathRules {
		from := crossPlatformPath(r.From)
		to := r.To

		if !strings.HasSuffix(from, separator) {
			from = from + separator
		}
		if !strings.HasSuffix(to, separator) {
			to = to + separator
		}
		if strings.HasPrefix(path, from) {
			return strings.Replace(path, from, to, 1)
		}
	}
	return path
}

func crossPlatformPath(path string) string {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
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

func (t *Term) handleExit() (int, error) {
	fullHistoryFile, err := config.GetConfigFilePath(historyFile)
	if err != nil {
		fmt.Println("Error saving history file:", err)
	} else {
		if f, err := os.OpenFile(fullHistoryFile, os.O_RDWR, 0666); err == nil {
			_, err = t.line.WriteHistory(f)
			if err != nil {
				fmt.Println("readline history error:", err)
			}
			f.Close()
		}
	}

	s, err := t.client.GetState()
	if err != nil {
		return 1, err
	}
	if !s.Exited {
		kill := true
		if t.client.AttachedToExistingProcess() {
			answer, err := t.line.Prompt("Would you like to kill the process? [Y/n] ")
			if err != nil {
				return 2, io.EOF
			}
			answer = strings.ToLower(strings.TrimSpace(answer))
			kill = (answer != "n" && answer != "no")
		}
		if err := t.client.Detach(kill); err != nil {
			return 1, err
		}
	}
	return 0, nil
}

func parseCommand(cmdstr string) (string, string) {
	vals := strings.SplitN(cmdstr, " ", 2)
	if len(vals) == 1 {
		return vals[0], ""
	}
	return vals[0], strings.TrimSpace(vals[1])
}
