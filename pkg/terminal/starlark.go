package terminal

import (
	"slices"

	"github.com/go-delve/delve/pkg/terminal/starbind"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
)

type starlarkContext struct {
	term *Term
}

var _ starbind.Context = starlarkContext{}

func (ctx starlarkContext) Client() service.Client {
	return ctx.term.client
}

func (ctx starlarkContext) RegisterCommand(name, helpMsg string, fn func(args string) error, allowedPrefixes int) {
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
	for i := range ctx.term.cmds.cmds {
		cmd := &ctx.term.cmds.cmds[i]
		if slices.Contains(cmd.aliases, name) {
			cmd.cmdFn = cmdfn
			cmd.helpMsg = helpMsg
			cmd.allowedPrefixes = cmdPrefix(allowedPrefixes)
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
			allowedPrefixes: cmdPrefix(allowedPrefixes),
		}
		ctx.term.cmds.cmds = append(ctx.term.cmds.cmds, newcmd)
	}
}

func (ctx starlarkContext) CallCommand(cmdstr string) error {
	return ctx.term.cmds.Call(cmdstr, ctx.term)
}

func (ctx starlarkContext) Scope() api.EvalScope {
	return api.EvalScope{GoroutineID: -1, Frame: ctx.term.cmds.frame}
}

func (ctx starlarkContext) LoadConfig() api.LoadConfig {
	return ctx.term.loadConfig()
}
