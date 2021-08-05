package terminal

import (
	"fmt"
	"io"
	"net/rpc"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/derekparker/trie"
	"github.com/peterh/liner"

	"github.com/go-delve/delve/pkg/config"
	"github.com/go-delve/delve/pkg/locspec"
	"github.com/go-delve/delve/pkg/terminal/colorize"
	"github.com/go-delve/delve/pkg/terminal/starbind"
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
	client       service.Client
	conf         *config.Config
	prompt       string
	line         *liner.State
	cmds         *Commands
	stdout       io.Writer
	InitFile     string
	displays     []displayEntry
	colorEscapes map[colorize.Style]string

	historyFile *os.File

	starlarkEnv *starbind.Env

	substitutePathRulesCache [][2]string

	// quitContinue is set to true by exitCommand to signal that the process
	// should be resumed before quitting.
	quitContinue bool

	longCommandMu         sync.Mutex
	longCommandCancelFlag bool

	quittingMutex sync.Mutex
	quitting      bool
}

type displayEntry struct {
	expr   string
	fmtstr string
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

	t := &Term{
		client: client,
		conf:   conf,
		prompt: "(dlv) ",
		line:   liner.NewLiner(),
		cmds:   cmds,
		stdout: os.Stdout,
	}

	if strings.ToLower(os.Getenv("TERM")) != "dumb" {
		t.stdout = getColorableWriter()
		t.colorEscapes = make(map[colorize.Style]string)
		t.colorEscapes[colorize.NormalStyle] = terminalResetEscapeCode
		wd := func(s string, defaultCode int) string {
			if s == "" {
				return fmt.Sprintf(terminalHighlightEscapeCode, defaultCode)
			}
			return s
		}
		t.colorEscapes[colorize.KeywordStyle] = conf.SourceListKeywordColor
		t.colorEscapes[colorize.StringStyle] = wd(conf.SourceListStringColor, ansiBrGreen)
		t.colorEscapes[colorize.NumberStyle] = conf.SourceListNumberColor
		t.colorEscapes[colorize.CommentStyle] = wd(conf.SourceListCommentColor, ansiBrMagenta)
		t.colorEscapes[colorize.ArrowStyle] = wd(conf.SourceListArrowColor, ansiBrYellow)
		switch x := conf.SourceListLineColor.(type) {
		case string:
			t.colorEscapes[colorize.LineNoStyle] = x
		case int:
			if (x > ansiWhite && x < ansiBrBlack) || x < ansiBlack || x > ansiBrWhite {
				x = ansiBlue
			}
			t.colorEscapes[colorize.LineNoStyle] = fmt.Sprintf(terminalHighlightEscapeCode, x)
		case nil:
			t.colorEscapes[colorize.LineNoStyle] = fmt.Sprintf(terminalHighlightEscapeCode, ansiBlue)
		}
	}

	if client != nil {
		lcfg := t.loadConfig()
		client.SetReturnValuesLoadConfig(&lcfg)
	}

	t.starlarkEnv = starbind.New(starlarkContext{t})
	return t
}

// Close returns the terminal to its previous mode.
func (t *Term) Close() {
	t.line.Close()
}

func (t *Term) sigintGuard(ch <-chan os.Signal, multiClient bool) {
	for range ch {
		t.longCommandCancel()
		t.starlarkEnv.Cancel()
		state, err := t.client.GetStateNonBlocking()
		if err == nil && state.Recording {
			fmt.Printf("received SIGINT, stopping recording (will not forward signal)\n")
			err := t.client.StopRecording()
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
			continue
		}
		if err == nil && state.CoreDumping {
			fmt.Printf("received SIGINT, stopping dump\n")
			err := t.client.CoreDumpCancel()
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
			continue
		}
		if multiClient {
			answer, err := t.line.Prompt("Would you like to [p]ause the target (returning to Delve's prompt) or [q]uit this client (leaving the target running) [p/q]? ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v", err)
				continue
			}
			answer = strings.TrimSpace(answer)
			switch answer {
			case "p":
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
				fmt.Println("only p or q allowed")
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
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT)
	go t.sigintGuard(ch, multiClient)

	fns := trie.New()
	cmds := trie.New()
	funcs, _ := t.client.ListFunctions("")
	for _, fn := range funcs {
		fns.Add(fn, nil)
	}
	for _, cmd := range t.cmds.cmds {
		for _, alias := range cmd.aliases {
			cmds.Add(alias, nil)
		}
	}

	t.line.SetCompleter(func(line string) (c []string) {
		cmd := t.cmds.Find(strings.Split(line, " ")[0], noPrefix)
		switch cmd.aliases[0] {
		case "break", "trace", "continue":
			if spc := strings.LastIndex(line, " "); spc > 0 {
				prefix := line[:spc] + " "
				funcs := fns.FuzzySearch(line[spc+1:])
				for _, f := range funcs {
					c = append(c, prefix+f)
				}
			}
		case "nullcmd", "nocmd":
			commands := cmds.FuzzySearch(strings.ToLower(line))
			c = append(c, commands...)
		}
		return
	})

	fullHistoryFile, err := config.GetConfigFilePath(historyFile)
	if err != nil {
		fmt.Printf("Unable to load history file: %v.", err)
	}

	t.historyFile, err = os.OpenFile(fullHistoryFile, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		fmt.Printf("Unable to open history file: %v. History will not be saved for this session.", err)
	}
	if _, err := t.line.ReadHistory(t.historyFile); err != nil {
		fmt.Printf("Unable to read history file: %v", err)
	}

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

	var lastCmd string

	// Ensure that the target process is neither running nor recording by
	// making a blocking call.
	_, _ = t.client.GetState()

	for {
		cmdstr, err := t.promptForInput()
		if err != nil {
			if err == io.EOF {
				fmt.Println("exit")
				return t.handleExit()
			}
			return 1, fmt.Errorf("Prompt for input failed.\n")
		}

		if strings.TrimSpace(cmdstr) == "" {
			cmdstr = lastCmd
		}

		lastCmd = cmdstr

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
	if t.conf == nil {
		return path
	}
	return locspec.SubstitutePath(path, t.substitutePathRules())
}

func (t *Term) substitutePathRules() [][2]string {
	if t.substitutePathRulesCache != nil {
		return t.substitutePathRulesCache
	}
	if t.conf == nil || t.conf.SubstitutePath == nil {
		return nil
	}
	spr := make([][2]string, 0, len(t.conf.SubstitutePath))
	for _, r := range t.conf.SubstitutePath {
		spr = append(spr, [2]string{r.From, r.To})
	}
	t.substitutePathRulesCache = spr
	return spr
}

// formatPath applies path substitution rules and shortens the resulting
// path by replacing the current directory with './'
func (t *Term) formatPath(path string) string {
	path = t.substitutePath(path)
	workingDir, _ := os.Getwd()
	return strings.Replace(path, workingDir, ".", 1)
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
	if t.historyFile != nil {
		if _, err := t.line.WriteHistory(t.historyFile); err != nil {
			fmt.Println("readline history error:", err)
		}
		if err := t.historyFile.Close(); err != nil {
			fmt.Printf("error closing history file: %s\n", err)
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
		if isErrProcessExited(err) {
			if t.client.IsMulticlient() {
				answer, err := yesno(t.line, "Remote process has exited. Would you like to kill the headless instance? [Y/n] ")
				if err != nil {
					return 2, io.EOF
				}
				if answer {
					if err := t.client.Detach(true); err != nil {
						return 1, err
					}
				}
				return 0, err
			}
			return 0, nil
		}
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
	r := api.LoadConfig{FollowPointers: true, MaxVariableRecurse: 1, MaxStringLen: 64, MaxArrayValues: 64, MaxStructFields: -1}

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

func (t *Term) removeDisplay(n int) error {
	if n < 0 || n >= len(t.displays) {
		return fmt.Errorf("%d is out of range", n)
	}
	t.displays[n] = displayEntry{"", ""}
	for i := len(t.displays) - 1; i >= 0; i-- {
		if t.displays[i].expr != "" {
			t.displays = t.displays[:i+1]
			return nil
		}
	}
	t.displays = t.displays[:0]
	return nil
}

func (t *Term) addDisplay(expr, fmtstr string) {
	t.displays = append(t.displays, displayEntry{expr: expr, fmtstr: fmtstr})
}

func (t *Term) printDisplay(i int) {
	expr, fmtstr := t.displays[i].expr, t.displays[i].fmtstr
	val, err := t.client.EvalVariable(api.EvalScope{GoroutineID: -1}, expr, ShortLoadConfig)
	if err != nil {
		if isErrProcessExited(err) {
			return
		}
		fmt.Printf("%d: %s = error %v\n", i, expr, err)
		return
	}
	fmt.Printf("%d: %s = %s\n", i, val.Name, val.SinglelineStringFormatted(fmtstr))
}

func (t *Term) printDisplays() {
	for i := range t.displays {
		if t.displays[i].expr != "" {
			t.printDisplay(i)
		}
	}
}

func (t *Term) onStop() {
	t.printDisplays()
}

func (t *Term) longCommandCancel() {
	t.longCommandMu.Lock()
	defer t.longCommandMu.Unlock()
	t.longCommandCancelFlag = true
}

func (t *Term) longCommandStart() {
	t.longCommandMu.Lock()
	defer t.longCommandMu.Unlock()
	t.longCommandCancelFlag = false
}

func (t *Term) longCommandCanceled() bool {
	t.longCommandMu.Lock()
	defer t.longCommandMu.Unlock()
	return t.longCommandCancelFlag
}

// isErrProcessExited returns true if `err` is an RPC error equivalent of proc.ErrProcessExited
func isErrProcessExited(err error) bool {
	rpcError, ok := err.(rpc.ServerError)
	return ok && strings.Contains(rpcError.Error(), "has exited with status")
}
