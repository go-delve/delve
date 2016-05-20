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
type ResumeMode int

const (
	// ModeStepInstruction a single CPU instruction.
	ModeStepInstruction = ResumeMode(0)
	// ModeStep to next source line, stepping into functions.
	ModeStep = iota
	// ModeResume until breakpoint/signal/manual stop.
	ModeResume
)

// Process represents a process we are debugging.
type Process struct {
	// Pid is the process pid.
	Pid int
	// Breakpoints is a map holding information on the active breakpoints
	// for this process.
	Breakpoints map[uint64]*Breakpoint
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
		Breakpoints: make(map[uint64]*Breakpoint),
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

// Detach from the process being debugged, optionally killing it.
func (p *Process) Detach(kill bool) (err error) {
	if p.Running() {
		if err = p.Halt(); err != nil {
			return
		}
	}
	if !kill {
		// Clean up any breakpoints we've set.
		for _, bp := range p.Breakpoints {
			if bp != nil {
				_, err := p.ClearBreakpoint(bp.Addr)
				if err != nil {
					return err
				}
			}
		}
	}
	err = PtraceDetach(p.Pid, 0)
	if err != nil {
		return
	}
	if kill {
		err = killProcess(p.Pid)
	}
	return
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

// Mourn will reap the process, ensuring it does not become a zombie.
func (p *Process) Mourn() (int, error) {
	p.exited = true
	_, ws, err := p.wait(p.Pid, 0)
	if err != nil {
		if err != syscall.ECHILD {
			return 0, err
		}
		return 0, nil
	}
	return ws.ExitStatus(), nil
}

// LoadInformation finds the executable and then uses it
// to parse the following information:
// * Dwarf .debug_frame section
// * Dwarf .debug_line section
// * Go symbol table.
func (p *Process) LoadInformation(path string) error {
	path, err := p.findExecutable(path)
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

// FindFileLocation returns the PC for a given file:line.
// Assumes that `file` is normailzed to lower case and '/' on Windows.
func (p *Process) FindFileLocation(fileName string, lineno int) (uint64, error) {
	pc, fn, err := p.Dwarf.LineToPC(fileName, lineno)
	if err != nil {
		return 0, err
	}
	if fn.Entry == pc {
		pc, _ = p.FirstPCAfterPrologue(fn, true)
	}
	return pc, nil
}

// FindFunctionLocation finds address of a function's line
// If firstLine == true is passed FindFunctionLocation will attempt to find the first line of the function
// If lineOffset is passed FindFunctionLocation will return the address of that line
// Pass lineOffset == 0 and firstLine == false if you want the address for the function's entry point
// Note that setting breakpoints at that address will cause surprising behavior:
// https://github.com/derekparker/delve/issues/170
func (p *Process) FindFunctionLocation(funcName string, firstLine bool, lineOffset int) (uint64, error) {
	origfn := p.Dwarf.LookupFunc(funcName)
	if origfn == nil {
		return 0, fmt.Errorf("Could not find function %s\n", funcName)
	}

	if firstLine {
		return p.FirstPCAfterPrologue(origfn, false)
	} else if lineOffset > 0 {
		filename, lineno, _ := p.Dwarf.PCToLine(origfn.Entry)
		breakAddr, _, err := p.Dwarf.LineToPC(filename, lineno+lineOffset)
		return breakAddr, err
	}

	return origfn.Entry, nil
}

// CurrentLocation returns the location of the current thread.
func (p *Process) CurrentLocation() (*Location, error) {
	return p.CurrentThread.Location()
}

// RequestManualStop sets the `halt` flag and
// sends SIGSTOP to all threads.
func (p *Process) RequestManualStop() error {
	if p.exited {
		return &ProcessExitedError{}
	}
	p.halt = true
	return requestManualStop(p)
}

// SetBreakpoint sets a breakpoint at addr, and stores it in the process wide
// break point table. Setting a break point must be thread specific due to
// ptrace actions needing the thread to be in a signal-delivery-stop.
func (p *Process) SetBreakpoint(addr uint64) (*Breakpoint, error) {
	return setBreakpoint(p, p.CurrentThread, addr, false, p.arch.BreakpointInstruction())
}

// SetTempBreakpoint sets a temp breakpoint. Used during 'next' operations.
func (p *Process) SetTempBreakpoint(addr uint64) (*Breakpoint, error) {
	return setBreakpoint(p, p.CurrentThread, addr, true, p.arch.BreakpointInstruction())
}

func setBreakpoint(p *Process, mem memoryReadWriter, addr uint64, temp bool, instr []byte) (*Breakpoint, error) {
	if p.exited {
		return nil, &ProcessExitedError{}
	}
	if bp, ok := p.FindBreakpoint(addr); ok {
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
func (p *Process) ClearBreakpoint(addr uint64) (*Breakpoint, error) {
	log.WithField("addr", uintptr(addr)).Info("clear breakpoint")
	if p.exited {
		return nil, &ProcessExitedError{}
	}
	bp, ok := p.FindBreakpoint(addr)
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
func (p *Process) Next() (err error) {
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
			p.ClearTempBreakpoints()
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

	return p.Continue()
}

// Continue continues execution of the debugged
// process. It will continue until it hits a breakpoint
// or is otherwise stopped.
func (p *Process) Continue() error {
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
		th := p.CurrentThread
		if fn, ok := atFunctionCall(th); ok {
			log.Printf("stepping into function: %s", fn.Name)
			return p.StepInto(fn)
		}
		return p.Next()
	case ModeResume:
		log.Info("begin resume")
		if err := p.resume(); err != nil {
			log.Printf("resume error: %v\n", err)
			return err
		}
	default:
		// Programmer error, safe to panic here.
		panic("invalid mode passed to resume")
	}
	status, trapthread, err := p.Wait()
	if err != nil {
		log.WithError(err).Error("Wait error")
		return err
	}
	if status.Exited() {
		return ProcessExitedError{Pid: p.Pid, Status: status.ExitStatus()}
	}
	// Make sure process is fully stopped.
	if err := p.Halt(); err != nil {
		log.WithError(err).Error("Halt error")
		return err
	}
	switch {
	case status.Trap():
		log.Debug("handling SIGTRAP")
		err := handleSigTrap(p, trapthread)
		if err == breakpointConditionNotMetError {
			// Do not stop for a breakpoint whose condition has not been met.
			return resume(p, mode)
		}
		return err
	case status.Signaled():
		log.WithField("signal", status.Signal()).Debug("signaled")
		// TODO(derekparker) alert users of signals.
		if err := trapthread.ContinueWithSignal(int(status.Signal())); err != nil {
			// TODO(derekparker) should go through regular resume flow.
			if err == syscall.ESRCH {
				exitcode, err := p.Mourn()
				if err != nil {
					return err
				}
				return ProcessExitedError{Pid: p.Pid, Status: exitcode}
			}
			return err
		}
		return resume(p, mode)
	default:
		panic("unhandled stop event")
	}
	return nil
}

func atFunctionCall(th *Thread) (*gosym.Func, bool) {
	pc, err := th.PC()
	if err != nil {
		return nil, false
	}
	text, err := th.Disassemble(pc, pc+maxInstructionLength, true)
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
	if err := p.setCurrentBreakpoints(); err != nil {
		log.WithError(err).Error("setCurrentBreakpoints error")
		return err
	}
	if err := p.pickCurrentThread(trapthread); err != nil {
		log.WithError(err).Error("pickCurrentThread error")
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
		return p.conditionErrors()
	case p.CurrentThread.onTriggeredTempBreakpoint():
		log.Debug("on triggered temp breakpoint")
		if err := p.ClearTempBreakpoints(); err != nil {
			return err
		}
		return p.conditionErrors()
	case p.CurrentThread.onTriggeredBreakpoint():
		log.Debug("on triggered breakpoint")
		onNextGoroutine, err := p.CurrentThread.onNextGoroutine()
		if err != nil {
			return err
		}
		if onNextGoroutine {
			if err := p.ClearTempBreakpoints(); err != nil {
				return err
			}
		}
		return p.conditionErrors()
	}
	return breakpointConditionNotMetError
}

func (p *Process) Wait() (*WaitStatus, *Thread, error) {
	return p.trapWait(-p.Pid)
}

func (p *Process) conditionErrors() error {
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

// pick a new p.CurrentThread, with the following priority:
// 	- a thread with onTriggeredTempBreakpoint() == true
// 	- a thread with onTriggeredBreakpoint() == true (prioritizing trapthread)
// 	- trapthread
func (p *Process) pickCurrentThread(trapthread *Thread) error {
	if trapthread == nil {
		panic("pickCurrentThread got nil thread")
	}
	for _, th := range p.Threads {
		if th.onTriggeredTempBreakpoint() {
			log.WithField("thread", th.ID).Debug("triggered temp breakpoint")
			return p.SwitchThread(th.ID)
		}
	}
	if trapthread.onTriggeredBreakpoint() {
		log.WithField("thread", trapthread.ID).Debug("triggered breakpoint")
		return p.SwitchThread(trapthread.ID)
	}
	for _, th := range p.Threads {
		if th.onTriggeredBreakpoint() {
			log.WithField("thread", th.ID).Debug("triggered breakpoint")
			return p.SwitchThread(th.ID)
		}
	}
	log.WithField("thread", trapthread.ID).Debug("switching to default thread")
	return p.SwitchThread(trapthread.ID)
}

// Step will continue until another source line is reached.
// Will step into functions.
func (p *Process) Step() (err error) {
	return resume(p, ModeStep)
}

// StepInto sets a temp breakpoint after the prologue of fn and calls Continue
func (p *Process) StepInto(fn *gosym.Func) error {
	for i := range p.Breakpoints {
		if p.Breakpoints[i].Temp {
			return fmt.Errorf("next while nexting")
		}
	}
	pc, _ := p.FirstPCAfterPrologue(fn, false)
	if _, err := p.SetTempBreakpoint(pc); err != nil {
		return err
	}
	return p.Continue()
}

// StepInstruction will continue the current thread for exactly
// one instruction. This method affects only the thread
// asssociated with the selected goroutine. All other
// threads will remain stopped.
func (p *Process) StepInstruction() (err error) {
	return resume(p, ModeStepInstruction)
}

// SwitchThread changes from current thread to the thread specified by `tid`.
func (p *Process) SwitchThread(tid int) error {
	if p.exited {
		return &ProcessExitedError{}
	}
	if th, ok := p.Threads[tid]; ok {
		p.CurrentThread = th
		p.SelectedGoroutine, _ = p.CurrentThread.GetG()
		return nil
	}
	return fmt.Errorf("thread %d does not exist", tid)
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
		return p.SwitchThread(g.thread.ID)
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
func (p *Process) Halt() (err error) {
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

// FindBreakpointByID finds the breakpoint for the given ID.
func (p *Process) FindBreakpointByID(id int) (*Breakpoint, bool) {
	for _, bp := range p.Breakpoints {
		if bp.ID == id {
			return bp, true
		}
	}
	return nil, false
}

// FindBreakpoint finds the breakpoint for the given pc.
func (p *Process) FindBreakpoint(pc uint64) (*Breakpoint, bool) {
	// Check to see if address is past the breakpoint, (i.e. breakpoint was hit).
	if bp, ok := p.Breakpoints[pc-uint64(p.arch.BreakpointSize())]; ok {
		return bp, true
	}
	// Directly use addr to lookup breakpoint.
	if bp, ok := p.Breakpoints[pc]; ok {
		return bp, true
	}
	return nil, false
}

// Returns a new Process struct.
func initializeDebugProcess(p *Process, path string, attach bool) (*Process, error) {
	if attach {
		var err error
		err = PtraceAttach(p.Pid)
		if err != nil {
			return nil, err
		}
		_, _, err = p.wait(p.Pid, 0)
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

	if err := updateThreadList(p); err != nil {
		return nil, err
	}

	ver, isextld, err := p.getGoInformation()
	if err != nil {
		return nil, err
	}

	p.arch.SetGStructOffset(ver, isextld)
	// SelectedGoroutine can not be set correctly by the call to updateThreadList
	// because without calling SetGStructOffset we can not read the G struct of CurrentThread
	// but without calling updateThreadList we can not examine memory to determine
	// the offset of g struct inside TLS
	p.SelectedGoroutine, _ = p.CurrentThread.GetG()

	panicpc, err := p.FindFunctionLocation("runtime.startpanic", true, 0)
	if err == nil {
		bp, err := p.SetBreakpoint(panicpc)
		if err == nil {
			bp.Name = "unrecovered-panic"
			bp.ID = -1
			breakpointIDCounter--
		}
	}

	return p, nil
}

func (p *Process) ClearTempBreakpoints() error {
	for _, bp := range p.Breakpoints {
		if !bp.Temp {
			continue
		}
		if _, err := p.ClearBreakpoint(bp.Addr); err != nil {
			return err
		}
	}
	return nil
}

func (p *Process) getGoInformation() (ver GoVersion, isextld bool, err error) {
	vv, err := p.EvalPackageVariable("runtime.buildVersion", LoadConfig{true, 0, 64, 0, 0})
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
func (p *Process) ConvertEvalScope(gid, frame int) (*EvalScope, error) {
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

func (p *Process) setCurrentBreakpoints() error {
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
			if _, err = p.SetTempBreakpoint(ret); err != nil {
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
