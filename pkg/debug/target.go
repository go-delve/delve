package debug

import (
	"errors"
	"fmt"
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
}

// New returns an initialized Target.
func New(p proc.Process, os, arch string, debugInfoDirs []string) (*Target, error) {
	bi := proc.NewBinaryInfo(os, arch, debugInfoDirs)
	t := &Target{
		Process: p,
		bi:      bi,
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
