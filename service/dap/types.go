package dap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type LaunchMode string

// LaunchMode is the type of a launch mode.
const (
	// This is the same as "debug" launch mode.
	DefaultLaunchMode LaunchMode = ""
	// "debug": compiles your program with optimizations disabled, starts and attaches to it. This is the default mode.
	DebugLaunchMode LaunchMode = "debug"
	// "test": compiles your unit test program with optizations disabled, starts and attaches to it.
	TestLaunchMode LaunchMode = "test"
	// "exec": executes a precompiled binary and begin a debug session.
	ExecLaunchMode LaunchMode = "exec"
)

func (m *LaunchMode) UnmarshalJSON(data []byte) error {
	var mode string
	if err := json.Unmarshal(data, &mode); err != nil {
		if _, ok := err.(*json.UnmarshalTypeError); ok {
			return fmt.Errorf(`cannot unmarshal '%s' into 'mode' of type string`, data)
		}
		return err
	}
	switch mode {
	case "", "debug", "test", "exec":
		*m = LaunchMode(mode)
	default:
		return fmt.Errorf(`unsupported 'mode' value %q`, mode)
	}
	return nil
}

// LaunchConfig is the collection of launch request attributes recognized by delve DAP implementation.
type LaunchConfig struct {
	// If empty, defaults to `debug`.
	Mode LaunchMode `json:"mode,omitempty"`

	// Required when mode is `debug`, `test`, or `exec`.
	// Path to the program folder (or any go file within that folder)
	// when in `debug` or `test` mode, and to the pre-built binary file
	// to debug in `exec` mode.
	// If it is not an absolute path, it will be interpreted as a path
	// relative to the working directory of the delve process.
	Program string `json:"program,omitempty"`

	// Command line arguments passed to the debugged program.
	Args []string `json:"args,omitempty"`

	// Build flags, to be passed to the Go compiler.
	// For example, "-tags=integration -mod=vendor -cover -v".
	BuildFlags string `json:"buildFlags,omitempty"`

	// Absolute path to the working directory of the program being debugged
	// if a non-empty value is specified. If not specified or empty,
	// the working directory of the delve process will be used.
	// This is similar to delve's `-wd` flag.
	Cwd string `json:"cwd,omitempty"`

	// Output path for the binary of the debugee.
	// This is deleted after the debugsession ends.
	Output string `json:"output,omitempty"`

	// NoDebug is used to run the program without debugging.
	NoDebug bool `json:"noDebug,omitempty"`

	LaunchAttachCommonConfig
}

// LaunchAttachCommonConfig is the attributes common in both launch/attach requests.
type LaunchAttachCommonConfig struct {
	// Automatically stop program after launch or attach.
	StopOnEntry bool `json:"stopOnEntry,omitempty"`

	// Backend used by delve. See `dlv help backend` for allowed values.
	// (Default: default)
	Backend string `json:"backend,omitempty"`

	// Maximum depth of stack trace collected from Delve.
	// (Default: `50`)
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
// Both 'from' and 'to' must be specified.
type SubstitutePath struct {
	// The local path to be replaced when passing paths to the debugger.
	From string `json:"from"`
	// The remote path to be replaced when passing paths back to the client.
	To string `json:"to"`
}

func (m *SubstitutePath) UnmarshalJSON(data []byte) error {
	// use custom unmarshal to check if both from/to are set.
	var tmp struct {
		From *string `json:"from"`
		To   *string `json:"to"`
	}

	if err := json.Unmarshal(data, &tmp); err != nil {
		if _, ok := err.(*json.UnmarshalTypeError); ok {
			return fmt.Errorf(`cannot use %s as 'substitutePath' of type {"from": string, "to": string}`, data)
		}
		return err
	}
	if tmp.From == nil || tmp.To == nil {
		return errors.New("'substitutePath' requires both 'from' and 'to' entries")
	}
	m.From, m.To = *tmp.From, *tmp.To
	return nil
}

type AttachMode string

// AttachMode is the type of an attach mode.
const (
	// "": same as LocalAttachMode
	DefaultAttachMode AttachMode = ""
	// "local": attaches to the local process with the given ProcessID. This is the default mode.
	LocalAttachMode AttachMode = "string"
)

func (m *AttachMode) UnmarshalJSON(data []byte) error {
	var mode string
	if err := json.Unmarshal(data, &mode); err != nil {
		if _, ok := err.(*json.UnmarshalTypeError); ok {
			return fmt.Errorf(`cannot unmarshal '%s' into 'mode' of type string`, data)
		}
		return err
	}
	switch mode {
	case "", "local":
		*m = AttachMode(mode)
	default:
		return fmt.Errorf(`unsupported 'mode' value %q`, mode)
	}
	return nil
}

// AttachConfig is the collection of attach request attributes recognized by delve DAP implementation.
type AttachConfig struct {
	// Attach mode.
	Mode AttachMode `json:"mode,omitempty"`

	// The numeric ID of the process to be debugged. Required and must not be 0.
	ProcessID int `json:"processId,omitempty"`

	LaunchAttachCommonConfig
}

func unmarshalLaunchAttachArgs(input map[string]interface{}, config interface{}) error {
	// TODO(hyangah): after go-dap PR 57 is merged, update the dependency and change to use
	// the new API.
	//   func unmarshalLaunchAttachArgs(input json.RawMessage, config interface{}) error { ... }
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(input); err != nil {
		return err
	}
	if err := json.Unmarshal(buf.Bytes(), config); err != nil {
		if uerr, ok := err.(*json.UnmarshalTypeError); ok {
			// Format json.UnmarshalTypeError error string in our own way. E.g.,
			//   "json: cannot unmarshal number into Go struct field LaunchArgs.substitutePath of type dap.SubstitutePath"
			//   => "cannot unmarshal number into 'substitutePath' of type {from:string, to:string}"
			//   "json: cannot unmarshal number into Go struct field LaunchArgs.program of type string" (go1.16)
			//   => "cannot unmarshal number into 'program' of type string"
			if strings.HasPrefix(uerr.Value, "substitutePath") {
				return fmt.Errorf("cannot unmarshal %v into 'substitutePath' of type {from:string, to:string}", uerr.Value)
			}
			return fmt.Errorf("cannot unmarshal %v into %q of type %v", uerr.Value, uerr.Field, uerr.Type.String())
		}
		return err
	}
	return nil
}
