package debug

import (
	"errors"
	"fmt"
	"go/ast"
	"runtime"

	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/core"
	"github.com/go-delve/delve/pkg/proc/gdbserial"
	"github.com/go-delve/delve/pkg/proc/native"
)

// Target represents the process being debugged.
// It is responsible for implementing the high level logic
// that is used to manipulate and inspect a running process.
type Target struct {
	proc.Process

	bi *proc.BinaryInfo

	// Breakpoint table, holds information on breakpoints.
	// Maps instruction address to Breakpoint struct.
	breakpoints proc.BreakpointMap
}

// New returns an initialized Target.
func New(p proc.Process, os, arch string, debugInfoDirs []string) (*Target, error) {
	bi := proc.NewBinaryInfo(os, arch, debugInfoDirs)
	t := &Target{
		Process:     p,
		bi:          bi,
		breakpoints: proc.NewBreakpointMap(),
	}
	// TODO(refactor) REMOVE BEFORE MERGE
	p.SetTarget(t)
	if err := t.Initialize(); err != nil {
		return nil, err
	}
	return t, nil
}

// ErrNoAttachPath is the error returned when the client tries to attach to
// a process on macOS using the lldb backend without specifying the path to
// the target's executable.
var ErrNoAttachPath = errors.New("must specify executable path on macOS")

// Attach will attach to the process specified by 'pid' using the backend specified.
// If debugInfoDirs is provided those directories will be included when looking up
// debug information that is separate from the binary.
func Attach(pid int, path, backend string, debugInfoDirs []string) (*Target, error) {
	var (
		p   proc.Process
		err error
	)

	switch backend {
	case "native":
		p, err = native.Attach(pid)
	case "lldb":
		p, err = betterGdbserialLaunchError(gdbserial.LLDBAttach(pid, path))
	case "default":
		if runtime.GOOS == "darwin" {
			p, err = betterGdbserialLaunchError(gdbserial.LLDBAttach(pid, path))
			break
		}
		p, err = native.Attach(pid)
	default:
		return nil, fmt.Errorf("unknown backend %q", backend)
	}
	if err != nil {
		return nil, err
	}
	t, err := New(p, runtime.GOOS, runtime.GOARCH, debugInfoDirs)
	if err != nil {
		p.Detach(false)
		return nil, err
	}
	return t, nil
}

// Launch will start a process with the given args and working directory using the
// backend specified.
// If foreground is true the process will have access to stdin.
// If debugInfoDirs is provided those directories will be included when looking up
// debug information that is separate from the binary.
func Launch(processArgs []string, wd string, foreground bool, backend string, debugInfoDirs []string) (*Target, error) {
	var (
		p   proc.Process
		err error
	)
	switch backend {
	case "native":
		p, err = native.Launch(processArgs, wd, foreground)
	case "lldb":
		p, err = betterGdbserialLaunchError(gdbserial.LLDBLaunch(processArgs, wd, foreground))
	case "rr":
		p, _, err = gdbserial.RecordAndReplay(processArgs, wd, false)
	case "default":
		if runtime.GOOS == "darwin" {
			p, err = betterGdbserialLaunchError(gdbserial.LLDBLaunch(processArgs, wd, foreground))
			break
		}
		p, err = native.Launch(processArgs, wd, foreground)
	default:
		return nil, fmt.Errorf("unknown backend %q", backend)
	}
	if err != nil {
		return nil, err
	}
	t, err := New(p, runtime.GOOS, runtime.GOARCH, debugInfoDirs)
	if err != nil {
		p.Detach(true)
		return nil, err
	}
	return t, nil
}

// OpenCoreOrRecording takes a path and opens either an RR recording or a core file
// depending on the backend selection.
// If a "rr" is not specified, it will attempt to open a core file.
// If opening a core file argv0 should be the path to the binary that produced the core file.
// If debugInfoDirs is provided those directories will be included when looking up
// debug information that is separate from the binary.
func OpenCoreOrRecording(backend, path, argv0 string, debugInfoDirs []string) (*Target, error) {
	var (
		p   proc.Process
		err error

		os   = runtime.GOOS
		arch = runtime.GOARCH
	)
	switch backend {
	case "rr":
		p, err = gdbserial.Replay(path, false, false)
	default:
		p, os, arch, err = core.OpenCore(path, argv0)
	}
	if err != nil {
		return nil, err
	}
	return New(p, os, arch, debugInfoDirs)
}

var errMacOSBackendUnavailable = errors.New("debugserver or lldb-server not found: install XCode's command line tools or lldb-server")

func betterGdbserialLaunchError(p proc.Process, err error) (proc.Process, error) {
	if runtime.GOOS != "darwin" {
		return p, err
	}
	if _, isUnavailable := err.(*gdbserial.ErrBackendUnavailable); !isUnavailable {
		return p, err
	}

	return p, errMacOSBackendUnavailable
}

// Initialize performs any setup that must be taken after
// we have successfully attached to the process we are going to debug.
// This includes any post-startup initialization the process must perform,
// as well as setting the default goroutine and creating some initial breakpoints
// that are set by default to catch when the process crashes or panics.
func (t *Target) Initialize() error {
	entry, err := t.Process.EntryPoint()
	if err != nil {
		return err
	}
	if err := t.bi.AddImage(t.Process.ExecutablePath(), entry); err != nil {
		return err
	}

	if err := t.Process.Initialize(); err != nil {
		return err
	}

	g, _ := proc.GetG(t.CurrentThread())
	t.SetSelectedGoroutine(g)

	createUnrecoveredPanicBreakpoint(t)
	createFatalThrowBreakpoint(t)
	return nil
}

// Detach will force the target to stop tracing the process.
// If kill is true the process will be killed during the detach.
func (t *Target) Detach(kill bool) error {
	return t.Process.Detach(kill)
}

// BinInfo returns information on the binary that is
// being debugged by this target.
// The information returned includes data gathered from
// parsing various sections of the binary.
// This is useful for getting line number translations, symbol
// information, and much more.
// See the documentation for BinaryInfo for more information.
func (t *Target) BinInfo() *proc.BinaryInfo {
	return t.bi
}

// Pid returns the PID of the process this target is attached to.
func (t *Target) Pid() int {
	return t.Process.Pid()
}

// SelectedGoroutine returns the goroutine which will be used as the default for
// operations if a specific goroutine is not specified.
// This is usually the goroutine that active on the thread which was stopped due to
// hitting a breakpoint.
// It could also be a goroutine the user selected.
func (t *Target) SelectedGoroutine() *proc.G {
	return t.Process.SelectedGoroutine()
}

// Recorded returns whether or not the target was recorded.
func (t *Target) Recorded() (bool, string) {
	return t.Process.Recorded()
}

// Restart allows you to restart the process from a given location.
// Only works when the selected backend is "rr".
func (t *Target) Restart(from string) error { return t.Process.Restart(from) }

// Direction controls whether execution goes forward or backward depending on the
// settings. This is only valid when using the "rr" backend.
func (t *Target) Direction(dir proc.Direction) error { return t.Process.Direction(dir) }

// When returns rr's current internal event number. Only valid when using the
// "rr" backend.
func (t *Target) When() (string, error) { return t.Process.When() }

// Checkpoint allow you to set a checkpoint at a certain location.
// Only valid with the "rr" backend.
func (t *Target) Checkpoint(where string) (int, error) { return t.Process.Checkpoint(where) }

// Checkpoints returns a list of currently active checkpoints.
// Only valid with the "rr" backend.
func (t *Target) Checkpoints() ([]proc.Checkpoint, error) { return t.Process.Checkpoints() }

// ClearCheckpoint will clear the checkpoint with the ID "n".
// Only valid with the "rr" backend.
func (t *Target) ClearCheckpoint(n int) error { return t.Process.ClearCheckpoint(n) }

// Valid returns true if the underlying process is in a state where
// it can be manipulated. This means it hasn't exited or been detached from.
func (t *Target) Valid() (bool, error) { return t.Process.Valid() }

// ResumeNotify specifies a channel that will be closed the next time
// Resume finishes resuming the underlying process.
func (t *Target) ResumeNotify(ch chan<- struct{}) { t.Process.ResumeNotify(ch) }

// ThreadList returns a list of threads in the underlying process.
func (t *Target) ThreadList() []proc.Thread { return t.Process.ThreadList() }

// FindThread returns the thread with the given ID.
func (t *Target) FindThread(id int) (proc.Thread, bool) { return t.Process.FindThread(id) }

// CurrentThread returns the default thread to be used for various operations.
// This is usually the last thread that threw an exception, however it could be
// a user set thread as well.
func (t *Target) CurrentThread() proc.Thread { return t.Process.CurrentThread() }

// Breakpoints returns a list of the active breakpoints that have been set in the
// underlying process.
func (t *Target) Breakpoints() *proc.BreakpointMap { return &t.breakpoints }

// RequestManualStop will attempt to stop the underlying process. Once stopped
// you may inspect process state.
func (t *Target) RequestManualStop() error { return t.Process.RequestManualStop() }

// SetBreakpoint sets a breakpoint at the provided address.
func (t *Target) SetBreakpoint(addr uint64, kind proc.BreakpointKind, cond ast.Expr) (*proc.Breakpoint, error) {
	if ok, err := t.Valid(); !ok {
		return nil, err
	}
	return t.Breakpoints().Set(addr, kind, cond, t.Process.WriteBreakpoint)
}

// ClearBreakpoint clears a breakpoint at the provided address.
func (t *Target) ClearBreakpoint(addr uint64) (*proc.Breakpoint, error) {
	if ok, err := t.Valid(); !ok {
		return nil, err
	}
	return t.Breakpoints().Clear(addr, t.Process.ClearBreakpointFn)
}

// StepInstruction will continue execution in the underlying process exactly 1 CPU instruction.
func (t *Target) StepInstruction() (err error) { return t.Process.StepInstruction() }

// SwitchThread will set the default thread to the one specified by "tid".
// That thread will then be used by default by any command that inspects process state.
func (t *Target) SwitchThread(tid int) error { return t.Process.SwitchThread(tid) }

// SwitchGoroutine will set the default goroutine to the one specified by "gid".
// This ennsures the selected goroutine remains active when continuing execution.
func (t *Target) SwitchGoroutine(gid int) error { return t.Process.SwitchGoroutine(gid) }

// ClearInternalBreakpoints will clear any non-user defined breakpoint.
func (t *Target) ClearInternalBreakpoints() error {
	return t.Breakpoints().ClearInternalBreakpoints(func(bp *proc.Breakpoint) error {
		if _, err := t.ClearBreakpoint(bp.Addr); err != nil {
			return err
		}
		for _, thread := range t.ThreadList() {
			if b := thread.Breakpoint(); b.Breakpoint == bp {
				thread.ClearCurrentBreakpointState()
			}
		}
		return nil
	})
}
