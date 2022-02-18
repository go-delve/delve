// DO NOT EDIT: auto-generated using _scripts/gen-starlark-bindings.go

package starbind

import (
	"fmt"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/rpc2"
	"go.starlark.net/starlark"
)

func (env *Env) starlarkPredeclare() starlark.StringDict {
	r := starlark.StringDict{}

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
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "IncludeFiles":
				err = unmarshalStarlarkValue(kv[1], &rpcArgs.IncludeFiles, "IncludeFiles")
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
	return r
}
