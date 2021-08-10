package dap

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Launch debug sessions support the following modes:
// -- [DEFAULT] "debug" - builds and launches debugger for specified program (similar to 'dlv debug')
//      Required args: program
//      Optional args with default: output, cwd, noDebug
//      Optional args: buildFlags, args
// -- "test" - builds and launches debugger for specified test (similar to 'dlv test')
//      same args as above
// -- "exec" - launches debugger for precompiled binary (similar to 'dlv exec')
//      Required args: program
//      Optional args with default: cwd, noDebug
//      Optional args: args
// -- replay: skips program build and sets the Debugger.CoreFile property based on the
//		Required args: coreFilePath
//      Optional args: program, args
// -- core: skips program build and sets the Debugger.CoreFile property based on the
//      Required args: program, traceDirPath
//      Optional args: args
//
// TODO(hyangah): change this to 'validateLaunchMode' that checks
// all the required/optional fields mentioned above.
func isValidLaunchMode(mode string) bool {
	switch mode {
	case "exec", "debug", "test", "replay", "core":
		return true
	}
	return false
}

// Attach debug sessions support the following modes:
// -- [DEFAULT] "local" -- attaches debugger to a local running process
//      Required args: processID
// TODO(polina): support "remote" mode
func isValidAttachMode(mode string) bool {
	return mode == "local"
}

// Default values for Launch/Attach configs.
// Used to initialize configuration variables before decoding
// arguments in launch/attach requests.
var (
	defaultLaunchAttachCommonConfig = LaunchAttachCommonConfig{
		Backend:         "default",
		StackTraceDepth: 50,
	}
	defaultLaunchConfig = LaunchConfig{
		Mode:                     "debug",
		Output:                   defaultDebugBinary,
		LaunchAttachCommonConfig: defaultLaunchAttachCommonConfig,
	}
	defaultAttachConfig = AttachConfig{
		Mode:                     "local",
		LaunchAttachCommonConfig: defaultLaunchAttachCommonConfig,
	}
)

// LaunchConfig is the collection of launch request attributes recognized by delve DAP implementation.
type LaunchConfig struct {
	// Acceptable values are:
	//   "debug": compiles your program with optimizations disabled, starts and attaches to it.
	//   "test": compiles your unit test program with optimizations disabled, starts and attaches to it.
	//   "exec": executes a precompiled binary and begins a debug session.
	//   "replay": replays an rr trace.
	//   "core": examines a core dump.
	//
	// Default is "debug".
	Mode string `json:"mode,omitempty"`

	// Required when mode is `debug`, `test`, or `exec`.
	// Path to the program folder (or any go file within that folder)
	// when in `debug` or `test` mode, and to the pre-built binary file
	// to debug in `exec` mode.
	// If it is not an absolute path, it will be interpreted as a path
	// relative to the working directory of the delve process.
	Program string `json:"program,omitempty"`

	// Command line arguments passed to the debugged program.
	Args []string `json:"args,omitempty"`

	// Absolute path to the working directory of the program being debugged
	// if a non-empty value is specified. If not specified or empty,
	// the program or the program's directory will be used.
	// This is similar to delve's `--wd` flag.
	Cwd string `json:"cwd,omitempty"`

	// Build flags, to be passed to the Go compiler.
	// For example, "-tags=integration -mod=vendor -cover -v" like delve's `--build-flags`.
	BuildFlags string `json:"buildFlags,omitempty"`

	// Output path for the binary of the debugee.
	// This is deleted after the debug session ends.
	Output string `json:"output,omitempty"`

	// NoDebug is used to run the program without debugging.
	NoDebug bool `json:"noDebug,omitempty"`

	// TraceDirPath is the trace directory path for replay mode.
	// This is required for "replay" mode but unused in other modes.
	TraceDirPath string `json:"traceDirPath,omitempty"`

	// CoreFilePath is the core file path for core mode.
	// This is required for "core" mode but unused in other modes.
	CoreFilePath string `json:"coreFilePath,omitempty"`

	LaunchAttachCommonConfig
}

// LaunchAttachCommonConfig is the attributes common in both launch/attach requests.
type LaunchAttachCommonConfig struct {
	// Automatically stop program after launch or attach.
	StopOnEntry bool `json:"stopOnEntry,omitempty"`

	// Backend used by delve. See `dlv backend` for allowed values.
	// Default is "default".
	Backend string `json:"backend,omitempty"`

	// Maximum depth of stack trace collected from Delve.
	// Default is 50.
	StackTraceDepth int `json:"stackTraceDepth,omitempty"`

	// Boolean value to indicate whether global package variables
	// should be shown in the variables pane or not.
	ShowGlobalVariables bool `json:"showGlobalVariables,omitempty"`

	// An array of mappings from a local path (client) to the remote path (debugger).
	// This setting is useful when working in a file system with symbolic links,
	// running remote debugging, or debugging an executable compiled externally.
	// The debug adapter will replace the local path with the remote path in all of the calls.
	SubstitutePath []SubstitutePath `json:"substitutePath,omitempty"`
}

// SubstitutePath defines a mapping from a local path to the remote path.
// Both 'from' and 'to' must be specified and non-empty.
type SubstitutePath struct {
	// The local path to be replaced when passing paths to the debugger.
	From string `json:"from,omitempty"`
	// The remote path to be replaced when passing paths back to the client.
	To string `json:"to,omitempty"`
}

func (m *SubstitutePath) UnmarshalJSON(data []byte) error {
	// use custom unmarshal to check if both from/to are set.
	type tmpType SubstitutePath
	var tmp tmpType

	if err := json.Unmarshal(data, &tmp); err != nil {
		if _, ok := err.(*json.UnmarshalTypeError); ok {
			return fmt.Errorf(`cannot use %s as 'substitutePath' of type {"from":string, "to":string}`, data)
		}
		return err
	}
	if tmp.From == "" || tmp.To == "" {
		return errors.New("'substitutePath' requires both 'from' and 'to' entries")
	}
	*m = SubstitutePath(tmp)
	return nil
}

// AttachConfig is the collection of attach request attributes recognized by delve DAP implementation.
type AttachConfig struct {
	// Acceptable values are:
	//   "local": attaches to the local process with the given ProcessID.
	//
	// Default is "local".
	Mode string `json:"mode"`

	// The numeric ID of the process to be debugged. Required and must not be 0.
	ProcessID int `json:"processId,omitempty"`

	LaunchAttachCommonConfig
}

// unmarshalLaunchAttachArgs wraps unmarshalling of launch/attach request's
// arguments attribute. Upon unmarshal failure, it returns an error massaged
// to be suitable for end-users.
func unmarshalLaunchAttachArgs(input json.RawMessage, config interface{}) error {
	if err := json.Unmarshal(input, config); err != nil {
		if uerr, ok := err.(*json.UnmarshalTypeError); ok {
			// Format json.UnmarshalTypeError error string in our own way. E.g.,
			//   "json: cannot unmarshal number into Go struct field LaunchArgs.substitutePath of type dap.SubstitutePath"
			//   => "cannot unmarshal number into 'substitutePath' of type {from:string, to:string}"
			//   "json: cannot unmarshal number into Go struct field LaunchArgs.program of type string" (go1.16)
			//   => "cannot unmarshal number into 'program' of type string"
			typ := uerr.Type.String()
			if uerr.Field == "substitutePath" {
				typ = `{"from":string, "to":string}`
			}
			return fmt.Errorf("cannot unmarshal %v into %q of type %v", uerr.Value, uerr.Field, typ)
		}
		return err
	}
	return nil
}
