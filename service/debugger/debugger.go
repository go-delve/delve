package debugger

import (
	"fmt"
	"log"
	"regexp"
	"runtime"
	"strconv"

	"github.com/derekparker/delve/proctl"
	"github.com/derekparker/delve/service/api"
)

// Debugger provides a thread-safe DebuggedProcess service. Instances of
// Debugger can be exposed by other services.
type Debugger struct {
	config     *Config
	process    *proctl.DebuggedProcess
	processOps chan func(*proctl.DebuggedProcess)
	stop       chan stopSignal
	running    bool
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

// stopSignal is used to stop the debugger.
type stopSignal struct {
	// KillProcess indicates whether to kill the debugee following detachment.
	KillProcess bool
}

// New creates a new Debugger.
func New(config *Config) *Debugger {
	debugger := &Debugger{
		processOps: make(chan func(*proctl.DebuggedProcess)),
		config:     config,
		stop:       make(chan stopSignal),
	}
	return debugger
}

// withProcess facilitates thread-safe access to the DebuggedProcess. Most
// interaction with DebuggedProcess should occur via calls to withProcess[1],
// and the functions placed on the processOps channel should be consumed and
// executed from the same thread as the DebuggedProcess.
//
// This is convenient because it allows things like HTTP handlers in
// goroutines to work with the DebuggedProcess with synchronous semantics.
//
// [1] There are some exceptional cases where direct access is okay; for
// instance, when performing an operation like halt which merely sends a
// signal to the process rather than performing something like a ptrace
// operation.
func (d *Debugger) withProcess(f func(*proctl.DebuggedProcess) error) error {
	if !d.running {
		return fmt.Errorf("debugger isn't running")
	}

	result := make(chan error)
	d.processOps <- func(proc *proctl.DebuggedProcess) {
		result <- f(proc)
	}
	return <-result
}

// Run starts debugging a process until Detach is called.
func (d *Debugger) Run() error {
	// We must ensure here that we are running on the same thread during
	// the execution of dbg. This is due to the fact that ptrace(2) expects
	// all commands after PTRACE_ATTACH to come from the same thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	d.running = true
	defer func() { d.running = false }()

	// Create the process by either attaching or launching.
	if d.config.AttachPid > 0 {
		log.Printf("attaching to pid %d", d.config.AttachPid)
		p, err := proctl.Attach(d.config.AttachPid)
		if err != nil {
			return fmt.Errorf("couldn't attach to pid %d: %s", d.config.AttachPid, err)
		}
		d.process = p
	} else {
		log.Printf("launching process with args: %v", d.config.ProcessArgs)
		p, err := proctl.Launch(d.config.ProcessArgs)
		if err != nil {
			return fmt.Errorf("couldn't launch process: %s", err)
		}
		d.process = p
	}

	// Handle access to the process from the current thread.
	log.Print("debugger started")
	for {
		select {
		case f := <-d.processOps:
			// Execute the function
			f(d.process)
		case s := <-d.stop:
			// Handle shutdown
			log.Print("debugger is stopping")

			// Clear breakpoints
			bps := []*proctl.BreakPoint{}
			for _, bp := range d.process.BreakPoints {
				if bp != nil {
					bps = append(bps, bp)
				}
			}
			for _, bp := range d.process.HWBreakPoints {
				if bp != nil {
					bps = append(bps, bp)
				}
			}
			for _, bp := range bps {
				_, err := d.process.Clear(bp.Addr)
				if err != nil {
					log.Printf("warning: couldn't clear breakpoint @ %#v: %s", bp.Addr, err)
				} else {
					log.Printf("cleared breakpoint @ %#v", bp.Addr)
				}
			}

			// Kill the process if requested
			if s.KillProcess {
				if err := d.process.Detach(); err == nil {
					log.Print("killed process")
				} else {
					log.Printf("couldn't kill process: %s", err)
				}
			} else {
				// Detach
				if !d.process.Exited() {
					if err := proctl.PtraceDetach(d.process.Pid, 0); err == nil {
						log.Print("detached from process")
					} else {
						log.Printf("couldn't detach from process: %s", err)
					}
				}
			}

			return nil
		}
	}
}

// Detach stops the debugger.
func (d *Debugger) Detach(kill bool) error {
	if !d.running {
		return fmt.Errorf("debugger isn't running")
	}

	d.stop <- stopSignal{KillProcess: kill}
	return nil
}

func (d *Debugger) State() (*api.DebuggerState, error) {
	var state *api.DebuggerState

	err := d.withProcess(func(p *proctl.DebuggedProcess) error {
		var thread *api.Thread
		th := p.CurrentThread
		if th != nil {
			thread = convertThread(th)
		}

		var breakpoint *api.BreakPoint
		bp := p.CurrentBreakpoint()
		if bp != nil {
			breakpoint = convertBreakPoint(bp)
		}

		lc := p.ListContext
		if lc == nil {
			log.Println("*** Nil list context ***")
			list := new(proctl.List)
			list.FunctionName = ""
			list.File = ""
			list.Line = 0
			list.Addr = 0
			list.ListSize = 10
			list.Direction = true
			p.ListContext = list
			lc = p.ListContext
		}

		var list *api.List
		if lc != nil {
			list = convertList(lc)
		}

		state = &api.DebuggerState{
			BreakPoint:    breakpoint,
			CurrentThread: thread,
			List:          list,
			Exited:        p.Exited(),
		}
		return nil
	})

	return state, err
}

func (d *Debugger) CreateBreakPoint(location *api.Arguments) (*api.BreakPoint, error) {
	var createdBp *api.BreakPoint
	err := d.withProcess(func(p *proctl.DebuggedProcess) error {
		var loc string = ""

		if len(location.Args) == 1 {
			loc = location.Args[0]
		}
		bp, breakError := p.BreakByLocation(loc)
		if breakError != nil {
			return breakError
		}
		createdBp = convertBreakPoint(bp)
		log.Printf("created breakpoint: %#v", createdBp)
		return nil
	})
	return createdBp, err
}

func (d *Debugger) ClearBreakPoint(requestedBp *api.BreakPoint) (*api.BreakPoint, error) {
	var clearedBp *api.BreakPoint
	err := d.withProcess(func(p *proctl.DebuggedProcess) error {
		bp, err := p.Clear(requestedBp.Addr)
		if err != nil {
			return fmt.Errorf("Can't clear breakpoint @%x: %s", requestedBp.Addr, err)
		}
		clearedBp = convertBreakPoint(bp)
		log.Printf("cleared breakpoint: %#v", clearedBp)
		return nil
	})
	return clearedBp, err
}

func (d *Debugger) BreakPoints() []*api.BreakPoint {
	bps := []*api.BreakPoint{}
	d.withProcess(func(p *proctl.DebuggedProcess) error {
		for _, bp := range p.HWBreakPoints {
			if bp == nil {
				continue
			}
			bps = append(bps, convertBreakPoint(bp))
		}

		for _, bp := range p.BreakPoints {
			if bp.Temp {
				continue
			}
			bps = append(bps, convertBreakPoint(bp))
		}
		return nil
	})
	return bps
}

func (d *Debugger) FindBreakPoint(id int) *api.BreakPoint {
	for _, bp := range d.BreakPoints() {
		if bp.ID == id {
			return bp
		}
	}
	return nil
}

func (d *Debugger) Threads() []*api.Thread {
	threads := []*api.Thread{}
	d.withProcess(func(p *proctl.DebuggedProcess) error {
		for _, th := range p.Threads {
			threads = append(threads, convertThread(th))
		}
		return nil
	})
	return threads
}

func (d *Debugger) FindThread(id int) *api.Thread {
	for _, thread := range d.Threads() {
		if thread.ID == id {
			return thread
		}
	}
	return nil
}

// Command handles commands which control the debugger lifecycle. Like other
// debugger operations, these are executed one at a time as part of the
// process operation pipeline.
//
// The one exception is the Halt command, which can be executed concurrently
// with any operation.
func (d *Debugger) Command(command *api.DebuggerCommand) (*api.DebuggerState, error) {
	var err error
	switch command.Name {
	case api.Continue:
		err = d.withProcess(func(p *proctl.DebuggedProcess) error {
			log.Print("continuing")
			e := p.Continue()
			return e
		})
	case api.Next:
		err = d.withProcess(func(p *proctl.DebuggedProcess) error {
			log.Print("nexting")
			return p.Next()
		})
	case api.Step:
		err = d.withProcess(func(p *proctl.DebuggedProcess) error {
			log.Print("stepping")
			return p.Step()
		})
	case api.SwitchThread:
		err = d.withProcess(func(p *proctl.DebuggedProcess) error {
			log.Printf("switching to thread %d", command.ThreadID)
			return p.SwitchThread(command.ThreadID)
		})
	case api.Halt:
		// RequestManualStop does not invoke any ptrace syscalls, so it's
		// safe to access the process directly.
		log.Print("halting")
		err = d.process.RequestManualStop()
	}
	if err != nil {
		// Only report the error if it's not a process exit.
		if _, exited := err.(proctl.ProcessExitedError); !exited {
			return nil, err
		}
	}
	return d.State()
}

func (d *Debugger) ListSource(location string) (*api.DebuggerState, error) {
	state, err := d.State()
	if state.List == nil {
		return nil, nil
	}

	err = d.withProcess(func(p *proctl.DebuggedProcess) error {

		if state.List.Line == 0 {
			state.List.File = state.CurrentThread.File
			state.List.Line = state.CurrentThread.Line
		}
		state.List.Direction = true
		numLines := state.List.ListSize
		halfLines := (numLines + 1) / 2

		if len(location) != 0 {
			switch location {
			case "-":
				state.List.Direction = false
				state.List.Line -= numLines
				if state.List.Line < halfLines {
					state.List.Line = halfLines
				}
			default:
				i, err := strconv.Atoi(location)
				if err != nil {
					// handle error
					return err
				}
				if i < halfLines {
					state.List.Line = halfLines
				} else {
					state.List.Line = i
				}
			}
		} else {
			state.List.Line += numLines
		}
		state.List.FirstLine = state.List.Line - halfLines + 1
		state.List.LastLine = state.List.Line + halfLines

		p.ListContext.Line = state.List.Line
		p.ListContext.Direction = state.List.Direction
		return nil
	})

	return state, err
}

func (d *Debugger) Sources(filter string) ([]string, error) {
	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	files := []string{}
	d.withProcess(func(p *proctl.DebuggedProcess) error {
		for f := range p.Sources() {
			if regex.Match([]byte(f)) {
				files = append(files, f)
			}
		}
		return nil
	})
	return files, nil
}

func (d *Debugger) Functions(filter string) ([]string, error) {
	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	funcs := []string{}
	d.withProcess(func(p *proctl.DebuggedProcess) error {
		for _, f := range p.Funcs() {
			if f.Sym != nil && regex.Match([]byte(f.Name)) {
				funcs = append(funcs, f.Name)
			}
		}
		return nil
	})
	return funcs, nil
}

func (d *Debugger) PackageVariables(threadID int, filter string) ([]api.Variable, error) {
	regex, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter argument: %s", err.Error())
	}

	vars := []api.Variable{}
	err = d.withProcess(func(p *proctl.DebuggedProcess) error {
		thread, found := p.Threads[threadID]
		if !found {
			return fmt.Errorf("couldn't find thread %d", threadID)
		}
		pv, err := thread.PackageVariables()
		if err != nil {
			return err
		}
		for _, v := range pv {
			if regex.Match([]byte(v.Name)) {
				vars = append(vars, convertVar(v))
			}
		}
		return nil
	})
	return vars, err
}

func (d *Debugger) LocalVariables(threadID int) ([]api.Variable, error) {
	vars := []api.Variable{}
	err := d.withProcess(func(p *proctl.DebuggedProcess) error {
		thread, found := p.Threads[threadID]
		if !found {
			return fmt.Errorf("couldn't find thread %d", threadID)
		}
		pv, err := thread.LocalVariables()
		if err != nil {
			return err
		}
		for _, v := range pv {
			vars = append(vars, convertVar(v))
		}
		return nil
	})
	return vars, err
}

func (d *Debugger) FunctionArguments(threadID int) ([]api.Variable, error) {
	vars := []api.Variable{}
	err := d.withProcess(func(p *proctl.DebuggedProcess) error {
		thread, found := p.Threads[threadID]
		if !found {
			return fmt.Errorf("couldn't find thread %d", threadID)
		}
		pv, err := thread.FunctionArguments()
		if err != nil {
			return err
		}
		for _, v := range pv {
			vars = append(vars, convertVar(v))
		}
		return nil
	})
	return vars, err
}

func (d *Debugger) EvalVariableInThread(threadID int, symbol string) (*api.Variable, error) {
	var variable *api.Variable
	err := d.withProcess(func(p *proctl.DebuggedProcess) error {
		thread, found := p.Threads[threadID]
		if !found {
			return fmt.Errorf("couldn't find thread %d", threadID)
		}
		v, err := thread.EvalVariable(symbol)
		if err != nil {
			return err
		}
		converted := convertVar(v)
		variable = &converted
		return nil
	})
	return variable, err
}

func (d *Debugger) Goroutines() ([]*api.Goroutine, error) {
	goroutines := []*api.Goroutine{}
	err := d.withProcess(func(p *proctl.DebuggedProcess) error {
		gs, err := p.GoroutinesInfo()
		if err != nil {
			return err
		}
		for _, g := range gs {
			goroutines = append(goroutines, convertGoroutine(g))
		}
		return nil
	})
	return goroutines, err
}

// convertList converts an internal list to an API List.
func convertList(ls *proctl.List) *api.List {
	return &api.List{
		LineSpec: api.LineSpec{
			FunctionName: ls.FunctionName,
			File:         ls.File,
			Line:         ls.Line,
			Addr:         ls.Addr,
		},
		ListSize:  10,
		Direction: true,
	}
}

// convertBreakPoint converts an internal breakpoint to an API BreakPoint.
func convertBreakPoint(bp *proctl.BreakPoint) *api.BreakPoint {
	return &api.BreakPoint{
		ID: bp.ID,
		LineSpec: api.LineSpec{
			FunctionName: bp.FunctionName,
			File:         bp.File,
			Line:         bp.Line,
			Addr:         bp.Addr,
		},
	}
}

// convertThread converts an internal thread to an API Thread.
func convertThread(th *proctl.ThreadContext) *api.Thread {
	var (
		function *api.Function
		file     string
		line     int
		pc       uint64
	)

	loc, err := th.Location()
	if err == nil {
		pc = loc.PC
		file = loc.File
		line = loc.Line
		if loc.Fn != nil {
			function = &api.Function{
				Name:   loc.Fn.Name,
				Type:   loc.Fn.Type,
				Value:  loc.Fn.Value,
				GoType: loc.Fn.GoType,
			}
		}
	}

	return &api.Thread{
		ID: th.Id,
		LineSpec: api.LineSpec{
			Addr: pc,
			File: file,
			Line: line,
		},
		Function: function,
	}
}

// convertVar converts an internal variable to an API Variable.
func convertVar(v *proctl.Variable) api.Variable {
	return api.Variable{
		Name:  v.Name,
		Value: v.Value,
		Type:  v.Type,
	}
}

// convertGoroutine converts an internal Goroutine to an API Goroutine.
func convertGoroutine(g *proctl.G) *api.Goroutine {
	var function *api.Function
	if g.Func != nil {
		function = &api.Function{
			Name:   g.Func.Name,
			Type:   g.Func.Type,
			Value:  g.Func.Value,
			GoType: g.Func.GoType,
		}
	}

	return &api.Goroutine{
		ID:       g.Id,
		PC:       g.PC,
		File:     g.File,
		Line:     g.Line,
		Function: function,
	}
}
