// DO NOT EDIT: auto-generated using github.com/go-delve/build-tools/cmd/gen-suitablemethods

package rpccommon

import (
	"github.com/go-delve/delve/service/rpc2"
	"reflect"
)

func suitableMethods2(s *rpc2.RPCServer, methods map[string]*methodType) {
	methods["RPCServer.AmendBreakpoint"] = &methodType{method: reflect.ValueOf(s.AmendBreakpoint)}
	methods["RPCServer.Ancestors"] = &methodType{method: reflect.ValueOf(s.Ancestors)}
	methods["RPCServer.AttachedToExistingProcess"] = &methodType{method: reflect.ValueOf(s.AttachedToExistingProcess)}
	methods["RPCServer.BuildID"] = &methodType{method: reflect.ValueOf(s.BuildID)}
	methods["RPCServer.CancelDownloads"] = &methodType{method: reflect.ValueOf(s.CancelDownloads)}
	methods["RPCServer.CancelNext"] = &methodType{method: reflect.ValueOf(s.CancelNext)}
	methods["RPCServer.Checkpoint"] = &methodType{method: reflect.ValueOf(s.Checkpoint)}
	methods["RPCServer.ClearBreakpoint"] = &methodType{method: reflect.ValueOf(s.ClearBreakpoint)}
	methods["RPCServer.ClearCheckpoint"] = &methodType{method: reflect.ValueOf(s.ClearCheckpoint)}
	methods["RPCServer.Command"] = &methodType{method: reflect.ValueOf(s.Command)}
	methods["RPCServer.CreateBreakpoint"] = &methodType{method: reflect.ValueOf(s.CreateBreakpoint)}
	methods["RPCServer.CreateEBPFTracepoint"] = &methodType{method: reflect.ValueOf(s.CreateEBPFTracepoint)}
	methods["RPCServer.CreateWatchpoint"] = &methodType{method: reflect.ValueOf(s.CreateWatchpoint)}
	methods["RPCServer.DebugInfoDirectories"] = &methodType{method: reflect.ValueOf(s.DebugInfoDirectories)}
	methods["RPCServer.Detach"] = &methodType{method: reflect.ValueOf(s.Detach)}
	methods["RPCServer.Disassemble"] = &methodType{method: reflect.ValueOf(s.Disassemble)}
	methods["RPCServer.DownloadLibraryDebugInfo"] = &methodType{method: reflect.ValueOf(s.DownloadLibraryDebugInfo)}
	methods["RPCServer.DumpCancel"] = &methodType{method: reflect.ValueOf(s.DumpCancel)}
	methods["RPCServer.DumpStart"] = &methodType{method: reflect.ValueOf(s.DumpStart)}
	methods["RPCServer.DumpWait"] = &methodType{method: reflect.ValueOf(s.DumpWait)}
	methods["RPCServer.Eval"] = &methodType{method: reflect.ValueOf(s.Eval)}
	methods["RPCServer.ExamineMemory"] = &methodType{method: reflect.ValueOf(s.ExamineMemory)}
	methods["RPCServer.FindLocation"] = &methodType{method: reflect.ValueOf(s.FindLocation)}
	methods["RPCServer.FollowExec"] = &methodType{method: reflect.ValueOf(s.FollowExec)}
	methods["RPCServer.FollowExecEnabled"] = &methodType{method: reflect.ValueOf(s.FollowExecEnabled)}
	methods["RPCServer.FunctionReturnLocations"] = &methodType{method: reflect.ValueOf(s.FunctionReturnLocations)}
	methods["RPCServer.GetBreakpoint"] = &methodType{method: reflect.ValueOf(s.GetBreakpoint)}
	methods["RPCServer.GetBufferedTracepoints"] = &methodType{method: reflect.ValueOf(s.GetBufferedTracepoints)}
	methods["RPCServer.GetEvents"] = &methodType{method: reflect.ValueOf(s.GetEvents)}
	methods["RPCServer.GetThread"] = &methodType{method: reflect.ValueOf(s.GetThread)}
	methods["RPCServer.GuessSubstitutePath"] = &methodType{method: reflect.ValueOf(s.GuessSubstitutePath)}
	methods["RPCServer.IsMulticlient"] = &methodType{method: reflect.ValueOf(s.IsMulticlient)}
	methods["RPCServer.LastModified"] = &methodType{method: reflect.ValueOf(s.LastModified)}
	methods["RPCServer.ListBreakpoints"] = &methodType{method: reflect.ValueOf(s.ListBreakpoints)}
	methods["RPCServer.ListCheckpoints"] = &methodType{method: reflect.ValueOf(s.ListCheckpoints)}
	methods["RPCServer.ListDynamicLibraries"] = &methodType{method: reflect.ValueOf(s.ListDynamicLibraries)}
	methods["RPCServer.ListFunctionArgs"] = &methodType{method: reflect.ValueOf(s.ListFunctionArgs)}
	methods["RPCServer.ListFunctions"] = &methodType{method: reflect.ValueOf(s.ListFunctions)}
	methods["RPCServer.ListGoroutines"] = &methodType{method: reflect.ValueOf(s.ListGoroutines)}
	methods["RPCServer.ListLocalVars"] = &methodType{method: reflect.ValueOf(s.ListLocalVars)}
	methods["RPCServer.ListPackageVars"] = &methodType{method: reflect.ValueOf(s.ListPackageVars)}
	methods["RPCServer.ListPackagesBuildInfo"] = &methodType{method: reflect.ValueOf(s.ListPackagesBuildInfo)}
	methods["RPCServer.ListRegisters"] = &methodType{method: reflect.ValueOf(s.ListRegisters)}
	methods["RPCServer.ListSources"] = &methodType{method: reflect.ValueOf(s.ListSources)}
	methods["RPCServer.ListTargets"] = &methodType{method: reflect.ValueOf(s.ListTargets)}
	methods["RPCServer.ListThreads"] = &methodType{method: reflect.ValueOf(s.ListThreads)}
	methods["RPCServer.ListTypes"] = &methodType{method: reflect.ValueOf(s.ListTypes)}
	methods["RPCServer.ProcessPid"] = &methodType{method: reflect.ValueOf(s.ProcessPid)}
	methods["RPCServer.Recorded"] = &methodType{method: reflect.ValueOf(s.Recorded)}
	methods["RPCServer.Restart"] = &methodType{method: reflect.ValueOf(s.Restart)}
	methods["RPCServer.Set"] = &methodType{method: reflect.ValueOf(s.Set)}
	methods["RPCServer.Stacktrace"] = &methodType{method: reflect.ValueOf(s.Stacktrace)}
	methods["RPCServer.State"] = &methodType{method: reflect.ValueOf(s.State)}
	methods["RPCServer.StopRecording"] = &methodType{method: reflect.ValueOf(s.StopRecording)}
	methods["RPCServer.ToggleBreakpoint"] = &methodType{method: reflect.ValueOf(s.ToggleBreakpoint)}
}

func suitableMethodsCommon(s *RPCServer, methods map[string]*methodType) {
	methods["RPCServer.GetVersion"] = &methodType{method: reflect.ValueOf(s.GetVersion)}
	methods["RPCServer.SetApiVersion"] = &methodType{method: reflect.ValueOf(s.SetApiVersion)}
}
