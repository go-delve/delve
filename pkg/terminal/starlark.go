package terminal

import (
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

func (ctx starlarkContext) RegisterCommand(name, helpMsg string, fn func(args string) error) {
	cmdfn := func(t *Term, ctx callContext, args string) error {
		return fn(args)
	}

	found := false
	for i := range ctx.term.cmds.cmds {
		cmd := &ctx.term.cmds.cmds[i]
		for _, alias := range cmd.aliases {
			if alias == name {
				cmd.cmdFn = cmdfn
				cmd.helpMsg = helpMsg
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		newcmd := command{
			aliases: []string{name},
			helpMsg: helpMsg,
			cmdFn:   cmdfn,
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
