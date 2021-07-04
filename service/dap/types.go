package dap

import (
	"encoding/json"
	"errors"
	"fmt"
)

// LaunchConfig is the collection of launch request attributes recognized by delve DAP implementation.
type LaunchConfig struct {
	// Command line arguments passed to the debugged program.
	Args []string `json:"args,omitempty"`

	// Backend used by delve. Maps to `dlv`'s `--backend` flag.
	// Allowed Values: `"default"`, `"native"`, `"lldb"`
	Backend string `json:"backend,omitempty"`

	// Build flags, to be passed to the Go compiler. Maps to dlv's `--build-flags` flag.
	BuildFlags string `json:"buildFlags,omitempty"`

	// Absolute path to the working directory of the program being debugged
	// if a non-empty value is specified. If not specified or empty,
	// the working directory of the delve process will be used.
	Cwd string `json:"cwd,omitempty"`

	// Environment variables passed to the program.
	Env map[string]string `json:"env,omitempty"`

	// One of `debug`, `test`, `exec`.
	Mode string `json:"mode,omitempty"`

	// Output path for the binary of the debugee.
	Output string `json:"output,omitempty"`

	// Path to the program folder (or any go file within that folder)
	// when in `debug` or `test` mode, and to the pre-built binary file
	// to debug in `exec` mode.
	// If it is not an absolute path, it will be interpreted as a path
	// relative to the working directory of the delve process.
	Program string `json:"program,omitempty"`

	// NoDebug is used to run the program without debugging.
	NoDebug bool `json:"noDebug,omitempty"`

	LaunchAttachCommonConfig
}

// LaunchAttachCommonConfig is the attributes common in both launch/attach requests.
type LaunchAttachCommonConfig struct {
	// Configuration name.
	Name string `json:"name,omitempty"`

	// Automatically stop program after launch.
	StopOnEntry bool `json:"stopOnEntry,omitempty"`

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

// AttachConfig is the collection of attach request attributes recognized by delve DAP implementation.
type AttachConfig struct {
	// Backend used by delve. Maps to `dlv`'s `--backend` flag.
	// Allowed Values: `"default"`, `"native"`, `"lldb"`
	Backend string `json:"backend,omitempty"`

	// Absolute path to the working directory of the program being debugged.
	Cwd string `json:"cwd,omitempty"`

	// Only empty or `local` is acceptable.
	Mode string `json:"mode,omitempty"`

	// The numeric ID of the process to be debugged. Must not be 0.
	ProcessID int `json:"processId,omitempty"`

	LaunchAttachCommonConfig
}
