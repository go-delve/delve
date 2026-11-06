// DO NOT EDIT: auto-generated using github.com/go-delve/build-tools/cmd/gen-starlark-bindings

package rpc2

import (
	"errors"
	"fmt"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/debugger"
	"go.starlark.net/starlark"
)

var errNoServer = errors.New("no server")

type starlarkRPCCallback struct {
	rpcRet any
	err    error
}

func (*starlarkRPCCallback) DisconnectChan() chan struct{} {
	return nil
}

func (*starlarkRPCCallback) SetupDoneChan() chan struct{} {
	return nil
}

func (cb *starlarkRPCCallback) Return(out any, err error) {
	cb.rpcRet = out
	cb.err = err
}
func starlarkPredeclare(env *debugger.StarlarkEnv) (starlark.StringDict, map[string]string) {
	r := starlark.StringDict{}
	doc := make(map[string]string)

	r["amend_breakpoint"] = starlark.NewBuiltin("amend_breakpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs AmendBreakpointIn
		var rpcRet AmendBreakpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Breakpoint, "Breakpoint")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Breakpoint":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Breakpoint, "Breakpoint")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.AmendBreakpoint(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["amend_breakpoint"] = "builtin amend_breakpoint(Breakpoint)\n\namend_breakpoint allows user to update an existing breakpoint\nfor example to change the information retrieved when the\nbreakpoint is hit or to change, add or remove the break condition.\n\narg.Breakpoint.ID must be a valid breakpoint ID"
	r["ancestors"] = starlark.NewBuiltin("ancestors", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs AncestorsIn
		var rpcRet AncestorsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.GoroutineID, "GoroutineID")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.NumAncestors, "NumAncestors")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[2], &rpcArgs.Depth, "Depth")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "GoroutineID":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.GoroutineID, "GoroutineID")
			case "NumAncestors":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.NumAncestors, "NumAncestors")
			case "Depth":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Depth, "Depth")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.Ancestors(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["ancestors"] = "builtin ancestors(GoroutineID, NumAncestors, Depth)\n\nancestors returns the stacktraces for the ancestors of a goroutine."
	r["attached_to_existing_process"] = starlark.NewBuiltin("attached_to_existing_process", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs AttachedToExistingProcessIn
		var rpcRet AttachedToExistingProcessOut
		err := s.AttachedToExistingProcess(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["attached_to_existing_process"] = "builtin attached_to_existing_process()\n\nattached_to_existing_process returns whether we attached to a running process or not"
	r["build_id"] = starlark.NewBuiltin("build_id", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs BuildIDIn
		var rpcRet BuildIDOut
		err := s.BuildID(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["build_id"] = "builtin build_id()"
	r["cancel_next"] = starlark.NewBuiltin("cancel_next", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs CancelNextIn
		var rpcRet CancelNextOut
		err := s.CancelNext(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["cancel_next"] = "builtin cancel_next()"
	r["checkpoint"] = starlark.NewBuiltin("checkpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs CheckpointIn
		var rpcRet CheckpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Where, "Where")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Where":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Where, "Where")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.Checkpoint(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["checkpoint"] = "builtin checkpoint(Where)"
	r["clear_breakpoint"] = starlark.NewBuiltin("clear_breakpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ClearBreakpointIn
		var rpcRet ClearBreakpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Id, "Id")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Name, "Name")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Id":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Id, "Id")
			case "Name":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Name, "Name")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.ClearBreakpoint(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["clear_breakpoint"] = "builtin clear_breakpoint(Id, Name)\n\nclear_breakpoint deletes a breakpoint by Name (if Name is not an\nempty string) or by ID."
	r["clear_checkpoint"] = starlark.NewBuiltin("clear_checkpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ClearCheckpointIn
		var rpcRet ClearCheckpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.ID, "ID")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "ID":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.ID, "ID")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.ClearCheckpoint(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["clear_checkpoint"] = "builtin clear_checkpoint(ID)"
	r["raw_command"] = starlark.NewBuiltin("raw_command", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs api.DebuggerCommand
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Name, "Name")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.ThreadID, "ThreadID")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[2], &rpcArgs.GoroutineID, "GoroutineID")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[3], &rpcArgs.ReturnInfoLoadConfig, "ReturnInfoLoadConfig")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			cfg := env.LoadConfig()
			rpcArgs.ReturnInfoLoadConfig = &cfg
		}
		if len(args) > 4 && args[4] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[4], &rpcArgs.Expr, "Expr")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 5 && args[5] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[5], &rpcArgs.WithEvents, "WithEvents")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 6 && args[6] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[6], &rpcArgs.UnsafeCall, "UnsafeCall")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Name":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Name, "Name")
			case "ThreadID":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.ThreadID, "ThreadID")
			case "GoroutineID":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.GoroutineID, "GoroutineID")
			case "ReturnInfoLoadConfig":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.ReturnInfoLoadConfig, "ReturnInfoLoadConfig")
			case "Expr":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Expr, "Expr")
			case "WithEvents":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.WithEvents, "WithEvents")
			case "UnsafeCall":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.UnsafeCall, "UnsafeCall")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		cb := &starlarkRPCCallback{}
		s.Command(rpcArgs, cb)
		if cb.err != nil {
			return starlark.None, cb.err
		}
		return env.InterfaceToStarlarkValue(cb.rpcRet), nil
	})
	doc["raw_command"] = "builtin raw_command(Name, ThreadID, GoroutineID, ReturnInfoLoadConfig, Expr, WithEvents, UnsafeCall)\n\nraw_command interrupts, continues and steps through the program."
	r["create_breakpoint"] = starlark.NewBuiltin("create_breakpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs CreateBreakpointIn
		var rpcRet CreateBreakpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Breakpoint, "Breakpoint")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.LocExpr, "LocExpr")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[2], &rpcArgs.SubstitutePathRules, "SubstitutePathRules")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[3], &rpcArgs.Suspended, "Suspended")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Breakpoint":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Breakpoint, "Breakpoint")
			case "LocExpr":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.LocExpr, "LocExpr")
			case "SubstitutePathRules":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.SubstitutePathRules, "SubstitutePathRules")
			case "Suspended":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Suspended, "Suspended")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.CreateBreakpoint(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["create_breakpoint"] = "builtin create_breakpoint(Breakpoint, LocExpr, SubstitutePathRules, Suspended)\n\ncreate_breakpoint creates a new breakpoint. The client is expected to populate `CreateBreakpointIn`\nwith an `api.Breakpoint` struct describing where to set the breakpoint. For more information on\nhow to properly request a breakpoint via the `api.Breakpoint` struct see the documentation for\n`debugger.CreateBreakpoint` here: https://pkg.go.dev/github.com/go-delve/delve/service/debugger#Debugger.CreateBreakpoint."
	r["create_ebpf_tracepoint"] = starlark.NewBuiltin("create_ebpf_tracepoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs CreateEBPFTracepointIn
		var rpcRet CreateEBPFTracepointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.FunctionName, "FunctionName")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "FunctionName":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.FunctionName, "FunctionName")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.CreateEBPFTracepoint(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["create_ebpf_tracepoint"] = "builtin create_ebpf_tracepoint(FunctionName)"
	r["create_watchpoint"] = starlark.NewBuiltin("create_watchpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs CreateWatchpointIn
		var rpcRet CreateWatchpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			rpcArgs.Scope = env.Scope()
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Expr, "Expr")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[2], &rpcArgs.Type, "Type")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Scope":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			case "Expr":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Expr, "Expr")
			case "Type":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Type, "Type")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.CreateWatchpoint(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["create_watchpoint"] = "builtin create_watchpoint(Scope, Expr, Type)"
	r["debug_info_directories"] = starlark.NewBuiltin("debug_info_directories", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs DebugInfoDirectoriesIn
		var rpcRet DebugInfoDirectoriesOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Set, "Set")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.List, "List")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Set":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Set, "Set")
			case "List":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.List, "List")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.DebugInfoDirectories(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["debug_info_directories"] = "builtin debug_info_directories(Set, List)"
	r["detach"] = starlark.NewBuiltin("detach", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs DetachIn
		var rpcRet DetachOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Kill, "Kill")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Kill":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Kill, "Kill")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.Detach(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["detach"] = "builtin detach(Kill)\n\ndetach detaches the debugger, optionally killing the process."
	r["disassemble"] = starlark.NewBuiltin("disassemble", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs DisassembleIn
		var rpcRet DisassembleOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			rpcArgs.Scope = env.Scope()
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.StartPC, "StartPC")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[2], &rpcArgs.EndPC, "EndPC")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[3], &rpcArgs.Flavour, "Flavour")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Scope":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			case "StartPC":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.StartPC, "StartPC")
			case "EndPC":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.EndPC, "EndPC")
			case "Flavour":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Flavour, "Flavour")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.Disassemble(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["disassemble"] = "builtin disassemble(Scope, StartPC, EndPC, Flavour)\n\ndisassemble code.\n\nIf both StartPC and EndPC are non-zero the specified range will be disassembled, otherwise the function containing StartPC will be disassembled.\n\nScope is used to mark the instruction the specified goroutine is stopped at.\n\nDisassemble will also try to calculate the destination address of an absolute indirect CALL if it happens to be the instruction the selected goroutine is stopped at."
	r["dump_cancel"] = starlark.NewBuiltin("dump_cancel", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs DumpCancelIn
		var rpcRet DumpCancelOut
		err := s.DumpCancel(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["dump_cancel"] = "builtin dump_cancel()\n\ndump_cancel cancels the core dump."
	r["dump_start"] = starlark.NewBuiltin("dump_start", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs DumpStartIn
		var rpcRet DumpStartOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Destination, "Destination")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Destination":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Destination, "Destination")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.DumpStart(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["dump_start"] = "builtin dump_start(Destination)\n\ndump_start starts a core dump to arg.Destination."
	r["dump_wait"] = starlark.NewBuiltin("dump_wait", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs DumpWaitIn
		var rpcRet DumpWaitOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Wait, "Wait")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Wait":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Wait, "Wait")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.DumpWait(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["dump_wait"] = "builtin dump_wait(Wait)\n\ndump_wait waits for the core dump to finish or for arg.Wait milliseconds.\nWait == 0 means return immediately.\nReturns the core dump status"
	r["eval"] = starlark.NewBuiltin("eval", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs EvalIn
		var rpcRet EvalOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			rpcArgs.Scope = env.Scope()
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Expr, "Expr")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[2], &rpcArgs.Cfg, "Cfg")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			cfg := env.LoadConfig()
			rpcArgs.Cfg = &cfg
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Scope":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			case "Expr":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Expr, "Expr")
			case "Cfg":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Cfg, "Cfg")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.Eval(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["eval"] = "builtin eval(Scope, Expr, Cfg)\n\neval returns a variable in the specified context.\n\nSee https://github.com/go-delve/delve/blob/master/Documentation/cli/expr.md\nfor a description of acceptable values of arg.Expr."
	r["examine_memory"] = starlark.NewBuiltin("examine_memory", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ExamineMemoryIn
		var rpcRet ExaminedMemoryOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Address, "Address")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Length, "Length")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Address":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Address, "Address")
			case "Length":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Length, "Length")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.ExamineMemory(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["examine_memory"] = "builtin examine_memory(Address, Length)"
	r["find_location"] = starlark.NewBuiltin("find_location", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs FindLocationIn
		var rpcRet FindLocationOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			rpcArgs.Scope = env.Scope()
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Loc, "Loc")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[2], &rpcArgs.IncludeNonExecutableLines, "IncludeNonExecutableLines")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[3], &rpcArgs.SubstitutePathRules, "SubstitutePathRules")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Scope":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			case "Loc":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Loc, "Loc")
			case "IncludeNonExecutableLines":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.IncludeNonExecutableLines, "IncludeNonExecutableLines")
			case "SubstitutePathRules":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.SubstitutePathRules, "SubstitutePathRules")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.FindLocation(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["find_location"] = "builtin find_location(Scope, Loc, IncludeNonExecutableLines, SubstitutePathRules)\n\nfind_location returns concrete location information described by a location expression.\n\n\tloc ::= <filename>:<line> | <function>[:<line>] | /<regex>/ | (+|-)<offset> | <line> | *<address>\n\t* <filename> can be the full path of a file or just a suffix\n\t* <function> ::= <package>.<receiver type>.<name> | <package>.(*<receiver type>).<name> | <receiver type>.<name> | <package>.<name> | (*<receiver type>).<name> | <name>\n\t  <function> must be unambiguous\n\t* /<regex>/ will return a location for each function matched by regex\n\t* +<offset> returns a location for the line that is <offset> lines after the current line\n\t* -<offset> returns a location for the line that is <offset> lines before the current line\n\t* <line> returns a location for a line in the current file\n\t* *<address> returns the location corresponding to the specified address\n\nNOTE: this function does not actually set breakpoints."
	r["follow_exec"] = starlark.NewBuiltin("follow_exec", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs FollowExecIn
		var rpcRet FollowExecOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Enable, "Enable")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Regex, "Regex")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Enable":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Enable, "Enable")
			case "Regex":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Regex, "Regex")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.FollowExec(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["follow_exec"] = "builtin follow_exec(Enable, Regex)\n\nfollow_exec enables or disables follow exec mode."
	r["follow_exec_enabled"] = starlark.NewBuiltin("follow_exec_enabled", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs FollowExecEnabledIn
		var rpcRet FollowExecEnabledOut
		err := s.FollowExecEnabled(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["follow_exec_enabled"] = "builtin follow_exec_enabled()\n\nfollow_exec_enabled returns true if follow exec mode is enabled."
	r["function_return_locations"] = starlark.NewBuiltin("function_return_locations", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs FunctionReturnLocationsIn
		var rpcRet FunctionReturnLocationsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.FnName, "FnName")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "FnName":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.FnName, "FnName")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.FunctionReturnLocations(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["function_return_locations"] = "builtin function_return_locations(FnName)\n\nfunction_return_locations is the implements the client call of the same name. Look at client documentation for more information."
	r["get_breakpoint"] = starlark.NewBuiltin("get_breakpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs GetBreakpointIn
		var rpcRet GetBreakpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Id, "Id")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Name, "Name")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Id":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Id, "Id")
			case "Name":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Name, "Name")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.GetBreakpoint(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["get_breakpoint"] = "builtin get_breakpoint(Id, Name)\n\nget_breakpoint gets a breakpoint by Name (if Name is not an empty string) or by ID."
	r["get_buffered_tracepoints"] = starlark.NewBuiltin("get_buffered_tracepoints", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs GetBufferedTracepointsIn
		var rpcRet GetBufferedTracepointsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.LoadCfg, "LoadCfg")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			cfg := env.LoadConfig()
			rpcArgs.LoadCfg = &cfg
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "LoadCfg":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.LoadCfg, "LoadCfg")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.GetBufferedTracepoints(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["get_buffered_tracepoints"] = "builtin get_buffered_tracepoints(LoadCfg)"
	r["get_thread"] = starlark.NewBuiltin("get_thread", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs GetThreadIn
		var rpcRet GetThreadOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Id, "Id")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Id":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Id, "Id")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.GetThread(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["get_thread"] = "builtin get_thread(Id)\n\nget_thread gets a thread by its ID."
	r["guess_substitute_path"] = starlark.NewBuiltin("guess_substitute_path", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs GuessSubstitutePathIn
		var rpcRet GuessSubstitutePathOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Args, "Args")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Args":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Args, "Args")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.GuessSubstitutePath(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["guess_substitute_path"] = "builtin guess_substitute_path(Args)"
	r["is_multiclient"] = starlark.NewBuiltin("is_multiclient", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs IsMulticlientIn
		var rpcRet IsMulticlientOut
		err := s.IsMulticlient(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["is_multiclient"] = "builtin is_multiclient()"
	r["last_modified"] = starlark.NewBuiltin("last_modified", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs LastModifiedIn
		var rpcRet LastModifiedOut
		err := s.LastModified(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["last_modified"] = "builtin last_modified()"
	r["breakpoints"] = starlark.NewBuiltin("breakpoints", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ListBreakpointsIn
		var rpcRet ListBreakpointsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.All, "All")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "All":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.All, "All")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.ListBreakpoints(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["breakpoints"] = "builtin breakpoints(All)\n\nbreakpoints gets all breakpoints."
	r["checkpoints"] = starlark.NewBuiltin("checkpoints", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ListCheckpointsIn
		var rpcRet ListCheckpointsOut
		err := s.ListCheckpoints(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["checkpoints"] = "builtin checkpoints()"
	r["dynamic_libraries"] = starlark.NewBuiltin("dynamic_libraries", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ListDynamicLibrariesIn
		var rpcRet ListDynamicLibrariesOut
		err := s.ListDynamicLibraries(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["dynamic_libraries"] = "builtin dynamic_libraries()"
	r["function_args"] = starlark.NewBuiltin("function_args", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ListFunctionArgsIn
		var rpcRet ListFunctionArgsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			rpcArgs.Scope = env.Scope()
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Cfg, "Cfg")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			rpcArgs.Cfg = env.LoadConfig()
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Scope":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			case "Cfg":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Cfg, "Cfg")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.ListFunctionArgs(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["function_args"] = "builtin function_args(Scope, Cfg)\n\nfunction_args lists all arguments to the current function"
	r["functions"] = starlark.NewBuiltin("functions", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ListFunctionsIn
		var rpcRet ListFunctionsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Filter, "Filter")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.FollowCalls, "FollowCalls")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Filter":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Filter, "Filter")
			case "FollowCalls":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.FollowCalls, "FollowCalls")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.ListFunctions(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["functions"] = "builtin functions(Filter, FollowCalls)\n\nfunctions lists all functions in the process matching filter."
	r["goroutines"] = starlark.NewBuiltin("goroutines", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ListGoroutinesIn
		var rpcRet ListGoroutinesOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Start, "Start")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Count, "Count")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[2], &rpcArgs.Filters, "Filters")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[3], &rpcArgs.GoroutineGroupingOptions, "GoroutineGroupingOptions")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 4 && args[4] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[4], &rpcArgs.EvalScope, "EvalScope")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			scope := env.Scope()
			rpcArgs.EvalScope = &scope
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Start":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Start, "Start")
			case "Count":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Count, "Count")
			case "Filters":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Filters, "Filters")
			case "GoroutineGroupingOptions":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.GoroutineGroupingOptions, "GoroutineGroupingOptions")
			case "EvalScope":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.EvalScope, "EvalScope")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.ListGoroutines(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["goroutines"] = "builtin goroutines(Start, Count, Filters, GoroutineGroupingOptions, EvalScope)\n\ngoroutines lists all goroutines.\nIf Count is specified ListGoroutines will return at the first Count\ngoroutines and an index in Nextg, that can be passed as the Start\nparameter, to get more goroutines from ListGoroutines.\nPassing a value of Start that wasn't returned by ListGoroutines will skip\nan undefined number of goroutines.\n\nIf arg.Filters are specified the list of returned goroutines is filtered\napplying the specified filters.\nFor example:\n\n\tListGoroutinesFilter{ Kind: ListGoroutinesFilterUserLoc, Negated: false, Arg: \"afile.go\" }\n\nwill only return goroutines whose UserLoc contains \"afile.go\" as a substring.\nMore specifically a goroutine matches a location filter if the specified\nlocation, formatted like this:\n\n\tfilename:lineno in function\n\ncontains Arg[0] as a substring.\n\nFilters can also be applied to goroutine labels:\n\n\tListGoroutineFilter{ Kind: ListGoroutinesFilterLabel, Negated: false, Arg: \"key=value\" }\n\nthis filter will only return goroutines that have a key=value label.\n\nIf arg.GroupBy is not GoroutineFieldNone then the goroutines will\nbe grouped with the specified criterion.\nIf the value of arg.GroupBy is GoroutineLabel goroutines will\nbe grouped by the value of the label with key GroupByKey.\nFor each group a maximum of MaxGroupMembers example goroutines are\nreturned, as well as the total number of goroutines in the group."
	r["local_vars"] = starlark.NewBuiltin("local_vars", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ListLocalVarsIn
		var rpcRet ListLocalVarsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			rpcArgs.Scope = env.Scope()
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Cfg, "Cfg")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			rpcArgs.Cfg = env.LoadConfig()
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Scope":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			case "Cfg":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Cfg, "Cfg")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.ListLocalVars(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["local_vars"] = "builtin local_vars(Scope, Cfg)\n\nlocal_vars lists all local variables in scope."
	r["package_vars"] = starlark.NewBuiltin("package_vars", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ListPackageVarsIn
		var rpcRet ListPackageVarsOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Filter, "Filter")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Cfg, "Cfg")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			rpcArgs.Cfg = env.LoadConfig()
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Filter":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Filter, "Filter")
			case "Cfg":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Cfg, "Cfg")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.ListPackageVars(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["package_vars"] = "builtin package_vars(Filter, Cfg)\n\npackage_vars lists all package variables in the context of the current thread."
	r["packages_build_info"] = starlark.NewBuiltin("packages_build_info", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ListPackagesBuildInfoIn
		var rpcRet ListPackagesBuildInfoOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.IncludeFiles, "IncludeFiles")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Filter, "Filter")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "IncludeFiles":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.IncludeFiles, "IncludeFiles")
			case "Filter":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Filter, "Filter")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.ListPackagesBuildInfo(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["packages_build_info"] = "builtin packages_build_info(IncludeFiles, Filter)\n\npackages_build_info returns the list of packages used by the program along with\nthe directory where each package was compiled and optionally the list of\nfiles constituting the package.\nNote that the directory path is a best guess and may be wrong is a tool\nother than cmd/go is used to perform the build."
	r["registers"] = starlark.NewBuiltin("registers", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ListRegistersIn
		var rpcRet ListRegistersOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.ThreadID, "ThreadID")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.IncludeFp, "IncludeFp")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[2], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			scope := env.Scope()
			rpcArgs.Scope = &scope
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "ThreadID":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.ThreadID, "ThreadID")
			case "IncludeFp":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.IncludeFp, "IncludeFp")
			case "Scope":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.ListRegisters(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["registers"] = "builtin registers(ThreadID, IncludeFp, Scope)\n\nregisters lists registers and their values.\nIf ListRegistersIn.Scope is not nil the registers of that eval scope will\nbe returned, otherwise ListRegistersIn.ThreadID will be used."
	r["sources"] = starlark.NewBuiltin("sources", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ListSourcesIn
		var rpcRet ListSourcesOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Filter, "Filter")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Filter":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Filter, "Filter")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.ListSources(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["sources"] = "builtin sources(Filter)\n\nsources lists all source files in the process matching filter."
	r["targets"] = starlark.NewBuiltin("targets", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ListTargetsIn
		var rpcRet ListTargetsOut
		err := s.ListTargets(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["targets"] = "builtin targets()\n\ntargets returns the list of targets we are currently attached to."
	r["threads"] = starlark.NewBuiltin("threads", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ListThreadsIn
		var rpcRet ListThreadsOut
		err := s.ListThreads(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["threads"] = "builtin threads()\n\nthreads lists all threads."
	r["types"] = starlark.NewBuiltin("types", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ListTypesIn
		var rpcRet ListTypesOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Filter, "Filter")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Filter":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Filter, "Filter")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.ListTypes(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["types"] = "builtin types(Filter)\n\ntypes lists all types in the process matching filter."
	r["process_pid"] = starlark.NewBuiltin("process_pid", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ProcessPidIn
		var rpcRet ProcessPidOut
		err := s.ProcessPid(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["process_pid"] = "builtin process_pid()\n\nprocess_pid returns the pid of the process we are debugging."
	r["recorded"] = starlark.NewBuiltin("recorded", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs RecordedIn
		var rpcRet RecordedOut
		err := s.Recorded(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["recorded"] = "builtin recorded()"
	r["restart"] = starlark.NewBuiltin("restart", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs RestartIn
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Position, "Position")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.ResetArgs, "ResetArgs")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[2], &rpcArgs.NewArgs, "NewArgs")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[3], &rpcArgs.Rerecord, "Rerecord")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 4 && args[4] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[4], &rpcArgs.Rebuild, "Rebuild")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 5 && args[5] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[5], &rpcArgs.NewRedirects, "NewRedirects")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Position":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Position, "Position")
			case "ResetArgs":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.ResetArgs, "ResetArgs")
			case "NewArgs":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.NewArgs, "NewArgs")
			case "Rerecord":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Rerecord, "Rerecord")
			case "Rebuild":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Rebuild, "Rebuild")
			case "NewRedirects":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.NewRedirects, "NewRedirects")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		cb := &starlarkRPCCallback{}
		s.Restart(rpcArgs, cb)
		if cb.err != nil {
			return starlark.None, cb.err
		}
		return env.InterfaceToStarlarkValue(cb.rpcRet), nil
	})
	doc["restart"] = "builtin restart(Position, ResetArgs, NewArgs, Rerecord, Rebuild, NewRedirects)\n\nrestart restarts program."
	r["set_expr"] = starlark.NewBuiltin("set_expr", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs SetIn
		var rpcRet SetOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Scope, "Scope")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		} else {
			rpcArgs.Scope = env.Scope()
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Symbol, "Symbol")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[2], &rpcArgs.Value, "Value")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[3], &rpcArgs.Timeout, "Timeout")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Scope":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Scope, "Scope")
			case "Symbol":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Symbol, "Symbol")
			case "Value":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Value, "Value")
			case "Timeout":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Timeout, "Timeout")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.Set(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["set_expr"] = "builtin set_expr(Scope, Symbol, Value, Timeout)\n\nset_expr sets the value of a variable. Only numerical types and\npointers are currently supported."
	r["set_execution_point"] = starlark.NewBuiltin("set_execution_point", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs SetExecutionPointIn
		var rpcRet SetExecutionPointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Addr, "Addr")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Addr":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Addr, "Addr")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.SetExecutionPoint(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["set_execution_point"] = "builtin set_execution_point(Addr)\n\nset_execution_point sets the next instruction to be executed by the current\nthread to the instruction at arg.Addr, without executing any of the\ninstructions in between (also known as \"set next statement\" or \"jump\"). The\ntarget address must be inside the function the current thread is stopped in."
	r["stacktrace"] = starlark.NewBuiltin("stacktrace", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs StacktraceIn
		var rpcRet StacktraceOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Id, "Id")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Depth, "Depth")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 2 && args[2] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[2], &rpcArgs.Full, "Full")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 3 && args[3] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[3], &rpcArgs.Defers, "Defers")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 4 && args[4] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[4], &rpcArgs.Opts, "Opts")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 5 && args[5] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[5], &rpcArgs.Cfg, "Cfg")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 6 && args[6] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[6], &rpcArgs.Skip, "Skip")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Id":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Id, "Id")
			case "Depth":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Depth, "Depth")
			case "Full":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Full, "Full")
			case "Defers":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Defers, "Defers")
			case "Opts":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Opts, "Opts")
			case "Cfg":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Cfg, "Cfg")
			case "Skip":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Skip, "Skip")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.Stacktrace(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["stacktrace"] = "builtin stacktrace(Id, Depth, Full, Defers, Opts, Cfg, Skip)\n\nstacktrace returns stacktrace of goroutine Id up to the specified Depth.\n\nIf Full is set it will also the variable of all local variables\nand function arguments of all stack frames."
	r["state"] = starlark.NewBuiltin("state", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs StateIn
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.NonBlocking, "NonBlocking")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "NonBlocking":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.NonBlocking, "NonBlocking")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		cb := &starlarkRPCCallback{}
		s.State(rpcArgs, cb)
		if cb.err != nil {
			return starlark.None, cb.err
		}
		return env.InterfaceToStarlarkValue(cb.rpcRet), nil
	})
	doc["state"] = "builtin state(NonBlocking)\n\nstate returns the current debugger state."
	r["toggle_breakpoint"] = starlark.NewBuiltin("toggle_breakpoint", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs ToggleBreakpointIn
		var rpcRet ToggleBreakpointOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Id, "Id")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		if len(args) > 1 && args[1] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[1], &rpcArgs.Name, "Name")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Id":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Id, "Id")
			case "Name":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Name, "Name")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.ToggleBreakpoint(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["toggle_breakpoint"] = "builtin toggle_breakpoint(Id, Name)\n\ntoggle_breakpoint toggles on or off a breakpoint by Name (if Name is not an\nempty string) or by ID."
	r["type_info"] = starlark.NewBuiltin("type_info", func(thread *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		if err := debugger.StarlarkThreadIsCancelled(thread); err != nil {
			return starlark.None, debugger.DecorateStarlarkError(thread, err)
		}
		s, hasServer := thread.Local(debugger.DlvThreadLocalRPCServer).(*RPCServer)
		if !hasServer {
			return starlark.None, debugger.DecorateStarlarkError(thread, errNoServer)
		}
		var rpcArgs TypeInfoIn
		var rpcRet TypeInfoOut
		if len(args) > 0 && args[0] != starlark.None {
			err := debugger.UnmarshalStarlarkValue(args[0], &rpcArgs.Name, "Name")
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		for _, kv := range kwargs {
			var err error
			switch kv[0].(starlark.String) {
			case "Name":
				err = debugger.UnmarshalStarlarkValue(kv[1], &rpcArgs.Name, "Name")
			default:
				err = fmt.Errorf("unknown argument %q", kv[0])
			}
			if err != nil {
				return starlark.None, debugger.DecorateStarlarkError(thread, err)
			}
		}
		err := s.TypeInfo(rpcArgs, &rpcRet)
		if err != nil {
			return starlark.None, err
		}
		return env.InterfaceToStarlarkValue(&rpcRet), nil
	})
	doc["type_info"] = "builtin type_info(Name)\n\ntype_info returns informations about the specified type."
	return r, doc
}
