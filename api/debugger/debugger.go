package debugger

import (
	"debug/gosym"
	"errors"
	"fmt"
	"go/parser"
	"log"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/derekparker/delve/api/types"
	"github.com/derekparker/delve/proc"
)

// Debugger service.
//
// Debugger provides a higher level of
// abstraction over proc.Process.
// It handles converting from internal types to
// the types expected by clients. It also handles
// functionality needed by clients, but not needed in
// lower lever packages such as proc.
type Debugger struct {
	config       *Config
	processMutex sync.Mutex
	process      *proc.Process
}

// Config provides the configuration to start a Debugger.
//
// Only one of ProcessArgs or AttachPid should be specified. If ProcessArgs is
// provided, a new process will be launched. Otherwise, the debugger will try
// to attach to an existing process with AttachPid.
type Config struct {
	// ProcessArgs are the arguments to launch a new process.
	ProcessArgs []string
	// AttachPid is the PID of an existing process to which the debugger should
	// attach.
	AttachPid int
}

// New creates a new Debugger.
func New(config *Config) (*Debugger, error) {
	d := &Debugger{
		config: config,
	}

	// Create the process by either attaching or launching.
	if d.config.AttachPid > 0 {
		log.Printf("attaching to pid %d", d.config.AttachPid)
		p, err := proc.Attach(d.config.AttachPid)
		if err != nil {
			return nil, attachErrorMessage(d.config.AttachPid, err)
		}
		d.process = p
	} else {
		log.Printf("launching process with args: %v", d.config.ProcessArgs)
		p, err := proc.Launch(d.config.ProcessArgs)
		if err != nil {
			return nil, fmt.Errorf("could not launch process: %s", err)
		}
		d.process = p
	}
	return d, nil
}

// ProcessPid returns the PID of the process
// the debugger is debugging.
func (d *Debugger) ProcessPid() int {
	return d.process.Pid
}

// Detach detaches from the target process.
// If `kill` is true we will kill the process after
// detaching.
func (d *Debugger) Detach(kill bool) error {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	return d.detach(kill)
}

func (d *Debugger) detach(kill bool) error {
	if d.config.AttachPid != 0 {
		return proc.Detach(d.process, kill)
	}
	return proc.Kill(d.process)
}

// Restart will restart the target process, first killing
// and then exec'ing it again.
func (d *Debugger) Restart() error {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	if !d.process.Exited() {
		if d.process.Running() {
			proc.Halt(d.process)
		}
		// Ensure the process is in a PTRACE_STOP.
		if err := stopProcess(d.ProcessPid()); err != nil {
			return err
		}
		if err := d.detach(true); err != nil {
			return err
		}
	}
	p, err := proc.Launch(d.config.ProcessArgs)
	if err != nil {
		return fmt.Errorf("could not launch process: %s", err)
	}
	for _, oldBp := range d.breakpoints() {
		if oldBp.ID < 0 {
			continue
		}
		newBp, err := proc.SetBreakpoint(p, oldBp.Addr)
		if err != nil {
			return err
		}
		if err := copyBreakpointInfo(newBp, oldBp); err != nil {
			return err
		}
	}
	d.process = p
	return nil
}

// State returns the current state of the debugger.
func (d *Debugger) State() (*types.DebuggerState, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	return d.state()
}

func (d *Debugger) state() (*types.DebuggerState, error) {
	if d.process.Exited() {
		return nil, proc.ProcessExitedError{Pid: d.ProcessPid()}
	}

	var (
		state     *types.DebuggerState
		goroutine *types.Goroutine
	)

	if d.process.SelectedGoroutine != nil {
		goroutine = types.ConvertGoroutine(d.process.SelectedGoroutine)
	}

	state = &types.DebuggerState{
		SelectedGoroutine: goroutine,
		Exited:            d.process.Exited(),
	}

	for i := range d.process.Threads {
		th := types.ConvertThread(d.process.Threads[i])
		state.Threads = append(state.Threads, th)
		if i == d.process.CurrentThread.ID {
			state.CurrentThread = th
		}
	}

	for _, bp := range d.process.Breakpoints {
		if bp.Temp {
			state.NextInProgress = true
			break
		}
	}

	return state, nil
}

// CreateBreakpoint creates a breakpoint.
func (d *Debugger) CreateBreakpoint(requestedBp *types.Breakpoint) (*types.Breakpoint, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	var (
		createdBp *types.Breakpoint
		addr      uint64
		err       error
	)

	if requestedBp.Name != "" {
		if err = types.ValidBreakpointName(requestedBp.Name); err != nil {
			return nil, err
		}
		if d.findBreakpointByName(requestedBp.Name) != nil {
			return nil, errors.New("breakpoint name already exists")
		}
	}

	switch {
	case len(requestedBp.File) > 0:
		fileName := requestedBp.File
		if runtime.GOOS == "windows" {
			// Accept fileName which is case-insensitive and slash-insensitive match
			fileNameNormalized := strings.ToLower(filepath.ToSlash(fileName))
			for symFile := range d.process.Dwarf.Files() {
				if fileNameNormalized == strings.ToLower(filepath.ToSlash(symFile)) {
					fileName = symFile
					break
				}
			}
		}
		addr, err = proc.FindFileLocation(d.process, fileName, requestedBp.Line)
	case len(requestedBp.FunctionName) > 0:
		if requestedBp.Line >= 0 {
			addr, err = proc.FindFunctionLocation(d.process, requestedBp.FunctionName, false, requestedBp.Line)
		} else {
			addr, err = proc.FindFunctionLocation(d.process, requestedBp.FunctionName, true, 0)
		}
	default:
		addr = requestedBp.Addr
	}

	if err != nil {
		return nil, err
	}

	bp, err := proc.SetBreakpoint(d.process, addr)
	if err != nil {
		return nil, err
	}
	if err := copyBreakpointInfo(bp, requestedBp); err != nil {
		if _, err1 := proc.ClearBreakpoint(d.process, bp.Addr); err1 != nil {
			err = fmt.Errorf("error while creating breakpoint: %v, additionally the breakpoint could not be properly rolled back: %v", err, err1)
		}
		return nil, err
	}
	createdBp = types.ConvertBreakpoint(bp)
	log.Printf("created breakpoint: %#v", createdBp)
	return createdBp, nil
}

func (d *Debugger) AmendBreakpoint(amend *types.Breakpoint) error {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	original := d.findBreakpoint(amend.ID)
	if original == nil {
		return fmt.Errorf("no breakpoint with ID %d", amend.ID)
	}
	if err := types.ValidBreakpointName(amend.Name); err != nil {
		return err
	}
	return copyBreakpointInfo(original, amend)
}

func (d *Debugger) CancelNext() error {
	return proc.ClearTempBreakpoints(d.process)
}

func copyBreakpointInfo(bp *proc.Breakpoint, requested *types.Breakpoint) (err error) {
	bp.Name = requested.Name
	bp.Tracepoint = requested.Tracepoint
	bp.Goroutine = requested.Goroutine
	bp.Stacktrace = requested.Stacktrace
	bp.Variables = requested.Variables
	bp.LoadArgs = types.LoadConfigToProc(requested.LoadArgs)
	bp.LoadLocals = types.LoadConfigToProc(requested.LoadLocals)
	bp.Cond = nil
	if requested.Cond != "" {
		bp.Cond, err = parser.ParseExpr(requested.Cond)
	}
	return err
}

// ClearBreakpoint clears a breakpoint.
func (d *Debugger) ClearBreakpoint(requestedBp *types.Breakpoint) (*types.Breakpoint, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	var clearedBp *types.Breakpoint
	bp, err := proc.ClearBreakpoint(d.process, requestedBp.Addr)
	if err != nil {
		return nil, fmt.Errorf("Can't clear breakpoint @%x: %s", requestedBp.Addr, err)
	}
	clearedBp = types.ConvertBreakpoint(bp)
	log.Printf("cleared breakpoint: %#v", clearedBp)
	return clearedBp, err
}

// Breakpoints returns the list of current breakpoints.
func (d *Debugger) Breakpoints() []*types.Breakpoint {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	return d.breakpoints()
}

func (d *Debugger) breakpoints() []*types.Breakpoint {
	bps := []*types.Breakpoint{}
	for _, bp := range d.process.Breakpoints {
		if bp.Temp {
			continue
		}
		bps = append(bps, types.ConvertBreakpoint(bp))
	}
	return bps
}

// FindBreakpoint returns the breakpoint specified by 'id'.
func (d *Debugger) FindBreakpoint(id int) *types.Breakpoint {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	bp := d.findBreakpoint(id)
	if bp == nil {
		return nil
	}
	return types.ConvertBreakpoint(bp)
}

func (d *Debugger) findBreakpoint(id int) *proc.Breakpoint {
	for _, bp := range d.process.Breakpoints {
		if bp.ID == id {
			return bp
		}
	}
	return nil
}

// FindBreakpointByName returns the breakpoint specified by 'name'
func (d *Debugger) FindBreakpointByName(name string) *types.Breakpoint {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()
	return d.findBreakpointByName(name)
}

func (d *Debugger) findBreakpointByName(name string) *types.Breakpoint {
	for _, bp := range d.breakpoints() {
		if bp.Name == name {
			return bp
		}
	}
	return nil
}

// Threads returns the threads of the target process.
func (d *Debugger) Threads() ([]*types.Thread, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	if d.process.Exited() {
		return nil, &proc.ProcessExitedError{}
	}
	threads := []*types.Thread{}
	for _, th := range d.process.Threads {
		threads = append(threads, types.ConvertThread(th))
	}
	return threads, nil
}

// FindThread returns the thread for the given 'id'.
func (d *Debugger) FindThread(id int) (*types.Thread, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	if d.process.Exited() {
		return nil, &proc.ProcessExitedError{}
	}

	for _, th := range d.process.Threads {
		if th.ID == id {
			return types.ConvertThread(th), nil
		}
	}
	return nil, nil
}

// Command handles commands which control the debugger lifecycle
func (d *Debugger) Command(command *types.DebuggerCommand) (*types.DebuggerState, error) {
	var err error

	if command.Name == types.Halt {
		// RequestManualStop does not invoke any ptrace syscalls, so it's safe to
		// access the process directly.
		log.Print("halting")
		err = proc.Stop(d.process)
	}

	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	switch command.Name {
	case types.Continue:
		log.Print("continuing")
		err = proc.Continue(d.process)
		if err != nil {
			if exitedErr, exited := err.(proc.ProcessExitedError); exited {
				state := &types.DebuggerState{}
				state.Exited = true
				state.ExitStatus = exitedErr.Status
				state.Err = errors.New(exitedErr.Error())
				return state, nil
			}
			return nil, err
		}
		state, stateErr := d.state()
		if stateErr != nil {
			return state, stateErr
		}
		err = d.collectBreakpointInformation(state)
		return state, err

	case types.Next:
		log.Print("nexting")
		err = proc.Next(d.process)
	case types.Step:
		log.Print("stepping")
		err = proc.Step(d.process)
	case types.StepInstruction:
		log.Print("single stepping")
		err = proc.StepInstruction(d.process)
	case types.SwitchThread:
		log.Printf("switching to thread %d", command.ThreadID)
		th, ok := d.process.Threads[command.ThreadID]
		if !ok {
			return nil, fmt.Errorf("invalid thread ID %d", command.ThreadID)
		}
		err = d.process.SetActiveThread(th)
	case types.SwitchGoroutine:
		log.Printf("switching to goroutine %d", command.GoroutineID)
		err = d.process.SwitchGoroutine(command.GoroutineID)
	case types.Halt:
		// RequestManualStop already called
	}
	if err != nil {
		return nil, err
	}
	return d.state()
}

func (d *Debugger) collectBreakpointInformation(state *types.DebuggerState) error {
	if state == nil {
		return nil
	}

	for i := range state.Threads {
		if state.Threads[i].Breakpoint == nil {
			continue
		}

		bp := state.Threads[i].Breakpoint
		bpi := &types.BreakpointInfo{}
		state.Threads[i].BreakpointInfo = bpi

		if bp.Goroutine {
			g, err := d.process.CurrentThread.GetG()
			if err != nil {
				return err
			}
			bpi.Goroutine = types.ConvertGoroutine(g)
		}

		if bp.Stacktrace > 0 {
			rawlocs, err := d.process.CurrentThread.Stacktrace(bp.Stacktrace)
			if err != nil {
				return err
			}
			bpi.Stacktrace, err = d.convertStacktrace(rawlocs, nil)
			if err != nil {
				return err
			}
		}

		s, err := d.process.Threads[state.Threads[i].ID].Scope()
		if err != nil {
			return err
		}

		if len(bp.Variables) > 0 {
			bpi.Variables = make([]types.Variable, len(bp.Variables))
		}
		for i := range bp.Variables {
			v, err := s.EvalVariable(bp.Variables[i], proc.LoadConfig{true, 1, 64, 64, -1})
			if err != nil {
				return err
			}
			bpi.Variables[i] = *types.ConvertVar(v)
		}
		if bp.LoadArgs != nil {
			if vars, err := s.FunctionArguments(*types.LoadConfigToProc(bp.LoadArgs)); err == nil {
				bpi.Arguments = convertVars(vars)
			}
		}
		if bp.LoadLocals != nil {
			if locals, err := s.LocalVariables(*types.LoadConfigToProc(bp.LoadLocals)); err == nil {
				bpi.Locals = convertVars(locals)
			}
		}
	}

	return nil
}

// Sources returns a list of the source files for target binary.
func (d *Debugger) Sources(filter string) ([]string, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	files := []string{}
	for f := range d.process.Dwarf.Files() {
		if regex.Match([]byte(f)) {
			files = append(files, f)
		}
	}
	return files, nil
}

// Functions returns a list of functions in the target process.
func (d *Debugger) Functions(filter string) ([]string, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	return regexFilterFuncs(filter, d.process.Dwarf.Funcs())
}

func (d *Debugger) Types(filter string) ([]string, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	types, err := d.process.Dwarf.TypeList()
	if err != nil {
		return nil, err
	}

	r := make([]string, 0, len(types))
	for _, typ := range types {
		if regex.Match([]byte(typ)) {
			r = append(r, typ)
		}
	}

	return r, nil
}

func regexFilterFuncs(filter string, allFuncs []gosym.Func) ([]string, error) {
	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	funcs := []string{}
	for _, f := range allFuncs {
		if f.Sym != nil && regex.Match([]byte(f.Name)) {
			funcs = append(funcs, f.Name)
		}
	}
	return funcs, nil
}

// PackageVariables returns a list of package variables for the thread,
// optionally regexp filtered using regexp described in 'filter'.
func (d *Debugger) PackageVariables(threadID int, filter string, cfg proc.LoadConfig) ([]types.Variable, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	vars := []types.Variable{}
	thread, found := d.process.Threads[threadID]
	if !found {
		return nil, fmt.Errorf("couldn't find thread %d", threadID)
	}
	scope, err := thread.Scope()
	if err != nil {
		return nil, err
	}
	pv, err := scope.PackageVariables(cfg)
	if err != nil {
		return nil, err
	}
	for _, v := range pv {
		if regex.Match([]byte(v.Name)) {
			vars = append(vars, *types.ConvertVar(v))
		}
	}
	return vars, err
}

// Registers returns string representation of the CPU registers.
func (d *Debugger) Registers(threadID int) (string, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	thread, found := d.process.Threads[threadID]
	if !found {
		return "", fmt.Errorf("couldn't find thread %d", threadID)
	}
	regs, err := thread.Registers()
	if err != nil {
		return "", err
	}
	return regs.String(), err
}

func convertVars(pv []*proc.Variable) []types.Variable {
	vars := make([]types.Variable, 0, len(pv))
	for _, v := range pv {
		vars = append(vars, *types.ConvertVar(v))
	}
	return vars
}

// LocalVariables returns a list of the local variables.
func (d *Debugger) LocalVariables(scope types.EvalScope, cfg proc.LoadConfig) ([]types.Variable, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.process, scope.GoroutineID, scope.Frame)
	if err != nil {
		return nil, err
	}
	pv, err := s.LocalVariables(cfg)
	if err != nil {
		return nil, err
	}
	return convertVars(pv), err
}

// FunctionArguments returns the arguments to the current function.
func (d *Debugger) FunctionArguments(scope types.EvalScope, cfg proc.LoadConfig) ([]types.Variable, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.process, scope.GoroutineID, scope.Frame)
	if err != nil {
		return nil, err
	}
	pv, err := s.FunctionArguments(cfg)
	if err != nil {
		return nil, err
	}
	return convertVars(pv), nil
}

// EvalVariableInScope will attempt to evaluate the variable represented by 'symbol'
// in the scope provided.
func (d *Debugger) EvalVariableInScope(scope types.EvalScope, symbol string, cfg proc.LoadConfig) (*types.Variable, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.process, scope.GoroutineID, scope.Frame)
	if err != nil {
		return nil, err
	}
	v, err := s.EvalVariable(symbol, cfg)
	if err != nil {
		return nil, err
	}
	return types.ConvertVar(v), err
}

// SetVariableInScope will set the value of the variable represented by
// 'symbol' to the value given, in the given scope.
func (d *Debugger) SetVariableInScope(scope types.EvalScope, symbol, value string) error {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	s, err := proc.ConvertEvalScope(d.process, scope.GoroutineID, scope.Frame)
	if err != nil {
		return err
	}
	return s.SetVariable(symbol, value)
}

// Goroutines will return a list of goroutines in the target process.
func (d *Debugger) Goroutines() ([]*types.Goroutine, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	goroutines := []*types.Goroutine{}
	gs, err := d.process.GoroutinesInfo()
	if err != nil {
		return nil, err
	}
	for _, g := range gs {
		goroutines = append(goroutines, types.ConvertGoroutine(g))
	}
	return goroutines, err
}

// Stacktrace returns a list of Stackframes for the given goroutine. The
// length of the returned list will be min(stack_len, depth).
// If 'full' is true, then local vars, function args, etc will be returned as well.
func (d *Debugger) Stacktrace(goroutineID, depth int, cfg *proc.LoadConfig) ([]types.Stackframe, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	var rawlocs []proc.Stackframe

	g, err := d.process.FindGoroutine(goroutineID)
	if err != nil {
		return nil, err
	}

	if g == nil {
		rawlocs, err = d.process.CurrentThread.Stacktrace(depth)
	} else {
		rawlocs, err = g.Stacktrace(depth)
	}
	if err != nil {
		return nil, err
	}

	return d.convertStacktrace(rawlocs, cfg)
}

func (d *Debugger) convertStacktrace(rawlocs []proc.Stackframe, cfg *proc.LoadConfig) ([]types.Stackframe, error) {
	locations := make([]types.Stackframe, 0, len(rawlocs))
	for i := range rawlocs {
		frame := types.Stackframe{Location: types.ConvertLocation(rawlocs[i].Call)}
		if cfg != nil {
			var err error
			scope := rawlocs[i].Scope(d.process.CurrentThread)
			locals, err := scope.LocalVariables(*cfg)
			if err != nil {
				return nil, err
			}
			arguments, err := scope.FunctionArguments(*cfg)
			if err != nil {
				return nil, err
			}

			frame.Locals = convertVars(locals)
			frame.Arguments = convertVars(arguments)
		}
		locations = append(locations, frame)
	}

	return locations, nil
}

// FindLocation will find the location specified by 'locStr'.
func (d *Debugger) FindLocation(scope types.EvalScope, locStr string) ([]types.Location, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	loc, err := parseLocationSpec(locStr)
	if err != nil {
		return nil, err
	}

	s, _ := proc.ConvertEvalScope(d.process, scope.GoroutineID, scope.Frame)

	locs, err := loc.Find(d, s, locStr)
	for i := range locs {
		file, line, fn := d.process.Dwarf.PCToLine(locs[i].PC)
		locs[i].File = file
		locs[i].Line = line
		locs[i].Function = types.ConvertFunction(fn)
	}
	return locs, err
}

// Disassembles code between startPC and endPC
// if endPC == 0 it will find the function containing startPC and disassemble the whole function
func (d *Debugger) Disassemble(scope types.EvalScope, startPC, endPC uint64, flavour types.AssemblyFlavour) (types.AsmInstructions, error) {
	d.processMutex.Lock()
	defer d.processMutex.Unlock()

	if endPC == 0 {
		_, _, fn := d.process.Dwarf.PCToLine(startPC)
		if fn == nil {
			return nil, fmt.Errorf("Address 0x%x does not belong to any function", startPC)
		}
		startPC = fn.Entry
		endPC = fn.End
	}

	currentGoroutine := true
	thread := d.process.CurrentThread

	if s, err := proc.ConvertEvalScope(d.process, scope.GoroutineID, scope.Frame); err == nil {
		thread = s.Thread
		if scope.GoroutineID != -1 {
			g, _ := s.Thread.GetG()
			if g == nil || g.ID != scope.GoroutineID {
				currentGoroutine = false
			}
		}
	}

	insts, err := proc.Disassemble(thread, startPC, endPC, currentGoroutine)
	if err != nil {
		return nil, err
	}
	disass := make(types.AsmInstructions, len(insts))

	for i := range insts {
		disass[i] = types.ConvertAsmInstruction(insts[i], insts[i].Text(proc.AssemblyFlavour(flavour)))
	}

	return disass, nil
}
