package terminal

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/go-delve/delve/service/api"

	"github.com/go-delve/liner"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

func starlarkSource(t *Term, c *Commands, path string) error {
	buf, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	threadID, _, err := starlarkEvalOne(0, t, c, path, string(buf), api.EvalStarlarkReset)
	t.client.EvalStarlarkCancel(threadID, false)
	return err
}

func starlarkEvalOne(threadID uint64, t *Term, c *Commands, path, source string, flags api.EvalStarlarkFlags) (uint64, bool, error) {
	out, errFromStarlark := t.client.EvalStarlark(threadID, api.EvalScope{GoroutineID: -1, Frame: c.frame}, t.loadConfig(), path, source, flags)
	for out.Kind != api.StarlarkDone {
		var (
			value         string
			errToStarlark error
		)
		threadID = out.ThreadID
		switch out.Kind {
		case api.StarlarkDlvCommand:
			errToStarlark = t.cmds.Call(out.Args[0], t)
		case api.StarlarkWriteFile:
			errToStarlark = os.WriteFile(out.Args[0], []byte(out.Args[1]), 0o640)
		case api.StarlarkAppendFile:
			var f *os.File
			f, errToStarlark = os.OpenFile(string(out.Args[0]), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
			if errToStarlark == nil {
				_, errToStarlark = f.Write([]byte(out.Args[1]))
				f.Close()
			}
		case api.StarlarkReadFile:
			var buf []byte
			buf, errToStarlark = os.ReadFile(out.Args[0])
			value = string(buf)
		case api.StarlarkPrint:
			fmt.Fprintln(t.stdout, out.Output)
		case api.StarlarkRegisterCommand:
			cmdName := out.Args[0]
			help := out.Args[1]
			registerCommand(t, cmdName, help, func(args string) error {
				threadID, _, err := starlarkEvalOne(0, t, c, api.StarlarkStdin, cmdName+" "+args, api.EvalStarlarkCallCommand|api.EvalStarlarkReset)
				t.client.EvalStarlarkCancel(threadID, false)
				return err
			})
		default:
			errToStarlark = fmt.Errorf("unknown starlark response kind: %v", out.Kind)
		}
		out, errFromStarlark = t.client.EvalStarlarkContinue(threadID, value, api.EvalScope{GoroutineID: -1, Frame: c.frame}, t.loadConfig(), errToStarlark)
	}
	if out.Output != "" {
		fmt.Fprintln(t.stdout, out.Output)
	}
	return out.ThreadID, out.IsCancelled, errFromStarlark
}

func starlarkRepl(t *Term, c *Commands) error {
	rl := liner.NewLiner()
	defer rl.Close()
	threadID, _, err := starlarkEvalOne(0, t, c, api.StarlarkStdin, "", api.EvalStarlarkReset)
	if err != nil {
		return err
	}
	for {
		isCancelled, err := rep(t, c, threadID, rl)
		if isCancelled {
			break
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintln(t.stdout, err)
		}
	}
	fmt.Fprintln(t.stdout)
	t.client.EvalStarlarkCancel(threadID, false)
	return nil
}

const (
	starlarkNormalPrompt = ">>> "
	starlarkExtraPrompt  = "... "

	starlarkExitCommand = "exit"
)

func rep(t *Term, c *Commands, threadID uint64, rl *liner.State) (bool, error) {
	defer t.stdout.Flush()
	eof := false

	lines := []string{}
	prompt := starlarkNormalPrompt
	readline := func() ([]byte, error) {
		line, err := rl.Prompt(prompt)
		t.stdout.Echo(prompt + line)
		if line == starlarkExitCommand {
			eof = true
			return nil, io.EOF
		}
		lines = append(lines, line)
		rl.AppendHistory(line)
		prompt = starlarkExtraPrompt
		if err != nil {
			if err == io.EOF {
				eof = true
			}
			return nil, err
		}
		return []byte(line + "\n"), nil
	}

	// read lines until we have a complete statement
	_, err := syntax.ParseCompoundStmt(api.StarlarkStdin, readline)
	if err != nil {
		if eof {
			return false, io.EOF
		}
		if evalErr, ok := err.(*starlark.EvalError); ok {
			fmt.Fprintln(t.stdout, evalErr.Backtrace())
		} else {
			fmt.Fprintln(t.stdout, err)
		}
		return false, nil
	}

	_, isCancelled, err := starlarkEvalOne(threadID, t, c, api.StarlarkStdin, strings.Join(lines, "\n"), 0)
	return isCancelled, err
}

func registerCommand(term *Term, name, helpMsg string, fn func(args string) error) {
	cmdfn := func(t *Term, callCtx callContext, args string) error {
		// If called with onPrefix, add the full command (name + args) to the breakpoint's CustomCommands
		if callCtx.Prefix == onPrefix {
			if callCtx.Breakpoint == nil {
				return nil
			}
			fullCmd := name
			if args != "" {
				fullCmd = name + " " + args
			}
			callCtx.Breakpoint.CustomCommands = append(callCtx.Breakpoint.CustomCommands, fullCmd)
			return nil
		}
		return fn(args)
	}

	found := false
	for i := range term.cmds.cmds {
		cmd := &term.cmds.cmds[i]
		if slices.Contains(cmd.aliases, name) {
			cmd.cmdFn = cmdfn
			cmd.helpMsg = helpMsg
			cmd.allowedPrefixes = onPrefix
			found = true
		}
		if found {
			break
		}
	}
	if !found {
		newcmd := command{
			aliases:         []string{name},
			helpMsg:         helpMsg,
			cmdFn:           cmdfn,
			allowedPrefixes: onPrefix,
		}
		term.cmds.cmds = append(term.cmds.cmds, newcmd)
	}
}
