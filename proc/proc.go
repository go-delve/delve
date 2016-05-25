package proc

import (
	"debug/gosym"
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/debug/dwarf"

	"github.com/Sirupsen/logrus"
	pdwarf "github.com/derekparker/delve/pkg/dwarf"
	"github.com/derekparker/delve/pkg/dwarf/reader"
)

var log = logrus.WithField("package", "proc")

// ResumeMode indicates how we should resume the debugged process.
type ResumeMode string

const (
	// ModeStepInstruction a single CPU instruction.
	ModeStepInstruction = ResumeMode("Step Instruction")
	// ModeStep to next source line, stepping into functions.
	ModeStep = ResumeMode("step")
	// ModeResume until breakpoint/signal/manual stop.
	ModeResume = ResumeMode("resume")
)

// Process represents a process we are debugging.
type Process struct {
	// Pid is the process pid.
	Pid int
	// Breakpoints is a map holding information on the active breakpoints
	// for this process.
	Breakpoints Breakpoints
	// Threads is a list of threads mapped as such: pid -> *Thread
	Threads map[int]*Thread
	// CurrentThread is the active thread. This is the thread that, by default,
	// will be used in certain commands such as "step". In general, the active thread
	// is the thread that stopped on a breakpoint.
	CurrentThread *Thread
	// SelectedGoroutine is the goroutine that will be used by default to set breakpoint, eval variables, etc...
	// Normally SelectedGoroutine is CurrentThread.GetG, it will not be only if SwitchGoroutine is called with a goroutine that isn't attached to a thread
	SelectedGoroutine *G
	// Dwarf is the dwarf debugging information for this process.
	Dwarf *pdwarf.Dwarf

	allGCache []*G
	os        *OSProcessDetails
	arch      Arch
	halt      bool
	exited    bool
}

// WaitStatus is the information obtained after waiting on a process.
type WaitStatus struct {
	exitstatus int
	exited     bool
	signaled   bool
	signal     syscall.Signal
}

// Exited returns whether the process has exited.
func (ws *WaitStatus) Exited() bool {
	return ws.exited
}

// ExitStatus returns the status of an exited process.
func (ws *WaitStatus) ExitStatus() int {
	return ws.exitstatus
}

// Signaled returns whether the process was stopped via a signal.
func (ws *WaitStatus) Signaled() bool {
	return ws.signaled
}

// Signal is the signal the process recieved.
func (ws *WaitStatus) Signal() syscall.Signal {
	return ws.signal
}

// Trap returns whether the process was stopped via a "trap" exception,
// e.g. a breakpoint or similar event.
func (ws *WaitStatus) Trap() bool {
	return ws.signal == syscall.SIGTRAP
}

// New returns an initialized Process struct.
func New(pid int) *Process {
	return &Process{
		Pid:         pid,
		Threads:     make(map[int]*Thread),
		Breakpoints: make(Breakpoints),
		os:          new(OSProcessDetails),
	}
}

// ProcessExitedError indicates that the process has exited and contains both
// process id and exit status.
type ProcessExitedError struct {
	Pid    int
	Status int
}

func (pe ProcessExitedError) Error() string {
	return fmt.Sprintf("Process %d has exited with status %d", pe.Pid, pe.Status)
}

// Exited returns whether the process has exited.
func (p *Process) Exited() bool {
	return p.exited
}

// Running returns whether the process is currently executing.
func (p *Process) Running() bool {
	for _, th := range p.Threads {
		if th.running {
			return true
		}
	}
	return false
}

// LoadInformation finds the executable and then uses it
// to parse the following information:
// * Dwarf .debug_frame section
// * Dwarf .debug_line section
// * Go symbol table.
func (p *Process) LoadInformation(path string) error {
	path, err := findExecutable(p.Pid, path)
	if err != nil {
		return err
	}
	d, err := pdwarf.Parse(path)
	if err != nil {
		return err
	}
	p.Dwarf = d
	p.loadProcessInformation()
	return nil
}

// CurrentLocation returns the location of the current thread.
func (p *Process) CurrentLocation() (*Location, error) {
	return p.CurrentThread.Location()
}

// Detach from the process being debugged, optionally killing it.
func Detach(p *Process, kill bool) error {
	if p.Running() {
		if err := Halt(p); err != nil {
			return err
		}
	}
	if !kill {
		// Clean up any breakpoints we've set.
		for _, bp := range p.Breakpoints {
			if bp != nil {
				if _, err := ClearBreakpoint(p, bp.Addr); err != nil {
					return err
				}
			}
		}
	}
	if err := PtraceDetach(p.Pid, 0); err != nil {
		return err
	}
	if kill {
		return Kill(p)
	}
	return nil
}

// Mourn will reap the process, ensuring it does not become a zombie.
func Mourn(p *Process) (int, error) {
	s, err := mourn(p)
	if err != nil {
		return 0, err
	}
	p.exited = true
	return s, nil
}

// FindFileLocation returns the PC for a given file:line.
// Assumes that `file` is normailzed to lower case and '/' on Windows.
func FindFileLocation(p *Process, fileName string, lineno int) (uint64, error) {
	pc, fn, err := p.Dwarf.LineToPC(fileName, lineno)
	if err != nil {
		return 0, err
	}
	if fn.Entry == pc {
		pc, _ = FirstPCAfterPrologue(p, fn, true)
	}
	return pc, nil
}

// FindFunctionLocation finds address of a function's line
// If firstLine == true is passed FindFunctionLocation will attempt to find the first line of the function
// If lineOffset is passed FindFunctionLocation will return the address of that line
// Pass lineOffset == 0 and firstLine == false if you want the address for the function's entry point
// Note that setting breakpoints at that address will cause surprising behavior:
// https://github.com/derekparker/delve/issues/170
func FindFunctionLocation(p *Process, funcName string, firstLine bool, lineOffset int) (uint64, error) {
	origfn := p.Dwarf.LookupFunc(funcName)
	if origfn == nil {
		return 0, fmt.Errorf("Could not find function %s\n", funcName)
	}

	if firstLine {
		return FirstPCAfterPrologue(p, origfn, false)
	} else if lineOffset > 0 {
		filename, lineno, _ := p.Dwarf.PCToLine(origfn.Entry)
		breakAddr, _, err := p.Dwarf.LineToPC(filename, lineno+lineOffset)
		return breakAddr, err
	}

	return origfn.Entry, nil
}

// Stop sets the `halt` flag and
// sends SIGSTOP to all threads.
func Stop(p *Process) error {
	if p.exited {
		return &ProcessExitedError{}
	}
	p.halt = true
	return stop(p)
}

// Kill kills the target process.
func Kill(p *Process) (err error) {
	if p.exited {
		return nil
	}
	log.WithField("pid", p.Pid).Info("killing process")
	if err = kill(p); err != nil {
		return errors.New("could not deliver signal " + err.Error())
	}
	_, err = Mourn(p)
	return err
}

// SetBreakpoint sets a breakpoint at addr, and stores it in the process wide
// break point table. Setting a break point must be thread specific due to
// ptrace actions needing the thread to be in a signal-delivery-stop.
func SetBreakpoint(p *Process, addr uint64) (*Breakpoint, error) {
	return setBreakpoint(p, p.CurrentThread, addr, false, p.arch.BreakpointInstruction())
}

// SetTempBreakpoint sets a temp breakpoint. Used during 'next' operations.
func SetTempBreakpoint(p *Process, addr uint64) (*Breakpoint, error) {
	return setBreakpoint(p, p.CurrentThread, addr, true, p.arch.BreakpointInstruction())
}

func setBreakpoint(p *Process, mem memoryReadWriter, addr uint64, temp bool, instr []byte) (*Breakpoint, error) {
	if p.exited {
		return nil, &ProcessExitedError{}
	}
	if bp, ok := p.Breakpoints[addr]; ok {
		return nil, BreakpointExistsError{bp.File, bp.Line, bp.Addr}
	}
	f, l, fn := p.Dwarf.PCToLine(uint64(addr))
	if fn == nil {
		return nil, InvalidAddressError{address: addr}
	}
	loc := &Location{Fn: fn, File: f, Line: l, PC: addr}
	bp, err := createAndWriteBreakpoint(p.CurrentThread, loc, temp, instr)
	if err != nil {
		return nil, err
	}
	p.Breakpoints[addr] = bp
	return bp, nil
}

// ClearBreakpoint clears the breakpoint at addr.
func ClearBreakpoint(p *Process, addr uint64) (*Breakpoint, error) {
	log.WithField("addr", uintptr(addr)).Info("clear breakpoint")
	if p.exited {
		return nil, &ProcessExitedError{}
	}
	bp, ok := p.Breakpoints[addr]
	if !ok {
		return nil, NoBreakpointError{addr: addr}
	}

	if _, err := bp.Clear(p.CurrentThread); err != nil {
		return nil, err
	}

	// Restore any thread that was stopped on this
	// breakpoint to the correct instruction.
	for _, th := range p.Threads {
		if tbp := th.CurrentBreakpoint; tbp != nil {
			if tbp.Addr == bp.Addr {
				th.clearBreakpointState()
				if err := th.SetPC(bp.Addr); err != nil {
					return nil, err
				}
			}
		}
	}

	delete(p.Breakpoints, addr)
	return bp, nil
}

// Status returns the status of the current main thread context.
func (p *Process) Status() *WaitStatus {
	return p.CurrentThread.Status
}

// Next continues execution until the next source line.
func Next(p *Process) (err error) {
	log.Info("nexting")
	if p.exited {
		return &ProcessExitedError{}
	}
	for i := range p.Breakpoints {
		if p.Breakpoints[i].Temp {
			return fmt.Errorf("next while nexting")
		}
	}

	// Get the goroutine for the current thread. We will
	// use it later in order to ensure we are on the same
	// goroutine.
	g, err := p.CurrentThread.GetG()
	if err != nil {
		return err
	}

	// Set breakpoints for any goroutine that is currently
	// blocked trying to read from a channel. This is so that
	// if control flow switches to that goroutine, we end up
	// somewhere useful instead of in runtime code.
	if _, err = setChanRecvBreakpoints(p); err != nil {
		return
	}

	var goroutineExiting bool
	if err = p.CurrentThread.setNextBreakpoints(); err != nil {
		switch t := err.(type) {
		case ThreadBlockedError, NoReturnAddr: // Noop
		case GoroutineExitingError:
			goroutineExiting = t.goid == g.ID
		default:
			ClearTempBreakpoints(p)
			return
		}
	}

	if !goroutineExiting {
		for i := range p.Breakpoints {
			if p.Breakpoints[i].Temp {
				p.Breakpoints[i].Cond = &ast.BinaryExpr{
					Op: token.EQL,
					X: &ast.SelectorExpr{
						X: &ast.SelectorExpr{
							X:   &ast.Ident{Name: "runtime"},
							Sel: &ast.Ident{Name: "curg"},
						},
						Sel: &ast.Ident{Name: "goid"},
					},
					Y: &ast.BasicLit{Kind: token.INT, Value: strconv.Itoa(g.ID)},
				}
			}
		}
	}

	return Continue(p)
}

// Continue continues execution of the debugged
// process. It will continue until it hits a breakpoint
// or is otherwise stopped.
func Continue(p *Process) error {
	return resume(p, ModeResume)
}

func resume(p *Process, mode ResumeMode) error {
	if p.exited {
		return ProcessExitedError{}
	}

	log.WithFields(logrus.Fields{"pid": p.Pid, "mode": mode}).Debug("resuming process")

	// Clear state.
	p.allGCache = nil

	// Resume process.
	switch mode {
	case ModeStepInstruction:
		log.Info("begin step instruction")
		if p.SelectedGoroutine == nil {
			return errors.New("cannot single step: no selected goroutine")
		}
		if p.SelectedGoroutine.thread == nil {
			return fmt.Errorf("cannot single step: no thread associated with goroutine %d", p.SelectedGoroutine.ID)
		}
		return p.SelectedGoroutine.thread.StepInstruction()
	case ModeStep:
		log.Info("begin step")
		if fn, ok := atFunctionCall(p.CurrentThread); ok {
			log.Printf("stepping into function: %s", fn.Name)
			return StepInto(p, fn)
		}
		return Next(p)
	case ModeResume:
		log.Info("begin resume")
		// all threads stopped over a breakpoint are made to step over it
		for _, thread := range p.Threads {
			if thread.CurrentBreakpoint != nil {
				if err := thread.StepInstruction(); err != nil {
					if err == ThreadExitedErr {
						delete(p.Threads, thread.ID)
						continue
					}
					logrus.WithError(err).Error("error while stepping thread to resume process")
					return err
				}
			}
		}
		// everything is resumed
		for _, thread := range p.Threads {
			if err := thread.Continue(); err != nil {
				if err == ThreadExitedErr {
					delete(p.Threads, thread.ID)
					continue
				}
				logrus.WithError(err).Error("error while resuming process")
				return err
			}
		}

	default:
		// Programmer error, safe to panic here.
		panic("invalid mode passed to resume")
	}
	status, trapthread, err := Wait(p)
	if err != nil {
		log.WithError(err).Error("Wait error")
		return err
	}
	if status.Exited() {
		return ProcessExitedError{Pid: p.Pid, Status: status.ExitStatus()}
	}
	// Make sure process is fully stopped.
	if err := Halt(p); err != nil {
		log.WithError(err).Error("Halt error")
		return err
	}
	switch {
	case status.Trap():
		log.Info("handling SIGTRAP")
		err := handleSigTrap(p, trapthread)
		if err == breakpointConditionNotMetError {
			// Do not stop for a breakpoint whose condition has not been met.
			return resume(p, mode)
		}
		if err != nil {
			log.WithError(err).Error("handleSigTrap error")
			return err
		}
		return nil
	case status.Signaled():
		log.WithField("signal", status.Signal()).Info("signaled")
		// TODO(derekparker) alert users of signals.
		if err := trapthread.ContinueWithSignal(int(status.Signal())); err != nil {
			// TODO(derekparker) should go through regular resume flow.
			if err == syscall.ESRCH {
				exitcode, err := Mourn(p)
				if err != nil {
					return err
				}
				return ProcessExitedError{Pid: p.Pid, Status: exitcode}
			}
			return err
		}
		return resume(p, mode)
	default:
		log.WithFields(logrus.Fields{
			"signal": status.Signal(),
			"trap":   status.Trap(),
			"exited": status.Exited(),
		}).Error("unhandled stop event")
		panic("unhandled stop event")
	}
	return nil
}

func atFunctionCall(th *Thread) (*gosym.Func, bool) {
	pc, err := th.PC()
	if err != nil {
		return nil, false
	}
	text, err := Disassemble(th, pc, pc+maxInstructionLength, true)
	if err != nil {
		return nil, false
	}
	loc := text[0].Loc
	for _, txt := range text {
		if txt.Loc.Line != loc.Line {
			break
		}
		if txt.IsCall() && txt.DestLoc != nil && txt.DestLoc.Fn != nil {
			return txt.DestLoc.Fn, true
		}
	}
	return nil, false
}

func handleSigTrap(p *Process, trapthread *Thread) error {
	log.Debug("checking breakpoint conditions")
	if err := setCurrentBreakpoints(p); err != nil {
		log.WithError(err).Error("setCurrentBreakpoints error")
		return err
	}
	th, err := activeThread(p.Threads, trapthread)
	if err != nil {
		log.WithError(err).Error("setCurrentThread error")
		return err
	}
	if err := p.SetActiveThread(th); err != nil {
		return err
	}
	switch {
	case p.CurrentThread.CurrentBreakpoint == nil:
		log.WithField("halt", p.halt).Info("no current breakpoint")
		if p.halt {
			p.halt = false
		}
		// runtime.Breakpoint or manual stop
		if p.CurrentThread.onRuntimeBreakpoint() {
			log.Debug("on runtime breakpoint")
			for i := 0; i < 2; i++ {
				if err := p.CurrentThread.StepInstruction(); err != nil {
					return err
				}
			}
		}
		return conditionErrors(p)
	case p.CurrentThread.onTriggeredTempBreakpoint():
		log.Debug("on triggered temp breakpoint")
		if err := ClearTempBreakpoints(p); err != nil {
			return err
		}
		return conditionErrors(p)
	case p.CurrentThread.onTriggeredBreakpoint():
		log.Debug("on triggered breakpoint")
		onNextGoroutine, err := p.CurrentThread.onNextGoroutine()
		if err != nil {
			return err
		}
		if onNextGoroutine {
			if err := ClearTempBreakpoints(p); err != nil {
				return err
			}
		}
		return conditionErrors(p)
	}
	return breakpointConditionNotMetError
}

func Wait(p *Process) (*WaitStatus, *Thread, error) {
	return wait(p, -p.Pid)
}

func conditionErrors(p *Process) error {
	var condErr error
	for _, th := range p.Threads {
		if th.CurrentBreakpoint != nil && th.BreakpointConditionError != nil {
			if condErr == nil {
				condErr = th.BreakpointConditionError
			} else {
				return fmt.Errorf("multiple errors evaluating conditions")
			}
		}
	}
	return condErr
}

// set a new p.CurrentThread, with the following priority:
// 	- a thread with onTriggeredTempBreakpoint() == true
// 	- a thread with onTriggeredBreakpoint() == true (prioritizing trapthread)
// 	- trapthread
func activeThread(threads map[int]*Thread, trapthread *Thread) (*Thread, error) {
	for _, th := range threads {
		if th.onTriggeredTempBreakpoint() {
			log.WithField("thread", th.ID).Debug("triggered temp breakpoint")
			return th, nil
		}
	}
	if trapthread.onTriggeredBreakpoint() {
		log.WithField("thread", trapthread.ID).Debug("triggered breakpoint")
		return trapthread, nil
	}
	for _, th := range threads {
		if th.onTriggeredBreakpoint() {
			log.WithField("thread", th.ID).Debug("triggered breakpoint")
			return th, nil
		}
	}
	log.WithField("thread", trapthread.ID).Debug("trapthread is active")
	return trapthread, nil
}

// Step will continue until another source line is reached.
// Will step into functions.
func Step(p *Process) (err error) {
	return resume(p, ModeStep)
}

// StepInto sets a temp breakpoint after the prologue of fn and calls Continue
func StepInto(p *Process, fn *gosym.Func) error {
	for i := range p.Breakpoints {
		if p.Breakpoints[i].Temp {
			return fmt.Errorf("next while nexting")
		}
	}
	pc, _ := FirstPCAfterPrologue(p, fn, false)
	if _, err := SetTempBreakpoint(p, pc); err != nil {
		return err
	}
	return Continue(p)
}

// StepInstruction will continue the current thread for exactly
// one instruction. This method affects only the thread
// asssociated with the selected goroutine. All other
// threads will remain stopped.
func StepInstruction(p *Process) (err error) {
	return resume(p, ModeStepInstruction)
}

// SetActiveThread changes from current thread to the thread specified.
func (p *Process) SetActiveThread(t *Thread) error {
	if p.exited {
		return &ProcessExitedError{}
	}
	if t == nil {
		// Programmer error, should never happen.
		panic("nil thread")
	}
	p.CurrentThread = t
	p.SelectedGoroutine, _ = t.GetG()
	return nil
}

// SwitchGoroutine changes from current thread to the thread
// running the specified goroutine.
func (p *Process) SwitchGoroutine(gid int) error {
	if p.exited {
		return &ProcessExitedError{}
	}
	g, err := p.FindGoroutine(gid)
	if err != nil {
		return err
	}
	if g == nil {
		// user specified -1 and SelectedGoroutine is nil
		return nil
	}
	if g.thread != nil {
		return p.SetActiveThread(g.thread)
	}
	p.SelectedGoroutine = g
	return nil
}

// GoroutinesInfo returns an array of G structures representing the information
// Delve cares about from the internal runtime G structure.
func (p *Process) GoroutinesInfo() ([]*G, error) {
	if p.exited {
		return nil, &ProcessExitedError{}
	}
	if p.allGCache != nil {
		return p.allGCache, nil
	}

	var (
		threadg = map[int]*Thread{}
		allg    []*G
		rdr     = p.DwarfReader()
	)

	for i := range p.Threads {
		if p.Threads[i].blocked() {
			continue
		}
		g, _ := p.Threads[i].GetG()
		if g != nil {
			threadg[g.ID] = p.Threads[i]
		}
	}

	addr, err := rdr.AddrFor("runtime.allglen")
	if err != nil {
		return nil, err
	}
	allglenBytes, err := p.CurrentThread.readMemory(uintptr(addr), 8)
	if err != nil {
		return nil, err
	}
	allglen := binary.LittleEndian.Uint64(allglenBytes)

	rdr.Seek(0)
	allgentryaddr, err := rdr.AddrFor("runtime.allgs")
	if err != nil {
		// try old name (pre Go 1.6)
		allgentryaddr, err = rdr.AddrFor("runtime.allg")
		if err != nil {
			return nil, err
		}
	}
	faddr, err := p.CurrentThread.readMemory(uintptr(allgentryaddr), p.arch.PtrSize())
	allgptr := binary.LittleEndian.Uint64(faddr)

	for i := uint64(0); i < allglen; i++ {
		gvar, err := p.CurrentThread.newGVariable(uintptr(allgptr+(i*uint64(p.arch.PtrSize()))), true)
		if err != nil {
			return nil, err
		}
		g, err := gvar.parseG()
		if err != nil {
			return nil, err
		}
		if thread, allocated := threadg[g.ID]; allocated {
			loc, err := thread.Location()
			if err != nil {
				return nil, err
			}
			g.thread = thread
			// Prefer actual thread location information.
			g.CurrentLoc = *loc
		}
		if g.Status != Gdead {
			allg = append(allg, g)
		}
	}
	p.allGCache = allg
	return allg, nil
}

// Halt stops all threads.
func Halt(p *Process) (err error) {
	if p.exited {
		return &ProcessExitedError{}
	}
	for _, th := range p.Threads {
		if err := th.Halt(); err != nil {
			return err
		}
	}
	return nil
}

// Registers obtains register values from the
// "current" thread of the traced process.
func (p *Process) Registers() (Registers, error) {
	return p.CurrentThread.Registers()
}

// PC returns the PC of the current thread.
func (p *Process) PC() (uint64, error) {
	return p.CurrentThread.PC()
}

// CurrentBreakpoint returns the breakpoint the current thread
// is stopped at.
func (p *Process) CurrentBreakpoint() *Breakpoint {
	return p.CurrentThread.CurrentBreakpoint
}

// DwarfReader returns a reader for the dwarf data
func (p *Process) DwarfReader() *reader.Reader {
	return p.Dwarf.Reader()
}

func getGoInformation(p *Process) (ver GoVersion, isextld bool, err error) {
	vv, err := EvalPackageVariable(p, "runtime.buildVersion", LoadConfig{true, 0, 64, 0, 0})
	if err != nil {
		err = fmt.Errorf("Could not determine version number: %v\n", err)
		return
	}
	if vv.Unreadable != nil {
		err = fmt.Errorf("Unreadable version number: %v\n", vv.Unreadable)
		return
	}

	ver, ok := ParseVersionString(constant.StringVal(vv.Value))
	if !ok {
		err = fmt.Errorf("Could not parse version number: %v\n", vv.Value)
		return
	}

	rdr := p.DwarfReader()
	rdr.Seek(0)
	for entry, err := rdr.NextCompileUnit(); entry != nil; entry, err = rdr.NextCompileUnit() {
		if err != nil {
			return ver, isextld, err
		}
		if prod, ok := entry.Val(dwarf.AttrProducer).(string); ok && (strings.HasPrefix(prod, "GNU AS")) {
			isextld = true
			break
		}
	}
	return
}

// Returns a new Process struct.
func initializeDebugProcess(p *Process, path string, attach bool) (*Process, error) {
	if attach {
		var err error
		err = PtraceAttach(p.Pid)
		if err != nil {
			return nil, err
		}
		_, _, err = Wait(p)
		if err != nil {
			return nil, err
		}
	}

	err := p.LoadInformation(path)
	if err != nil {
		return nil, err
	}

	switch runtime.GOARCH {
	case "amd64":
		p.arch = AMD64Arch()
	}

	if err := p.updateThreadList(); err != nil {
		return nil, err
	}

	ver, isextld, err := getGoInformation(p)
	if err != nil {
		return nil, err
	}

	p.arch.SetGStructOffset(ver, isextld)
	// SelectedGoroutine can not be set correctly by the call to updateThreadList
	// because without calling SetGStructOffset we can not read the G struct of CurrentThread
	// but without calling updateThreadList we can not examine memory to determine
	// the offset of g struct inside TLS
	p.SelectedGoroutine, _ = p.CurrentThread.GetG()

	panicpc, err := FindFunctionLocation(p, "runtime.startpanic", true, 0)
	if err == nil {
		bp, err := SetBreakpoint(p, panicpc)
		if err == nil {
			bp.Name = "unrecovered-panic"
			bp.ID = -1
			breakpointIDCounter--
		}
	}

	return p, nil
}

func ClearTempBreakpoints(p *Process) error {
	for _, bp := range p.Breakpoints {
		if !bp.Temp {
			continue
		}
		if _, err := ClearBreakpoint(p, bp.Addr); err != nil {
			return err
		}
	}
	return nil
}

// FindGoroutine returns a G struct representing the goroutine
// specified by `gid`.
func (p *Process) FindGoroutine(gid int) (*G, error) {
	if gid == -1 {
		return p.SelectedGoroutine, nil
	}

	gs, err := p.GoroutinesInfo()
	if err != nil {
		return nil, err
	}
	for i := range gs {
		if gs[i].ID == gid {
			return gs[i], nil
		}
	}
	return nil, fmt.Errorf("Unknown goroutine %d", gid)
}

// ConvertEvalScope returns a new EvalScope in the context of the
// specified goroutine ID and stack frame.
func ConvertEvalScope(p *Process, gid, frame int) (*EvalScope, error) {
	if p.exited {
		return nil, &ProcessExitedError{}
	}
	g, err := p.FindGoroutine(gid)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return p.CurrentThread.Scope()
	}

	var out EvalScope

	if g.thread == nil {
		out.Thread = p.CurrentThread
	} else {
		out.Thread = g.thread
	}

	locs, err := g.Stacktrace(frame)
	if err != nil {
		return nil, err
	}

	if frame >= len(locs) {
		return nil, fmt.Errorf("Frame %d does not exist in goroutine %d", frame, gid)
	}

	out.PC, out.CFA = locs[frame].Current.PC, locs[frame].CFA

	return &out, nil
}

func setCurrentBreakpoints(p *Process) error {
	for _, th := range p.Threads {
		if th.CurrentBreakpoint == nil {
			err := th.SetCurrentBreakpoint()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func setChanRecvBreakpoints(p *Process) (int, error) {
	var count int
	allg, err := p.GoroutinesInfo()
	if err != nil {
		return 0, err
	}

	for _, g := range allg {
		if g.ChanRecvBlocked() {
			ret, err := g.chanRecvReturnAddr(p)
			if err != nil {
				if _, ok := err.(NullAddrError); ok {
					continue
				}
				return 0, err
			}
			if _, err = SetTempBreakpoint(p, ret); err != nil {
				if _, ok := err.(BreakpointExistsError); ok {
					// Ignore duplicate breakpoints in case if multiple
					// goroutines wait on the same channel
					continue
				}
				return 0, err
			}
			count++
		}
	}
	return count, nil
}
