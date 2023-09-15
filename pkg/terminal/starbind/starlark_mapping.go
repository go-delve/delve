// DO NOT EDIT: auto-generated using _scripts/gen-starlark-bindings.go

package starbind

import (
	"fmt"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/rpc2"
	"go.starlark.net/starlark"
)

func (env *Env) starlarkPredeclare() (starlark.StringDict, map[string]string) {
	r := starlark.StringDict{}
	doc := make(map[string]string)

	r["amend_breakpoint"] = starlark.NewBuiltin("amend_breakpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.AmendBreakpointIn
		var rpcRet rpc2.AmendBreakpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Breakpoint, "Breakpoint")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Breakpoint":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Breakpoint, "Breakpoint")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("AmendBreakpoint", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["amend_breakpoint"] = "builtin amend_breakpoint(Breakpoint)\n\namend_breakpoint allows user to update an existing breakpoint\nfor example to change the information retrieved when the\nbreakpoint is hit or to change, add or remove the break condition.\n\narg.Breakpoint.ID must be a valid breakpoint ID"
	r["ancestors"] = starlark.NewBuiltin("ancestors", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.AncestorsIn
		var rpcRet rpc2.AncestorsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.GoroutineID, "GoroutineID")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.NumAncestors, "NumAncestors")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := unmarshalStarlarkValue(args[2], &rpcArgs.Depth, "Depth")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "GoroutineID":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.GoroutineID, "GoroutineID")
			case "NumAncestors":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.NumAncestors, "NumAncestors")
			case "Depth":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Depth, "Depth")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("Ancestors", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["ancestors"] = "builtin ancestors(GoroutineID, NumAncestors, Depth)\n\nancestors returns the stacktraces for the ancestors of a goroutine."
	r["attached_to_existing_process"] = starlark.NewBuiltin("attached_to_existing_process", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.AttachedToExistingProcessIn
		var rpcRet rpc2.AttachedToExistingProcessOut
		err := env.ctx.Client().CallAPI("AttachedToExistingProcess", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["attached_to_existing_process"] = "builtin attached_to_existing_process()\n\nattached_to_existing_process returns whether we attached to a running process or not"
	r["build_id"] = starlark.NewBuiltin("build_id", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.BuildIDIn
		var rpcRet rpc2.BuildIDOut
		err := env.ctx.Client().CallAPI("BuildID", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["build_id"] = "builtin build_id()"
	r["cancel_next"] = starlark.NewBuiltin("cancel_next", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.CancelNextIn
		var rpcRet rpc2.CancelNextOut
		err := env.ctx.Client().CallAPI("CancelNext", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["cancel_next"] = "builtin cancel_next()"
	r["checkpoint"] = starlark.NewBuiltin("checkpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.CheckpointIn
		var rpcRet rpc2.CheckpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Where, "Where")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Where":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Where, "Where")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("Checkpoint", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["checkpoint"] = "builtin checkpoint(Where)"
	r["clear_breakpoint"] = starlark.NewBuiltin("clear_breakpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ClearBreakpointIn
		var rpcRet rpc2.ClearBreakpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Id, "Id")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Name, "Name")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Id":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Id, "Id")
			case "Name":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Name, "Name")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("ClearBreakpoint", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["clear_breakpoint"] = "builtin clear_breakpoint(Id, Name)\n\nclear_breakpoint deletes a breakpoint by Name (if Name is not an\nempty string) or by ID."
	r["clear_checkpoint"] = starlark.NewBuiltin("clear_checkpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ClearCheckpointIn
		var rpcRet rpc2.ClearCheckpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.ID, "ID")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "ID":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.ID, "ID")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("ClearCheckpoint", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["clear_checkpoint"] = "builtin clear_checkpoint(ID)"
	r["raw_command"] = starlark.NewBuiltin("raw_command", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs api.DebuggerCommand
		var rpcRet rpc2.CommandOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Name, "Name")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.ThreadID, "ThreadID")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := unmarshalStarlarkValue(args[2], &rpcArgs.GoroutineID, "GoroutineID")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := unmarshalStarlarkValue(args[3], &rpcArgs.ReturnInfoLoadConfig, "ReturnInfoLoadConfig")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		} else {
			cfg := env.ctx.LoadConfig()
			rpcArgs.ReturnInfoLoadConfig = &cfg
		}
		if len(args) > 4 && args[4] != starlark.None {
			err := unmarshalStarlarkValue(args[4], &rpcArgs.Expr, "Expr")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 5 && args[5] != starlark.None {
			err := unmarshalStarlarkValue(args[5], &rpcArgs.UnsafeCall, "UnsafeCall")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Name":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Name, "Name")
			case "ThreadID":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.ThreadID, "ThreadID")
			case "GoroutineID":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.GoroutineID, "GoroutineID")
			case "ReturnInfoLoadConfig":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.ReturnInfoLoadConfig, "ReturnInfoLoadConfig")
			case "Expr":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Expr, "Expr")
			case "UnsafeCall":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.UnsafeCall, "UnsafeCall")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("Command", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["raw_command"] = "builtin raw_command(Name, ThreadID, GoroutineID, ReturnInfoLoadConfig, Expr, UnsafeCall)\n\nraw_command interrupts, continues and steps through the program."
	r["create_breakpoint"] = starlark.NewBuiltin("create_breakpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.CreateBreakpointIn
		var rpcRet rpc2.CreateBreakpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Breakpoint, "Breakpoint")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.LocExpr, "LocExpr")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := unmarshalStarlarkValue(args[2], &rpcArgs.SubstitutePathRules, "SubstitutePathRules")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := unmarshalStarlarkValue(args[3], &rpcArgs.Suspended, "Suspended")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Breakpoint":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Breakpoint, "Breakpoint")
			case "LocExpr":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.LocExpr, "LocExpr")
			case "SubstitutePathRules":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.SubstitutePathRules, "SubstitutePathRules")
			case "Suspended":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Suspended, "Suspended")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("CreateBreakpoint", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["create_breakpoint"] = "builtin create_breakpoint(Breakpoint, LocExpr, SubstitutePathRules, Suspended)\n\ncreate_breakpoint creates a new breakpoint. The client is expected to populate `CreateBreakpointIn`\nwith an `api.Breakpoint` struct describing where to set the breakpoint. For more information on\nhow to properly request a breakpoint via the `api.Breakpoint` struct see the documentation for\n`debugger.CreateBreakpoint` here: https://pkg.go.dev/github.com/go-delve/delve/service/debugger#Debugger.CreateBreakpoint."
	r["create_ebpf_tracepoint"] = starlark.NewBuiltin("create_ebpf_tracepoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.CreateEBPFTracepointIn
		var rpcRet rpc2.CreateEBPFTracepointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.FunctionName, "FunctionName")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "FunctionName":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.FunctionName, "FunctionName")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("CreateEBPFTracepoint", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["create_ebpf_tracepoint"] = "builtin create_ebpf_tracepoint(FunctionName)"
	r["create_watchpoint"] = starlark.NewBuiltin("create_watchpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.CreateWatchpointIn
		var rpcRet rpc2.CreateWatchpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		} else {
			rpcArgs.Scope = env.ctx.Scope()
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Expr, "Expr")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := unmarshalStarlarkValue(args[2], &rpcArgs.Type, "Type")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Scope":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			case "Expr":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Expr, "Expr")
			case "Type":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Type, "Type")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("CreateWatchpoint", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["create_watchpoint"] = "builtin create_watchpoint(Scope, Expr, Type)"
	r["debug_info_directories"] = starlark.NewBuiltin("debug_info_directories", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.DebugInfoDirectoriesIn
		var rpcRet rpc2.DebugInfoDirectoriesOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Set, "Set")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.List, "List")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Set":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Set, "Set")
			case "List":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.List, "List")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("DebugInfoDirectories", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["debug_info_directories"] = "builtin debug_info_directories(Set, List)"
	r["detach"] = starlark.NewBuiltin("detach", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.DetachIn
		var rpcRet rpc2.DetachOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Kill, "Kill")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Kill":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Kill, "Kill")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("Detach", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["detach"] = "builtin detach(Kill)\n\ndetach detaches the debugger, optionally killing the process."
	r["disassemble"] = starlark.NewBuiltin("disassemble", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.DisassembleIn
		var rpcRet rpc2.DisassembleOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		} else {
			rpcArgs.Scope = env.ctx.Scope()
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.StartPC, "StartPC")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := unmarshalStarlarkValue(args[2], &rpcArgs.EndPC, "EndPC")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := unmarshalStarlarkValue(args[3], &rpcArgs.Flavour, "Flavour")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Scope":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			case "StartPC":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.StartPC, "StartPC")
			case "EndPC":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.EndPC, "EndPC")
			case "Flavour":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Flavour, "Flavour")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("Disassemble", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["disassemble"] = "builtin disassemble(Scope, StartPC, EndPC, Flavour)\n\ndisassemble code.\n\nIf both StartPC and EndPC are non-zero the specified range will be disassembled, otherwise the function containing StartPC will be disassembled.\n\nScope is used to mark the instruction the specified goroutine is stopped at.\n\nDisassemble will also try to calculate the destination address of an absolute indirect CALL if it happens to be the instruction the selected goroutine is stopped at."
	r["dump_cancel"] = starlark.NewBuiltin("dump_cancel", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.DumpCancelIn
		var rpcRet rpc2.DumpCancelOut
		err := env.ctx.Client().CallAPI("DumpCancel", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["dump_cancel"] = "builtin dump_cancel()\n\ndump_cancel cancels the core dump."
	r["dump_start"] = starlark.NewBuiltin("dump_start", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.DumpStartIn
		var rpcRet rpc2.DumpStartOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Destination, "Destination")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Destination":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Destination, "Destination")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("DumpStart", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["dump_start"] = "builtin dump_start(Destination)\n\ndump_start starts a core dump to arg.Destination."
	r["dump_wait"] = starlark.NewBuiltin("dump_wait", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.DumpWaitIn
		var rpcRet rpc2.DumpWaitOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Wait, "Wait")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Wait":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Wait, "Wait")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("DumpWait", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["dump_wait"] = "builtin dump_wait(Wait)\n\ndump_wait waits for the core dump to finish or for arg.Wait milliseconds.\nWait == 0 means return immediately.\nReturns the core dump status"
	r["eval"] = starlark.NewBuiltin("eval", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.EvalIn
		var rpcRet rpc2.EvalOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		} else {
			rpcArgs.Scope = env.ctx.Scope()
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Expr, "Expr")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := unmarshalStarlarkValue(args[2], &rpcArgs.Cfg, "Cfg")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		} else {
			cfg := env.ctx.LoadConfig()
			rpcArgs.Cfg = &cfg
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Scope":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			case "Expr":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Expr, "Expr")
			case "Cfg":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Cfg, "Cfg")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("Eval", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["eval"] = "builtin eval(Scope, Expr, Cfg)\n\neval returns a variable in the specified context.\n\nSee https://github.com/go-delve/delve/blob/master/Documentation/cli/expr.md\nfor a description of acceptable values of arg.Expr."
	r["examine_memory"] = starlark.NewBuiltin("examine_memory", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ExamineMemoryIn
		var rpcRet rpc2.ExaminedMemoryOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Address, "Address")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Length, "Length")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Address":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Address, "Address")
			case "Length":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Length, "Length")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("ExamineMemory", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["examine_memory"] = "builtin examine_memory(Address, Length)"
	r["find_location"] = starlark.NewBuiltin("find_location", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.FindLocationIn
		var rpcRet rpc2.FindLocationOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		} else {
			rpcArgs.Scope = env.ctx.Scope()
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Loc, "Loc")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := unmarshalStarlarkValue(args[2], &rpcArgs.IncludeNonExecutableLines, "IncludeNonExecutableLines")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := unmarshalStarlarkValue(args[3], &rpcArgs.SubstitutePathRules, "SubstitutePathRules")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Scope":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			case "Loc":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Loc, "Loc")
			case "IncludeNonExecutableLines":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.IncludeNonExecutableLines, "IncludeNonExecutableLines")
			case "SubstitutePathRules":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.SubstitutePathRules, "SubstitutePathRules")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("FindLocation", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["find_location"] = "builtin find_location(Scope, Loc, IncludeNonExecutableLines, SubstitutePathRules)\n\nfind_location returns concrete location information described by a location expression.\n\n\tloc ::= <filename>:<line> | <function>[:<line>] | /<regex>/ | (+|-)<offset> | <line> | *<address>\n\t* <filename> can be the full path of a file or just a suffix\n\t* <function> ::= <package>.<receiver type>.<name> | <package>.(*<receiver type>).<name> | <receiver type>.<name> | <package>.<name> | (*<receiver type>).<name> | <name>\n\t  <function> must be unambiguous\n\t* /<regex>/ will return a location for each function matched by regex\n\t* +<offset> returns a location for the line that is <offset> lines after the current line\n\t* -<offset> returns a location for the line that is <offset> lines before the current line\n\t* <line> returns a location for a line in the current file\n\t* *<address> returns the location corresponding to the specified address\n\nNOTE: this function does not actually set breakpoints."
	r["follow_exec"] = starlark.NewBuiltin("follow_exec", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.FollowExecIn
		var rpcRet rpc2.FollowExecOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Enable, "Enable")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Regex, "Regex")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Enable":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Enable, "Enable")
			case "Regex":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Regex, "Regex")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("FollowExec", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["follow_exec"] = "builtin follow_exec(Enable, Regex)\n\nfollow_exec enables or disables follow exec mode."
	r["follow_exec_enabled"] = starlark.NewBuiltin("follow_exec_enabled", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.FollowExecEnabledIn
		var rpcRet rpc2.FollowExecEnabledOut
		err := env.ctx.Client().CallAPI("FollowExecEnabled", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["follow_exec_enabled"] = "builtin follow_exec_enabled()\n\nfollow_exec_enabled returns true if follow exec mode is enabled."
	r["function_return_locations"] = starlark.NewBuiltin("function_return_locations", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.FunctionReturnLocationsIn
		var rpcRet rpc2.FunctionReturnLocationsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.FnName, "FnName")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "FnName":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.FnName, "FnName")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("FunctionReturnLocations", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["function_return_locations"] = "builtin function_return_locations(FnName)\n\nfunction_return_locations is the implements the client call of the same name. Look at client documentation for more information."
	r["get_breakpoint"] = starlark.NewBuiltin("get_breakpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.GetBreakpointIn
		var rpcRet rpc2.GetBreakpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Id, "Id")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Name, "Name")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Id":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Id, "Id")
			case "Name":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Name, "Name")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("GetBreakpoint", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["get_breakpoint"] = "builtin get_breakpoint(Id, Name)\n\nget_breakpoint gets a breakpoint by Name (if Name is not an empty string) or by ID."
	r["get_buffered_tracepoints"] = starlark.NewBuiltin("get_buffered_tracepoints", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.GetBufferedTracepointsIn
		var rpcRet rpc2.GetBufferedTracepointsOut
		err := env.ctx.Client().CallAPI("GetBufferedTracepoints", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["get_buffered_tracepoints"] = "builtin get_buffered_tracepoints()"
	r["get_thread"] = starlark.NewBuiltin("get_thread", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.GetThreadIn
		var rpcRet rpc2.GetThreadOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Id, "Id")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Id":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Id, "Id")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("GetThread", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["get_thread"] = "builtin get_thread(Id)\n\nget_thread gets a thread by its ID."
	r["is_multiclient"] = starlark.NewBuiltin("is_multiclient", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.IsMulticlientIn
		var rpcRet rpc2.IsMulticlientOut
		err := env.ctx.Client().CallAPI("IsMulticlient", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["is_multiclient"] = "builtin is_multiclient()"
	r["last_modified"] = starlark.NewBuiltin("last_modified", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.LastModifiedIn
		var rpcRet rpc2.LastModifiedOut
		err := env.ctx.Client().CallAPI("LastModified", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["last_modified"] = "builtin last_modified()"
	r["breakpoints"] = starlark.NewBuiltin("breakpoints", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ListBreakpointsIn
		var rpcRet rpc2.ListBreakpointsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.All, "All")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "All":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.All, "All")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("ListBreakpoints", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["breakpoints"] = "builtin breakpoints(All)\n\nbreakpoints gets all breakpoints."
	r["checkpoints"] = starlark.NewBuiltin("checkpoints", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ListCheckpointsIn
		var rpcRet rpc2.ListCheckpointsOut
		err := env.ctx.Client().CallAPI("ListCheckpoints", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["checkpoints"] = "builtin checkpoints()"
	r["dynamic_libraries"] = starlark.NewBuiltin("dynamic_libraries", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ListDynamicLibrariesIn
		var rpcRet rpc2.ListDynamicLibrariesOut
		err := env.ctx.Client().CallAPI("ListDynamicLibraries", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["dynamic_libraries"] = "builtin dynamic_libraries()"
	r["function_args"] = starlark.NewBuiltin("function_args", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ListFunctionArgsIn
		var rpcRet rpc2.ListFunctionArgsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		} else {
			rpcArgs.Scope = env.ctx.Scope()
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Cfg, "Cfg")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		} else {
			rpcArgs.Cfg = env.ctx.LoadConfig()
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Scope":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			case "Cfg":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Cfg, "Cfg")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("ListFunctionArgs", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["function_args"] = "builtin function_args(Scope, Cfg)\n\nfunction_args lists all arguments to the current function"
	r["functions"] = starlark.NewBuiltin("functions", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ListFunctionsIn
		var rpcRet rpc2.ListFunctionsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Filter, "Filter")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Filter":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Filter, "Filter")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("ListFunctions", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["functions"] = "builtin functions(Filter)\n\nfunctions lists all functions in the process matching filter."
	r["goroutines"] = starlark.NewBuiltin("goroutines", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ListGoroutinesIn
		var rpcRet rpc2.ListGoroutinesOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Start, "Start")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Count, "Count")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := unmarshalStarlarkValue(args[2], &rpcArgs.Filters, "Filters")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := unmarshalStarlarkValue(args[3], &rpcArgs.GoroutineGroupingOptions, "GoroutineGroupingOptions")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 4 && args[4] != starlark.None {
			err := unmarshalStarlarkValue(args[4], &rpcArgs.EvalScope, "EvalScope")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		} else {
			scope := env.ctx.Scope()
			rpcArgs.EvalScope = &scope
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Start":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Start, "Start")
			case "Count":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Count, "Count")
			case "Filters":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Filters, "Filters")
			case "GoroutineGroupingOptions":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.GoroutineGroupingOptions, "GoroutineGroupingOptions")
			case "EvalScope":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.EvalScope, "EvalScope")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("ListGoroutines", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["goroutines"] = "builtin goroutines(Start, Count, Filters, GoroutineGroupingOptions, EvalScope)\n\ngoroutines lists all goroutines.\nIf Count is specified ListGoroutines will return at the first Count\ngoroutines and an index in Nextg, that can be passed as the Start\nparameter, to get more goroutines from ListGoroutines.\nPassing a value of Start that wasn't returned by ListGoroutines will skip\nan undefined number of goroutines.\n\nIf arg.Filters are specified the list of returned goroutines is filtered\napplying the specified filters.\nFor example:\n\n\tListGoroutinesFilter{ Kind: ListGoroutinesFilterUserLoc, Negated: false, Arg: \"afile.go\" }\n\nwill only return goroutines whose UserLoc contains \"afile.go\" as a substring.\nMore specifically a goroutine matches a location filter if the specified\nlocation, formatted like this:\n\n\tfilename:lineno in function\n\ncontains Arg[0] as a substring.\n\nFilters can also be applied to goroutine labels:\n\n\tListGoroutineFilter{ Kind: ListGoroutinesFilterLabel, Negated: false, Arg: \"key=value\" }\n\nthis filter will only return goroutines that have a key=value label.\n\nIf arg.GroupBy is not GoroutineFieldNone then the goroutines will\nbe grouped with the specified criterion.\nIf the value of arg.GroupBy is GoroutineLabel goroutines will\nbe grouped by the value of the label with key GroupByKey.\nFor each group a maximum of MaxGroupMembers example goroutines are\nreturned, as well as the total number of goroutines in the group."
	r["local_vars"] = starlark.NewBuiltin("local_vars", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ListLocalVarsIn
		var rpcRet rpc2.ListLocalVarsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		} else {
			rpcArgs.Scope = env.ctx.Scope()
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Cfg, "Cfg")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		} else {
			rpcArgs.Cfg = env.ctx.LoadConfig()
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Scope":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			case "Cfg":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Cfg, "Cfg")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("ListLocalVars", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["local_vars"] = "builtin local_vars(Scope, Cfg)\n\nlocal_vars lists all local variables in scope."
	r["package_vars"] = starlark.NewBuiltin("package_vars", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ListPackageVarsIn
		var rpcRet rpc2.ListPackageVarsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Filter, "Filter")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Cfg, "Cfg")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		} else {
			rpcArgs.Cfg = env.ctx.LoadConfig()
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Filter":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Filter, "Filter")
			case "Cfg":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Cfg, "Cfg")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("ListPackageVars", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["package_vars"] = "builtin package_vars(Filter, Cfg)\n\npackage_vars lists all package variables in the context of the current thread."
	r["packages_build_info"] = starlark.NewBuiltin("packages_build_info", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ListPackagesBuildInfoIn
		var rpcRet rpc2.ListPackagesBuildInfoOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.IncludeFiles, "IncludeFiles")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Filter, "Filter")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "IncludeFiles":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.IncludeFiles, "IncludeFiles")
			case "Filter":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Filter, "Filter")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("ListPackagesBuildInfo", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["packages_build_info"] = "builtin packages_build_info(IncludeFiles, Filter)\n\npackages_build_info returns the list of packages used by the program along with\nthe directory where each package was compiled and optionally the list of\nfiles constituting the package.\nNote that the directory path is a best guess and may be wrong is a tool\nother than cmd/go is used to perform the build."
	r["registers"] = starlark.NewBuiltin("registers", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ListRegistersIn
		var rpcRet rpc2.ListRegistersOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.ThreadID, "ThreadID")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.IncludeFp, "IncludeFp")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := unmarshalStarlarkValue(args[2], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		} else {
			scope := env.ctx.Scope()
			rpcArgs.Scope = &scope
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "ThreadID":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.ThreadID, "ThreadID")
			case "IncludeFp":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.IncludeFp, "IncludeFp")
			case "Scope":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("ListRegisters", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["registers"] = "builtin registers(ThreadID, IncludeFp, Scope)\n\nregisters lists registers and their values.\nIf ListRegistersIn.Scope is not nil the registers of that eval scope will\nbe returned, otherwise ListRegistersIn.ThreadID will be used."
	r["sources"] = starlark.NewBuiltin("sources", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ListSourcesIn
		var rpcRet rpc2.ListSourcesOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Filter, "Filter")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Filter":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Filter, "Filter")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("ListSources", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["sources"] = "builtin sources(Filter)\n\nsources lists all source files in the process matching filter."
	r["targets"] = starlark.NewBuiltin("targets", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ListTargetsIn
		var rpcRet rpc2.ListTargetsOut
		err := env.ctx.Client().CallAPI("ListTargets", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["targets"] = "builtin targets()\n\ntargets returns the list of targets we are currently attached to."
	r["threads"] = starlark.NewBuiltin("threads", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ListThreadsIn
		var rpcRet rpc2.ListThreadsOut
		err := env.ctx.Client().CallAPI("ListThreads", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["threads"] = "builtin threads()\n\nthreads lists all threads."
	r["types"] = starlark.NewBuiltin("types", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ListTypesIn
		var rpcRet rpc2.ListTypesOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Filter, "Filter")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Filter":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Filter, "Filter")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("ListTypes", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["types"] = "builtin types(Filter)\n\ntypes lists all types in the process matching filter."
	r["process_pid"] = starlark.NewBuiltin("process_pid", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ProcessPidIn
		var rpcRet rpc2.ProcessPidOut
		err := env.ctx.Client().CallAPI("ProcessPid", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["process_pid"] = "builtin process_pid()\n\nprocess_pid returns the pid of the process we are debugging."
	r["recorded"] = starlark.NewBuiltin("recorded", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.RecordedIn
		var rpcRet rpc2.RecordedOut
		err := env.ctx.Client().CallAPI("Recorded", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["recorded"] = "builtin recorded()"
	r["restart"] = starlark.NewBuiltin("restart", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.RestartIn
		var rpcRet rpc2.RestartOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Position, "Position")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.ResetArgs, "ResetArgs")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := unmarshalStarlarkValue(args[2], &rpcArgs.NewArgs, "NewArgs")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := unmarshalStarlarkValue(args[3], &rpcArgs.Rerecord, "Rerecord")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 4 && args[4] != starlark.None {
			err := unmarshalStarlarkValue(args[4], &rpcArgs.Rebuild, "Rebuild")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 5 && args[5] != starlark.None {
			err := unmarshalStarlarkValue(args[5], &rpcArgs.NewRedirects, "NewRedirects")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Position":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Position, "Position")
			case "ResetArgs":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.ResetArgs, "ResetArgs")
			case "NewArgs":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.NewArgs, "NewArgs")
			case "Rerecord":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Rerecord, "Rerecord")
			case "Rebuild":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Rebuild, "Rebuild")
			case "NewRedirects":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.NewRedirects, "NewRedirects")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("Restart", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["restart"] = "builtin restart(Position, ResetArgs, NewArgs, Rerecord, Rebuild, NewRedirects)\n\nrestart restarts program."
	r["set_expr"] = starlark.NewBuiltin("set_expr", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.SetIn
		var rpcRet rpc2.SetOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		} else {
			rpcArgs.Scope = env.ctx.Scope()
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Symbol, "Symbol")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := unmarshalStarlarkValue(args[2], &rpcArgs.Value, "Value")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Scope":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			case "Symbol":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Symbol, "Symbol")
			case "Value":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Value, "Value")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("Set", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["set_expr"] = "builtin set_expr(Scope, Symbol, Value)\n\nset_expr sets the value of a variable. Only numerical types and\npointers are currently supported."
	r["stacktrace"] = starlark.NewBuiltin("stacktrace", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.StacktraceIn
		var rpcRet rpc2.StacktraceOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Id, "Id")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Depth, "Depth")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := unmarshalStarlarkValue(args[2], &rpcArgs.Full, "Full")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := unmarshalStarlarkValue(args[3], &rpcArgs.Defers, "Defers")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 4 && args[4] != starlark.None {
			err := unmarshalStarlarkValue(args[4], &rpcArgs.Opts, "Opts")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 5 && args[5] != starlark.None {
			err := unmarshalStarlarkValue(args[5], &rpcArgs.Cfg, "Cfg")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Id":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Id, "Id")
			case "Depth":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Depth, "Depth")
			case "Full":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Full, "Full")
			case "Defers":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Defers, "Defers")
			case "Opts":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Opts, "Opts")
			case "Cfg":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Cfg, "Cfg")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("Stacktrace", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["stacktrace"] = "builtin stacktrace(Id, Depth, Full, Defers, Opts, Cfg)\n\nstacktrace returns stacktrace of goroutine Id up to the specified Depth.\n\nIf Full is set it will also the variable of all local variables\nand function arguments of all stack frames."
	r["state"] = starlark.NewBuiltin("state", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.StateIn
		var rpcRet rpc2.StateOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.NonBlocking, "NonBlocking")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "NonBlocking":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.NonBlocking, "NonBlocking")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("State", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["state"] = "builtin state(NonBlocking)\n\nstate returns the current debugger state."
	r["toggle_breakpoint"] = starlark.NewBuiltin("toggle_breakpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := isCancelled(thread); err != nil {
			return starlark.None, decorateError(thread, err)
		}
		var rpcArgs rpc2.ToggleBreakpointIn
		var rpcRet rpc2.ToggleBreakpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := unmarshalStarlarkValue(args[0], &rpcArgs.Id, "Id")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := unmarshalStarlarkValue(args[1], &rpcArgs.Name, "Name")
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Id":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Id, "Id")
			case "Name":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.Name, "Name")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, decorateError(thread, err)
			}
		}
		err := env.ctx.Client().CallAPI("ToggleBreakpoint", &rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.interfaceToStarlarkValue(rpcRet), nil
	})
	doc["toggle_breakpoint"] = "builtin toggle_breakpoint(Id, Name)\n\ntoggle_breakpoint toggles on or off a breakpoint by Name (if Name is not an\nempty string) or by ID."
	return r, doc
}
