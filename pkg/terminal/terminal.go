package terminal

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync"

	"syscall"

	"github.com/peterh/liner"

	"github.com/go-delve/delve/pkg/config"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
)

const (
	historyFile                 string = ".dbg_history"
	terminalHighlightEscapeCode string = "\033[%2dm"
	terminalResetEscapeCode     string = "\033[0m"
)

const (
	ansiBlack     = 30
	ansiRed       = 31
	ansiGreen     = 32
	ansiYellow    = 33
	ansiBlue      = 34
	ansiMagenta   = 35
	ansiCyan      = 36
	ansiWhite     = 37
	ansiBrBlack   = 90
	ansiBrRed     = 91
	ansiBrGreen   = 92
	ansiBrYellow  = 93
	ansiBrBlue    = 94
	ansiBrMagenta = 95
	ansiBrCyan    = 96
	ansiBrWhite   = 97
)

// Term represents the terminal running dlv.
type Term struct {
	client   service.Client
	conf     *config.Config
	prompt   string
	line     *liner.State
	cmds     *Commands
	dumb     bool
	stdout   io.Writer
	InitFile string

	// quitContinue is set to true by exitCommand to signal that the process
	// should be resumed before quitting.
	quitContinue bool

	quittingMutex sync.Mutex
	quitting      bool
}

// New returns a new Term.
func New(client service.Client, conf *config.Config) *Term {
	cmds := DebugCommands(client)
	if conf != nil && conf.Aliases != nil {
		cmds.Merge(conf.Aliases)
	}

	if conf == nil {
		conf = &config.Config{}
	}

	var w io.Writer

	dumb := strings.ToLower(os.Getenv("TERM")) == "dumb"
	if dumb {
		w = os.Stdout
	} else {
		w = getColorableWriter()
	}

	if client != nil {
		client.SetReturnValuesLoadConfig(&LongLoadConfig)
	}

	if (conf.SourceListLineColor > ansiWhite &&
		conf.SourceListLineColor < ansiBrBlack) ||
		conf.SourceListLineColor < ansiBlack ||
		conf.SourceListLineColor > ansiBrWhite {
		conf.SourceListLineColor = ansiBlue
	}

	return &Term{
		client: client,
		conf:   conf,
		prompt: "(dlv) ",
		line:   liner.NewLiner(),
		cmds:   cmds,
		dumb:   dumb,
		stdout: w,
	}
}

// Close returns the terminal to its previous mode.
func (t *Term) Close() {
	t.line.Close()
}

func (t *Term) sigintGuard(ch <-chan os.Signal, multiClient bool) {
	for range ch {
		if multiClient {
			answer, err := t.line.Prompt("Would you like to [s]top the target or [q]uit this client, leaving the target running [s/q]? ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v", err)
				continue
			}
			answer = strings.TrimSpace(answer)
			switch answer {
			case "s":
				_, err := t.client.Halt()
				if err != nil {
					fmt.Fprintf(os.Stderr, "%v", err)
				}
			case "q":
				t.quittingMutex.Lock()
				t.quitting = true
				t.quittingMutex.Unlock()
				err := t.client.Disconnect(false)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%v", err)
				} else {
					t.Close()
				}
			default:
				fmt.Println("only s or q allowed")
			}

		} else {
			fmt.Printf("received SIGINT, stopping process (will not forward signal)\n")
			_, err := t.client.Halt()
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v", err)
			}
		}
	}
}

// Run begins running dlv in the terminal.
func (t *Term) Run() (int, error) {
	defer t.Close()

	multiClient := t.client.IsMulticlient()

	// Send the debugger a halt command on SIGINT
	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGINT)
	go t.sigintGuard(ch, multiClient)

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
			if _, ok := err.(ExitRequestError); ok {
				return t.handleExit()
			}
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

		if err := t.cmds.Call(cmdstr, t); err != nil {
			if _, ok := err.(ExitRequestError); ok {
				return t.handleExit()
			}
			// The type information gets lost in serialization / de-serialization,
			// so we do a string compare on the error message to see if the process
			// has exited, or if the command actually failed.
			if strings.Contains(err.Error(), "exited") {
				fmt.Fprintln(os.Stderr, err.Error())
			} else {
				t.quittingMutex.Lock()
				quitting := t.quitting
				t.quittingMutex.Unlock()
				if quitting {
					return t.handleExit()
				}
				fmt.Fprintf(os.Stderr, "Command failed: %s\n", err)
			}
		}
	}
}

// Println prints a line to the terminal.
func (t *Term) Println(prefix, str string) {
	if !t.dumb {
		terminalColorEscapeCode := fmt.Sprintf(terminalHighlightEscapeCode, t.conf.SourceListLineColor)
		prefix = fmt.Sprintf("%s%s%s", terminalColorEscapeCode, prefix, terminalResetEscapeCode)
	}
	fmt.Fprintf(t.stdout, "%s%s\n", prefix, str)
}

// Substitutes directory to source file.
//
// Ensures that only directory is substituted, for example:
// substitute from `/dir/subdir`, substitute to `/new`
// for file path `/dir/subdir/file` will return file path `/new/file`.
// for file path `/dir/subdir-2/file` substitution will not be applied.
//
// If more than one substitution rule is defined, the rules are applied
// in the order they are defined, first rule that matches is used for
// substitution.
func (t *Term) substitutePath(path string) string {
	path = crossPlatformPath(path)
	if t.conf == nil {
		return path
	}

	// On windows paths returned from headless server are as c:/dir/dir
	// though os.PathSeparator is '\\'

	separator := "/"                     //make it default
	if strings.Index(path, "\\") != -1 { //dependent on the path
		separator = "\\"
	}
	for _, r := range t.conf.SubstitutePath {
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
	if runtime.GOOS == "windows" {
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

func yesno(line *liner.State, question string) (bool, error) {
	for {
		answer, err := line.Prompt(question)
		if err != nil {
			return false, err
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		switch answer {
		case "n", "no":
			return false, nil
		case "y", "yes":
			return true, nil
		}
	}
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

	t.quittingMutex.Lock()
	quitting := t.quitting
	t.quittingMutex.Unlock()
	if quitting {
		return 0, nil
	}

	s, err := t.client.GetState()
	if err != nil {
		return 1, err
	}
	if !s.Exited {
		if t.quitContinue {
			err := t.client.Disconnect(true)
			if err != nil {
				return 2, err
			}
			return 0, nil
		}

		doDetach := true
		if t.client.IsMulticlient() {
			answer, err := yesno(t.line, "Would you like to kill the headless instance? [Y/n] ")
			if err != nil {
				return 2, io.EOF
			}
			doDetach = answer
		}

		if doDetach {
			kill := true
			if t.client.AttachedToExistingProcess() {
				answer, err := yesno(t.line, "Would you like to kill the process? [Y/n] ")
				if err != nil {
					return 2, io.EOF
				}
				kill = answer
			}
			if err := t.client.Detach(kill); err != nil {
				return 1, err
			}
		}
	}
	return 0, nil
}

// loadConfig returns an api.LoadConfig with the parameterss specified in
// the configuration file.
func (t *Term) loadConfig() api.LoadConfig {
	r := api.LoadConfig{true, 1, 64, 64, -1}

	if t.conf != nil && t.conf.MaxStringLen != nil {
		r.MaxStringLen = *t.conf.MaxStringLen
	}
	if t.conf != nil && t.conf.MaxArrayValues != nil {
		r.MaxArrayValues = *t.conf.MaxArrayValues
	}
	if t.conf != nil && t.conf.MaxVariableRecurse != nil {
		r.MaxVariableRecurse = *t.conf.MaxVariableRecurse
	}

	return r
}
