package dap

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/locspec"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/debugger"
	"github.com/sirupsen/logrus"
)

var (
	// ErrCanNotRestart is returned when the target cannot be restarted.
	// This is returned for targets that have been attached to, or when
	// debugging core files.
	ErrCanNotRestart = errors.New("can not restart this target")

	// ErrNotRecording is returned when StopRecording is called while the
	// debugger is not recording the target.
	ErrNotRecording = errors.New("debugger is not recording")

	// ErrCoreDumpInProgress is returned when a core dump is already in progress.
	ErrCoreDumpInProgress = errors.New("core dump in progress")

	// ErrCoreDumpNotSupported is returned when core dumping is not supported
	ErrCoreDumpNotSupported = errors.New("core dumping not supported")
)

// Debugger wraps debugger.Debugger for the DAP server.
// This layer allows the debug adapter to track its own
// running status, as well as handle breakpoints that should
// be processed and then resume execution (breakpoints on
// other goroutines while nexting and log points).
type Debugger struct {
	debugger     *debugger.Debugger
	log          *logrus.Entry
	logToConsole func(string)

	runningMutex sync.Mutex
	running      bool

	stopMutex sync.Mutex
}

// NewDapDebugger creates a new Debugger. ProcessArgs specify the commandline arguments for the
// new process.
func NewDapDebugger(config *debugger.Config, processArgs []string, log *logrus.Entry, logToConsole func(string)) (*Debugger, error) {
	debugger, err := debugger.New(config, processArgs)
	if err != nil {
		return nil, err
	}
	return &Debugger{
		debugger:     debugger,
		log:          log,
		logToConsole: logToConsole,
		running:      false,
	}, nil
}

// Launch will start a process with the given args and working directory.
func (d *Debugger) Launch(processArgs []string, wd string) (*proc.Target, error) {
	return d.debugger.Launch(processArgs, wd)
}

// Attach will attach to the process specified by 'pid'.
func (d *Debugger) Attach(pid int, path string) (*proc.Target, error) {
	return d.debugger.Attach(pid, path)
}

// ProcessPid returns the PID of the process
// the debugger is debugging.
func (d *Debugger) ProcessPid() int {
	return d.debugger.ProcessPid()
}

// LastModified returns the time that the process' executable was last
// modified.
func (d *Debugger) LastModified() time.Time {
	return d.debugger.LastModified()
}

// FunctionReturnLocations returns all return locations
// for the given function, a list of addresses corresponding
// to 'ret' or 'call runtime.deferreturn'.
func (d *Debugger) FunctionReturnLocations(fnName string) ([]uint64, error) {
	return d.debugger.FunctionReturnLocations(fnName)
}

// Detach detaches from the target process.
// If `kill` is true we will kill the process after
// detaching.
func (d *Debugger) Detach(kill bool) error {
	return d.debugger.Detach(kill)
}

// Restart will restart the target process, first killing
// and then exec'ing it again.
// If the target process is a recording it will restart it from the given
// position. If pos starts with 'c' it's a checkpoint ID, otherwise it's an
// event number. If resetArgs is true, newArgs will replace the process args.
func (d *Debugger) Restart(rerecord bool, pos string, resetArgs bool, newArgs []string, newRedirects [3]string, rebuild bool) ([]api.DiscardedBreakpoint, error) {
	return d.debugger.Restart(rerecord, pos, resetArgs, newArgs, newRedirects, rebuild)
}

// State returns the current state of the debugger.
func (d *Debugger) State(nowait bool) (*api.DebuggerState, error) {
	return d.debugger.State(nowait)
}

// CreateBreakpoint creates a breakpoint.
func (d *Debugger) CreateBreakpoint(requestedBp *api.Breakpoint) (*api.Breakpoint, error) {
	return d.debugger.CreateBreakpoint(requestedBp)
}

// AmendBreakpoint will update the breakpoint with the matching ID.
// It also enables or disables the breakpoint.
func (d *Debugger) AmendBreakpoint(amend *api.Breakpoint) error {
	return d.debugger.AmendBreakpoint(amend)
}

// CancelNext will clear internal breakpoints, thus cancelling the 'next',
// 'step' or 'stepout' operation.
func (d *Debugger) CancelNext() error {
	return d.debugger.CancelNext()
}

// ClearBreakpoint clears a breakpoint.
func (d *Debugger) ClearBreakpoint(requestedBp *api.Breakpoint) (*api.Breakpoint, error) {
	return d.debugger.ClearBreakpoint(requestedBp)
}

// Breakpoints returns the list of current breakpoints.
func (d *Debugger) Breakpoints() []*api.Breakpoint {
	return d.debugger.Breakpoints()
}

// FindBreakpoint returns the breakpoint specified by 'id'.
func (d *Debugger) FindBreakpoint(id int) *api.Breakpoint {
	return d.debugger.FindBreakpoint(id)
}

// FindBreakpointByName returns the breakpoint specified by 'name'
func (d *Debugger) FindBreakpointByName(name string) *api.Breakpoint {
	return d.debugger.FindBreakpointByName(name)
}

// CreateWatchpoint creates a watchpoint on the specified expression.
func (d *Debugger) CreateWatchpoint(goid, frame, deferredCall int, expr string, wtype api.WatchType) (*api.Breakpoint, error) {
	return d.debugger.CreateWatchpoint(goid, frame, deferredCall, expr, wtype)
}

// Threads returns the threads of the target process.
func (d *Debugger) Threads() ([]proc.Thread, error) {
	return d.debugger.Threads()
}

// FindThread returns the thread for the given 'id'.
func (d *Debugger) FindThread(id int) (proc.Thread, error) {
	return d.debugger.FindThread(id)
}

// FindGoroutine returns the goroutine for the given 'id'.
func (d *Debugger) FindGoroutine(id int) (*proc.G, error) {
	return d.debugger.FindGoroutine(id)
}

func (d *Debugger) setRunning(running bool) {
	d.runningMutex.Lock()
	d.running = running
	d.runningMutex.Unlock()
}

func (d *Debugger) IsRunning() bool {
	d.runningMutex.Lock()
	defer d.runningMutex.Unlock()
	return d.running
}

// halt is a helper function to allow dap.Debugger to make sure that a halt
// is processed. It is possible that this halt request will come in while a
// a breakpoint is being processed. If the debugger decides to resume execution
// after processing the breakpoint, the halt request would be skipped. Additional
// synchronization is required between "resume" and "halt" to make sure this does
// not happen.
func (d *Debugger) halt() (*api.DebuggerState, error) {
	d.stopMutex.Lock()
	defer d.stopMutex.Unlock()
	d.setRunning(false)
	return d.debugger.Command(&api.DebuggerCommand{Name: api.Halt}, nil)
}

func (d *Debugger) resume() (*api.DebuggerState, error) {
	resumeNotify := make(chan struct{}, 1)
	d.stopMutex.Lock()
	go func() {
		<-resumeNotify
		d.stopMutex.Unlock()
	}()

	if !d.IsRunning() {
		// A halt request came in so we need to CancelNext.
		if err := d.debugger.CancelNext(); err != nil {
			d.log.Error(err)
		}
		return d.debugger.Command(&api.DebuggerCommand{Name: api.Halt}, resumeNotify)
	}
	return d.debugger.Command(&api.DebuggerCommand{Name: api.Continue}, resumeNotify)
}

// Command handles commands which control the debugger lifecycle
func (d *Debugger) Command(command *api.DebuggerCommand, resumeNotify chan struct{}) (*api.DebuggerState, error) {
	if command.Name == api.Halt {
		return d.halt()
	}

	d.setRunning(true)
	defer d.setRunning(false)

	state, err := d.debugger.Command(command, resumeNotify)
	for {
		if !(state != nil && state.NextInProgress) {
			break
		}
		// If there is a NextInProgress, we want to notify the user that a breakpoint
		// was hit and then continue.
		if bp := state.CurrentThread.Breakpoint; bp != nil {
			msg := fmt.Sprintf("goroutine %d hit breakpoint (id: %d, loc: %s:%d) during %s", stoppedGoroutineID(state), bp.ID, bp.File, bp.Line, command.Name)
			d.log.Debugln(msg)
			d.logToConsole(msg)
		}
		state, err = d.resume()
	}
	return state, err
}

// Sources returns a list of the source files for target binary.
func (d *Debugger) Sources(filter string) ([]string, error) {
	return d.debugger.Sources(filter)
}

// Functions returns a list of functions in the target process.
func (d *Debugger) Functions(filter string) ([]string, error) {
	return d.debugger.Functions(filter)
}

// Types returns all type information in the binary.
func (d *Debugger) Types(filter string) ([]string, error) {
	return d.debugger.Types(filter)
}

// PackageVariables returns a list of package variables for the thread,
// optionally regexp filtered using regexp described in 'filter'.
func (d *Debugger) PackageVariables(filter string, cfg proc.LoadConfig) ([]*proc.Variable, error) {
	return d.debugger.PackageVariables(filter, cfg)
}

// ThreadRegisters returns registers of the specified thread.
func (d *Debugger) ThreadRegisters(threadID int, floatingPoint bool) (*op.DwarfRegisters, error) {
	return d.debugger.ThreadRegisters(threadID, floatingPoint)
}

// ScopeRegisters returns registers for the specified scope.
func (d *Debugger) ScopeRegisters(goid, frame, deferredCall int, floatingPoint bool) (*op.DwarfRegisters, error) {
	return d.debugger.ScopeRegisters(goid, frame, deferredCall, floatingPoint)
}

// DwarfRegisterToString returns the name and value representation of the given register.
func (d *Debugger) DwarfRegisterToString(i int, reg *op.DwarfRegister) (string, bool, string) {
	return d.debugger.DwarfRegisterToString(i, reg)
}

// LocalVariables returns a list of the local variables.
func (d *Debugger) LocalVariables(goid, frame, deferredCall int, cfg proc.LoadConfig) ([]*proc.Variable, error) {
	return d.debugger.LocalVariables(goid, frame, deferredCall, cfg)
}

// FunctionArguments returns the arguments to the current function.
func (d *Debugger) FunctionArguments(goid, frame, deferredCall int, cfg proc.LoadConfig) ([]*proc.Variable, error) {
	return d.debugger.FunctionArguments(goid, frame, deferredCall, cfg)
}

// Function returns the current function.
func (d *Debugger) Function(goid, frame, deferredCall int, cfg proc.LoadConfig) (*proc.Function, error) {
	return d.debugger.Function(goid, frame, deferredCall, cfg)
}

// EvalVariableInScope will attempt to evaluate the variable represented by 'symbol'
// in the scope provided.
func (d *Debugger) EvalVariableInScope(goid, frame, deferredCall int, symbol string, cfg proc.LoadConfig) (*proc.Variable, error) {
	return d.debugger.EvalVariableInScope(goid, frame, deferredCall, symbol, cfg)
}

// LoadResliced will attempt to 'reslice' a map, array or slice so that the values
// up to cfg.MaxArrayValues children are loaded starting from index start.
func (d *Debugger) LoadResliced(v *proc.Variable, start int, cfg proc.LoadConfig) (*proc.Variable, error) {
	d.LockTarget()
	defer d.UnlockTarget()
	return v.LoadResliced(start, cfg)
}

// SetVariableInScope will set the value of the variable represented by
// 'symbol' to the value given, in the given scope.
func (d *Debugger) SetVariableInScope(goid, frame, deferredCall int, symbol, value string) error {
	return d.debugger.SetVariableInScope(goid, frame, deferredCall, symbol, value)

}

// Goroutines will return a list of goroutines in the target process.
func (d *Debugger) Goroutines(start, count int) ([]*proc.G, int, error) {
	return d.debugger.Goroutines(start, count)
}

// Stacktrace returns a list of Stackframes for the given goroutine. The
// length of the returned list will be min(stack_len, depth).
// If 'full' is true, then local vars, function args, etc will be returned as well.
func (d *Debugger) Stacktrace(goroutineID, depth int, opts api.StacktraceOptions) ([]proc.Stackframe, error) {
	return d.debugger.Stacktrace(goroutineID, depth, opts)
}

// Ancestors returns the stacktraces for the ancestors of a goroutine.
func (d *Debugger) Ancestors(goroutineID, numAncestors, depth int) ([]api.Ancestor, error) {
	return d.debugger.Ancestors(goroutineID, numAncestors, depth)
}

// ConvertStacktrace converts a slice of proc.Stackframe into a slice of
// api.Stackframe, loading local variables and arguments of each frame if
// cfg is not nil.
func (d *Debugger) ConvertStacktrace(rawlocs []proc.Stackframe, cfg *proc.LoadConfig) ([]api.Stackframe, error) {
	return d.debugger.ConvertStacktrace(rawlocs, cfg)
}

// CurrentPackage returns the fully qualified name of the
// package corresponding to the function location of the
// current thread.
func (d *Debugger) CurrentPackage() (string, error) {
	return d.debugger.CurrentPackage()
}

// FindLocation will find the location specified by 'locStr'.
func (d *Debugger) FindLocation(goid, frame, deferredCall int, locStr string, includeNonExecutableLines bool, substitutePathRules [][2]string) ([]api.Location, error) {
	return d.debugger.FindLocation(goid, frame, deferredCall, locStr, includeNonExecutableLines, substitutePathRules)
}

// FindLocationSpec will find the location specified by 'locStr' and 'locSpec'.
// 'locSpec' should be the result of calling 'locspec.Parse(locStr)'. 'locStr'
// is also passed, because it made be used to broaden the search criteria, if
// the parsed result did not find anything.
func (d *Debugger) FindLocationSpec(goid, frame, deferredCall int, locStr string, locSpec locspec.LocationSpec, includeNonExecutableLines bool, substitutePathRules [][2]string) ([]api.Location, error) {
	return d.debugger.FindLocationSpec(goid, frame, deferredCall, locStr, locSpec, includeNonExecutableLines, substitutePathRules)
}

// Disassemble code between startPC and endPC.
// if endPC == 0 it will find the function containing startPC and disassemble the whole function.
func (d *Debugger) Disassemble(goroutineID int, addr1, addr2 uint64) ([]proc.AsmInstruction, error) {
	return d.debugger.Disassemble(goroutineID, addr1, addr2)
}

func (d *Debugger) AsmInstructionText(inst *proc.AsmInstruction, flavour proc.AssemblyFlavour) string {
	return d.debugger.AsmInstructionText(inst, flavour)
}

// Recorded returns true if the target is a recording.
func (d *Debugger) Recorded() (recorded bool, tracedir string) {
	return d.debugger.Recorded()
}

// FindThreadReturnValues returns the return values of the function that
// the thread of the given 'id' just stepped out of.
func (d *Debugger) FindThreadReturnValues(id int, cfg proc.LoadConfig) ([]*proc.Variable, error) {
	return d.debugger.FindThreadReturnValues(id, cfg)
}

// Checkpoint will set a checkpoint specified by the locspec.
func (d *Debugger) Checkpoint(where string) (int, error) {
	return d.debugger.Checkpoint(where)
}

// Checkpoints will return a list of checkpoints.
func (d *Debugger) Checkpoints() ([]proc.Checkpoint, error) {
	return d.debugger.Checkpoints()
}

// ClearCheckpoint will clear the checkpoint of the given ID.
func (d *Debugger) ClearCheckpoint(id int) error {
	return d.debugger.ClearCheckpoint(id)
}

// ListDynamicLibraries returns a list of loaded dynamic libraries.
func (d *Debugger) ListDynamicLibraries() []*proc.Image {
	return d.debugger.ListDynamicLibraries()
}

// ExamineMemory returns the raw memory stored at the given address.
// The amount of data to be read is specified by length.
// This function will return an error if it reads less than `length` bytes.
func (d *Debugger) ExamineMemory(address uint64, length int) ([]byte, error) {
	return d.debugger.ExamineMemory(address, length)
}

func (d *Debugger) GetVersion(out *api.GetVersionOut) error {
	return d.debugger.GetVersion(out)
}

// ListPackagesBuildInfo returns the list of packages used by the program along with
// the directory where each package was compiled and optionally the list of
// files constituting the package.
func (d *Debugger) ListPackagesBuildInfo(includeFiles bool) []*proc.PackageBuildInfo {
	return d.debugger.ListPackagesBuildInfo(includeFiles)
}

// StopRecording stops a recording (if one is in progress)
func (d *Debugger) StopRecording() error {
	return d.debugger.StopRecording()
}

// StopReason returns the reason the reason why the target process is stopped.
// A process could be stopped for multiple simultaneous reasons, in which
// case only one will be reported.
func (d *Debugger) StopReason() proc.StopReason {
	return d.debugger.StopReason()
}

// LockTarget acquires the target mutex.
func (d *Debugger) LockTarget() {
	d.debugger.LockTarget()
}

// UnlockTarget releases the target mutex.
func (d *Debugger) UnlockTarget() {
	d.debugger.UnlockTarget()
}

// DumpStart starts a core dump to dest.
func (d *Debugger) DumpStart(dest string) error {
	return d.debugger.DumpStart(dest)
}

// DumpWait waits for the dump to finish, or for the duration of wait.
// Returns the state of the dump.
// If wait == 0 returns immediately.
func (d *Debugger) DumpWait(wait time.Duration) *proc.DumpState {
	return d.debugger.DumpWait(wait)
}

// DumpCancel canels a dump in progress
func (d *Debugger) DumpCancel() error {
	return d.debugger.DumpCancel()
}
