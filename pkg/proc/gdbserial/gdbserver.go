// This file and its companion gdbserver_conn implement a target.Interface
// backed by a connection to a debugger speaking the "Gdb Remote Serial
// Protocol".
//
// The "Gdb Remote Serial Protocol" is a low level debugging protocol
// originally designed so that gdb could be used to debug programs running
// in embedded environments but it was later extended to support programs
// running on any environment and a variety of debuggers support it:
// gdbserver, lldb-server, macOS's debugserver and rr.
//
// The protocol is specified at:
//   https://sourceware.org/gdb/onlinedocs/gdb/Remote-Protocol.html
// with additional documentation for lldb specific extensions described at:
//   https://github.com/llvm-mirror/lldb/blob/master/docs/lldb-gdb-remote.txt
//
// Terminology:
//  * inferior: the program we are trying to debug
//  * stub: the debugger on the other side of the protocol's connection (for
//    example lldb-server)
//  * gdbserver: stub version of gdb
//  * lldb-server: stub version of lldb
//  * debugserver: a different stub version of lldb, installed with lldb on
//    macOS.
//  * mozilla rr: a stub that records the full execution of a program
//    and can then play it back.
//
// Implementations of the protocol vary wildly between stubs, while there is
// a command to query the stub about supported features (qSupported) this
// only covers *some* of the more recent additions to the protocol and most
// of the older packets are optional and *not* implemented by all stubs.
// For example gdbserver implements 'g' (read all registers) but not 'p'
// (read single register) while lldb-server implements 'p' but not 'g'.
//
// The protocol is also underspecified with regards to how the stub should
// handle a multithreaded inferior. Its default mode of operation is
// "all-stop mode", when a thread hits a breakpoint all other threads are
// also stopped. But the protocol doesn't say what happens if a second
// thread hits a breakpoint while the stub is in the process of stopping all
// other threads.
//
// In practice the stub is allowed to swallow the second breakpoint hit or
// to return it at a later time. If the stub chooses the latter behavior
// (like gdbserver does) it is allowed to return delayed events on *any*
// vCont packet. This is incredibly inconvenient since if we get notified
// about a delayed breakpoint while we are trying to singlestep another
// thread it's impossible to know when the singlestep we requested ended.
//
// What this means is that gdbserver can only be supported for multithreaded
// inferiors by selecting non-stop mode, which behaves in a markedly
// different way from all-stop mode and isn't supported by anything except
// gdbserver.
//
// lldb-server/debugserver takes a different approach, only the first stop
// event is reported, if any other event happens "simultaneously" they are
// suppressed by the stub and the debugger can query for them using
// qThreadStopInfo. This is much easier for us to implement and the
// implementation gracefully degrades to the case where qThreadStopInfo is
// unavailable but the inferior is run in single threaded mode.
//
// Therefore the following code will assume lldb-server-like behavior.

package gdbserial

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"errors"
	"fmt"
	"go/ast"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/arch/x86/x86asm"

	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/linutil"
	isatty "github.com/mattn/go-isatty"
)

const (
	gdbWireFullStopPacket = false
	gdbWireMaxLen         = 120

	maxTransmitAttempts    = 3    // number of retransmission attempts on failed checksum
	initialInputBufferSize = 2048 // size of the input buffer for gdbConn
)

const heartbeatInterval = 10 * time.Second

// ErrDirChange is returned when trying to change execution direction
// while there are still internal breakpoints set.
var ErrDirChange = errors.New("direction change with internal breakpoints")

// Process implements proc.Process using a connection to a debugger stub
// that understands Gdb Remote Serial Protocol.
type gdbProcess struct {
	bi   *proc.BinaryInfo
	conn gdbConn

	threads       map[int]*gdbThread
	currentThread *gdbThread

	exited, detached bool
	ctrlC            bool // ctrl-c was sent to stop inferior

	manualStopRequested bool

	breakpoints proc.BreakpointMap

	gcmdok         bool   // true if the stub supports g and G commands
	threadStopInfo bool   // true if the stub supports qThreadStopInfo
	tracedir       string // if attached to rr the path to the trace directory

	loadGInstrAddr uint64 // address of the g loading instruction, zero if we couldn't allocate it

	process  *os.Process
	waitChan chan *os.ProcessState

	onDetach func() // called after a successful detach
}

var _ proc.ProcessInternal = &gdbProcess{}

// Thread represents an operating system thread.
type gdbThread struct {
	ID                int
	strID             string
	regs              gdbRegisters
	CurrentBreakpoint proc.BreakpointState
	p                 *gdbProcess
	sig               uint8 // signal received by thread after last stop
	setbp             bool  // thread was stopped because of a breakpoint
	common            proc.CommonThread
}

// ErrBackendUnavailable is returned when the stub program can not be found.
type ErrBackendUnavailable struct{}

func (err *ErrBackendUnavailable) Error() string {
	return "backend unavailable"
}

// gdbRegisters represents the current value of the registers of a thread.
// The storage space for all the registers is allocated as a single memory
// block in buf, the value field inside an individual gdbRegister will be a
// slice of the global buf field.
type gdbRegisters struct {
	regs     map[string]gdbRegister
	regsInfo []gdbRegisterInfo
	tls      uint64
	gaddr    uint64
	hasgaddr bool
	buf      []byte
}

type gdbRegister struct {
	value  []byte
	regnum int
}

// newProcess creates a new Process instance.
// If process is not nil it is the stub's process and will be killed after
// Detach.
// Use Listen, Dial or Connect to complete connection.
func newProcess(process *os.Process) *gdbProcess {
	logger := logflags.GdbWireLogger()
	p := &gdbProcess{
		conn: gdbConn{
			maxTransmitAttempts: maxTransmitAttempts,
			inbuf:               make([]byte, 0, initialInputBufferSize),
			direction:           proc.Forward,
			log:                 logger,
		},
		threads:        make(map[int]*gdbThread),
		bi:             proc.NewBinaryInfo(runtime.GOOS, runtime.GOARCH),
		breakpoints:    proc.NewBreakpointMap(),
		gcmdok:         true,
		threadStopInfo: true,
		process:        process,
	}

	if process != nil {
		p.waitChan = make(chan *os.ProcessState)
		go func() {
			state, _ := process.Wait()
			p.waitChan <- state
		}()
	}

	return p
}

// Listen waits for a connection from the stub.
func (p *gdbProcess) Listen(listener net.Listener, path string, pid int, debugInfoDirs []string, stopReason proc.StopReason) (*proc.Target, error) {
	acceptChan := make(chan net.Conn)

	go func() {
		conn, _ := listener.Accept()
		acceptChan <- conn
	}()

	select {
	case conn := <-acceptChan:
		listener.Close()
		if conn == nil {
			return nil, errors.New("could not connect")
		}
		return p.Connect(conn, path, pid, debugInfoDirs, stopReason)
	case status := <-p.waitChan:
		listener.Close()
		return nil, fmt.Errorf("stub exited while waiting for connection: %v", status)
	}
}

// Dial attempts to connect to the stub.
func (p *gdbProcess) Dial(addr string, path string, pid int, debugInfoDirs []string, stopReason proc.StopReason) (*proc.Target, error) {
	for {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			return p.Connect(conn, path, pid, debugInfoDirs, stopReason)
		}
		select {
		case status := <-p.waitChan:
			return nil, fmt.Errorf("stub exited while attempting to connect: %v", status)
		default:
		}
		time.Sleep(time.Second)
	}
}

// Connect connects to a stub and performs a handshake.
//
// Path and pid are, respectively, the path to the executable of the target
// program and the PID of the target process, both are optional, however
// some stubs do not provide ways to determine path and pid automatically
// and Connect will be unable to function without knowing them.
func (p *gdbProcess) Connect(conn net.Conn, path string, pid int, debugInfoDirs []string, stopReason proc.StopReason) (*proc.Target, error) {
	p.conn.conn = conn
	p.conn.pid = pid
	err := p.conn.handshake()
	if err != nil {
		conn.Close()
		return nil, err
	}

	if verbuf, err := p.conn.exec([]byte("$qGDBServerVersion"), "init"); err == nil {
		for _, v := range strings.Split(string(verbuf), ";") {
			if strings.HasPrefix(v, "version:") {
				if v[len("version:"):] == "902" {
					// Workaround for https://bugs.llvm.org/show_bug.cgi?id=36968, 'g' command crashes a version of debugserver on some systems (?)
					p.gcmdok = false
					break
				}
			}
		}
	}

	tgt, err := p.initialize(path, debugInfoDirs, stopReason)
	if err != nil {
		return nil, err
	}

	// None of the stubs we support returns the value of fs_base or gs_base
	// along with the registers, therefore we have to resort to executing a MOV
	// instruction on the inferior to find out where the G struct of a given
	// thread is located.
	// Here we try to allocate some memory on the inferior which we will use to
	// store the MOV instruction.
	// If the stub doesn't support memory allocation reloadRegisters will
	// overwrite some existing memory to store the MOV.
	if addr, err := p.conn.allocMemory(256); err == nil {
		if _, err := p.conn.writeMemory(uintptr(addr), p.loadGInstr()); err == nil {
			p.loadGInstrAddr = addr
		}
	}

	return tgt, nil
}

// unusedPort returns an unused tcp port
// This is a hack and subject to a race condition with other running
// programs, but most (all?) OS will cycle through all ephemeral ports
// before reassigning one port they just assigned, unless there's heavy
// churn in the ephemeral range this should work.
func unusedPort() string {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return ":8081"
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return fmt.Sprintf(":%d", port)
}

const debugserverExecutable = "/Library/Developer/CommandLineTools/Library/PrivateFrameworks/LLDB.framework/Versions/A/Resources/debugserver"

// ErrUnsupportedOS is returned when trying to use the lldb backend on Windows.
var ErrUnsupportedOS = errors.New("lldb backend not supported on Windows")

func getLdEnvVars() []string {
	var result []string

	environ := os.Environ()
	for i := 0; i < len(environ); i++ {
		if strings.HasPrefix(environ[i], "LD_") ||
			strings.HasPrefix(environ[i], "DYLD_") {
			result = append(result, "-e", environ[i])
		}
	}

	return result
}

// LLDBLaunch starts an instance of lldb-server and connects to it, asking
// it to launch the specified target program with the specified arguments
// (cmd) on the specified directory wd.
func LLDBLaunch(cmd []string, wd string, foreground bool, debugInfoDirs []string, tty string) (*proc.Target, error) {
	if runtime.GOOS == "windows" {
		return nil, ErrUnsupportedOS
	}

	if foreground {
		// Disable foregrounding if we can't open /dev/tty or debugserver will
		// crash. See issue #1215.
		if !isatty.IsTerminal(os.Stdin.Fd()) {
			foreground = false
		}
	}

	isDebugserver := false

	var listener net.Listener
	var port string
	var process *exec.Cmd
	if _, err := os.Stat(debugserverExecutable); err == nil {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		ldEnvVars := getLdEnvVars()
		args := make([]string, 0, len(cmd)+4+len(ldEnvVars))
		args = append(args, ldEnvVars...)
		if foreground {
			args = append(args, "--stdio-path", "/dev/tty")
		}
		if tty != "" {
			args = append(args, "--stdio-path", tty)
		}
		if logflags.LLDBServerOutput() {
			args = append(args, "-g", "-l", "stdout")
		}
		args = append(args, "-F", "-R", fmt.Sprintf("127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port), "--")
		args = append(args, cmd...)

		isDebugserver = true

		process = exec.Command(debugserverExecutable, args...)
	} else {
		if _, err := exec.LookPath("lldb-server"); err != nil {
			return nil, &ErrBackendUnavailable{}
		}
		port = unusedPort()
		args := make([]string, 0, len(cmd)+3)
		args = append(args, "gdbserver", port, "--")
		args = append(args, cmd...)

		process = exec.Command("lldb-server", args...)
	}

	if logflags.LLDBServerOutput() || logflags.GdbWire() || foreground {
		process.Stdout = os.Stdout
		process.Stderr = os.Stderr
	}
	if foreground {
		foregroundSignalsIgnore()
		process.Stdin = os.Stdin
	}
	if wd != "" {
		process.Dir = wd
	}

	process.SysProcAttr = sysProcAttr(foreground)

	if runtime.GOOS == "darwin" {
		process.Env = proc.DisableAsyncPreemptEnv()
	}

	err := process.Start()
	if err != nil {
		return nil, err
	}

	p := newProcess(process.Process)
	p.conn.isDebugserver = isDebugserver

	var tgt *proc.Target
	if listener != nil {
		tgt, err = p.Listen(listener, cmd[0], 0, debugInfoDirs, proc.StopLaunched)
	} else {
		tgt, err = p.Dial(port, cmd[0], 0, debugInfoDirs, proc.StopLaunched)
	}
	return tgt, err
}

// LLDBAttach starts an instance of lldb-server and connects to it, asking
// it to attach to the specified pid.
// Path is path to the target's executable, path only needs to be specified
// for some stubs that do not provide an automated way of determining it
// (for example debugserver).
func LLDBAttach(pid int, path string, debugInfoDirs []string) (*proc.Target, error) {
	if runtime.GOOS == "windows" {
		return nil, ErrUnsupportedOS
	}

	isDebugserver := false
	var process *exec.Cmd
	var listener net.Listener
	var port string
	if _, err := os.Stat(debugserverExecutable); err == nil {
		isDebugserver = true
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		process = exec.Command(debugserverExecutable, "-R", fmt.Sprintf("127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port), "--attach="+strconv.Itoa(pid))
	} else {
		if _, err := exec.LookPath("lldb-server"); err != nil {
			return nil, &ErrBackendUnavailable{}
		}
		port = unusedPort()
		process = exec.Command("lldb-server", "gdbserver", "--attach", strconv.Itoa(pid), port)
	}

	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	process.SysProcAttr = sysProcAttr(false)

	err := process.Start()
	if err != nil {
		return nil, err
	}

	p := newProcess(process.Process)
	p.conn.isDebugserver = isDebugserver

	var tgt *proc.Target
	if listener != nil {
		tgt, err = p.Listen(listener, path, pid, debugInfoDirs, proc.StopAttached)
	} else {
		tgt, err = p.Dial(port, path, pid, debugInfoDirs, proc.StopAttached)
	}
	return tgt, err
}

// EntryPoint will return the process entry point address, useful for
// debugging PIEs.
func (p *gdbProcess) EntryPoint() (uint64, error) {
	var entryPoint uint64
	if auxv, err := p.conn.readAuxv(); err == nil {
		// If we can't read the auxiliary vector it just means it's not supported
		// by the OS or by the stub. If we are debugging a PIE and the entry point
		// is needed proc.LoadBinaryInfo will complain about it.
		entryPoint = linutil.EntryPointFromAuxv(auxv, p.BinInfo().Arch.PtrSize())
	}
	return entryPoint, nil
}

// initialize uses qProcessInfo to load the inferior's PID and
// executable path. This command is not supported by all stubs and not all
// stubs will report both the PID and executable path.
func (p *gdbProcess) initialize(path string, debugInfoDirs []string, stopReason proc.StopReason) (*proc.Target, error) {
	var err error
	if path == "" {
		// If we are attaching to a running process and the user didn't specify
		// the executable file manually we must ask the stub for it.
		// We support both qXfer:exec-file:read:: (the gdb way) and calling
		// qProcessInfo (the lldb way).
		// Unfortunately debugserver on macOS supports neither.
		path, err = p.conn.readExecFile()
		if err != nil {
			if isProtocolErrorUnsupported(err) {
				_, path, err = queryProcessInfo(p, p.Pid())
				if err != nil {
					p.conn.conn.Close()
					return nil, err
				}
			} else {
				p.conn.conn.Close()
				return nil, fmt.Errorf("could not determine executable path: %v", err)
			}
		}
	}

	if path == "" {
		// try using jGetLoadedDynamicLibrariesInfos which is the only way to do
		// this supported on debugserver (but only on macOS >= 12.10)
		images, _ := p.conn.getLoadedDynamicLibraries()
		for _, image := range images {
			if image.MachHeader.FileType == macho.TypeExec {
				path = image.Pathname
				break
			}
		}
	}

	err = p.updateThreadList(&threadUpdater{p: p})
	if err != nil {
		p.conn.conn.Close()
		p.bi.Close()
		return nil, err
	}
	p.clearThreadSignals()

	if p.conn.pid <= 0 {
		p.conn.pid, _, err = queryProcessInfo(p, 0)
		if err != nil && !isProtocolErrorUnsupported(err) {
			p.conn.conn.Close()
			p.bi.Close()
			return nil, err
		}
	}
	tgt, err := proc.NewTarget(p, proc.NewTargetConfig{
		Path:                path,
		DebugInfoDirs:       debugInfoDirs,
		WriteBreakpoint:     p.writeBreakpoint,
		DisableAsyncPreempt: runtime.GOOS == "darwin",
		StopReason:          stopReason})
	if err != nil {
		p.conn.conn.Close()
		return nil, err
	}
	return tgt, nil
}

func queryProcessInfo(p *gdbProcess, pid int) (int, string, error) {
	pi, err := p.conn.queryProcessInfo(pid)
	if err != nil {
		return 0, "", err
	}
	if pid == 0 {
		n, _ := strconv.ParseUint(pi["pid"], 16, 64)
		pid = int(n)
	}
	return pid, pi["name"], nil
}

// BinInfo returns information on the binary.
func (p *gdbProcess) BinInfo() *proc.BinaryInfo {
	return p.bi
}

// Recorded returns whether or not we are debugging
// a recorded "traced" program.
func (p *gdbProcess) Recorded() (bool, string) {
	return p.tracedir != "", p.tracedir
}

// Pid returns the process ID.
func (p *gdbProcess) Pid() int {
	return int(p.conn.pid)
}

// Valid returns true if we are not detached
// and the process has not exited.
func (p *gdbProcess) Valid() (bool, error) {
	if p.detached {
		return false, proc.ErrProcessDetached
	}
	if p.exited {
		return false, &proc.ErrProcessExited{Pid: p.Pid()}
	}
	return true, nil
}

// ResumeNotify specifies a channel that will be closed the next time
// ContinueOnce finishes resuming the target.
func (p *gdbProcess) ResumeNotify(ch chan<- struct{}) {
	p.conn.resumeChan = ch
}

// FindThread returns the thread with the given ID.
func (p *gdbProcess) FindThread(threadID int) (proc.Thread, bool) {
	thread, ok := p.threads[threadID]
	return thread, ok
}

// ThreadList returns all threads in the process.
func (p *gdbProcess) ThreadList() []proc.Thread {
	r := make([]proc.Thread, 0, len(p.threads))
	for _, thread := range p.threads {
		r = append(r, thread)
	}
	return r
}

// CurrentThread returns the current active
// selected thread.
func (p *gdbProcess) CurrentThread() proc.Thread {
	return p.currentThread
}

// SetCurrentThread is used internally by proc.Target to change the current thread.
func (p *gdbProcess) SetCurrentThread(th proc.Thread) {
	p.currentThread = th.(*gdbThread)
}

const (
	interruptSignal  = 0x2
	breakpointSignal = 0x5
	faultSignal      = 0xb
	childSignal      = 0x11
	stopSignal       = 0x13

	debugServerTargetExcBadAccess      = 0x91
	debugServerTargetExcBadInstruction = 0x92
	debugServerTargetExcArithmetic     = 0x93
	debugServerTargetExcEmulation      = 0x94
	debugServerTargetExcSoftware       = 0x95
	debugServerTargetExcBreakpoint     = 0x96
)

// ContinueOnce will continue execution of the process until
// a breakpoint is hit or signal is received.
func (p *gdbProcess) ContinueOnce() (proc.Thread, proc.StopReason, error) {
	if p.exited {
		return nil, proc.StopExited, &proc.ErrProcessExited{Pid: p.conn.pid}
	}

	if p.conn.direction == proc.Forward {
		// step threads stopped at any breakpoint over their breakpoint
		for _, thread := range p.threads {
			if thread.CurrentBreakpoint.Breakpoint != nil {
				if err := thread.stepInstruction(&threadUpdater{p: p}); err != nil {
					return nil, proc.StopUnknown, err
				}
			}
		}
	}

	for _, th := range p.threads {
		th.clearBreakpointState()
	}

	p.setCtrlC(false)

	// resume all threads
	var threadID string
	var trapthread *gdbThread
	var tu = threadUpdater{p: p}
	var atstart bool
continueLoop:
	for {
		var err error
		var sig uint8
		tu.Reset()
		threadID, sig, err = p.conn.resume(p.threads, &tu)
		if err != nil {
			if _, exited := err.(proc.ErrProcessExited); exited {
				p.exited = true
			}
			return nil, proc.StopExited, err
		}

		// For stubs that support qThreadStopInfo updateThreadList will
		// find out the reason why each thread stopped.
		p.updateThreadList(&tu)

		trapthread = p.findThreadByStrID(threadID)
		if trapthread != nil && !p.threadStopInfo {
			// For stubs that do not support qThreadStopInfo we manually set the
			// reason the thread returned by resume() stopped.
			trapthread.sig = sig
		}

		var shouldStop bool
		trapthread, atstart, shouldStop = p.handleThreadSignals(trapthread)
		if shouldStop {
			break continueLoop
		}
	}

	p.clearThreadRegisters()

	stopReason := proc.StopUnknown
	if atstart {
		stopReason = proc.StopLaunched
	}

	if p.BinInfo().GOOS == "linux" {
		if err := linutil.ElfUpdateSharedObjects(p); err != nil {
			return nil, stopReason, err
		}
	}

	if err := p.setCurrentBreakpoints(); err != nil {
		return nil, stopReason, err
	}

	if trapthread == nil {
		return nil, stopReason, fmt.Errorf("could not find thread %s", threadID)
	}

	var err error
	switch trapthread.sig {
	case 0x91:
		err = errors.New("bad access")
	case 0x92:
		err = errors.New("bad instruction")
	case 0x93:
		err = errors.New("arithmetic exception")
	case 0x94:
		err = errors.New("emulation exception")
	case 0x95:
		err = errors.New("software exception")
	case 0x96:
		err = errors.New("breakpoint exception")
	}
	if err != nil {
		// the signals that are reported here can not be propagated back to the target process.
		trapthread.sig = 0
	}
	return trapthread, stopReason, err
}

func (p *gdbProcess) findThreadByStrID(threadID string) *gdbThread {
	for _, thread := range p.threads {
		if thread.strID == threadID {
			return thread
		}
	}
	return nil
}

// handleThreadSignals looks at the signals received by each thread and
// decides which ones to mask and which ones to propagate back to the target
// and returns true if we should stop execution in response to one of the
// signals and return control to the user.
// Adjusts trapthread to a thread that we actually want to stop at.
func (p *gdbProcess) handleThreadSignals(trapthread *gdbThread) (trapthreadOut *gdbThread, atstart bool, shouldStop bool) {
	var trapthreadCandidate *gdbThread

	for _, th := range p.threads {
		isStopSignal := false

		// 0x5 is always a breakpoint, a manual stop either manifests as 0x13
		// (lldb), 0x11 (debugserver) or 0x2 (gdbserver).
		// Since 0x2 could also be produced by the user
		// pressing ^C (in which case it should be passed to the inferior) we need
		// the ctrlC flag to know that we are the originators.
		switch th.sig {
		case interruptSignal: // interrupt
			if p.getCtrlC() {
				isStopSignal = true
			}
		case breakpointSignal: // breakpoint
			isStopSignal = true
		case childSignal: // stop on debugserver but SIGCHLD on lldb-server/linux
			if p.conn.isDebugserver {
				isStopSignal = true
			}
		case stopSignal: // stop
			isStopSignal = true

		// The following are fake BSD-style signals sent by debugserver
		// Unfortunately debugserver can not convert them into signals for the
		// process so we must stop here.
		case debugServerTargetExcBadAccess, debugServerTargetExcBadInstruction, debugServerTargetExcArithmetic, debugServerTargetExcEmulation, debugServerTargetExcSoftware, debugServerTargetExcBreakpoint:

			trapthreadCandidate = th
			shouldStop = true

		// Signal 0 is returned by rr when it reaches the start of the process
		// in backward continue mode.
		case 0:
			if p.conn.direction == proc.Backward && th == trapthread {
				isStopSignal = true
				atstart = true
			}

		default:
			// any other signal is always propagated to inferior
		}

		if isStopSignal {
			if trapthreadCandidate == nil {
				trapthreadCandidate = th
			}
			th.sig = 0
			shouldStop = true
		}
	}

	if (trapthread == nil || trapthread.sig != 0) && trapthreadCandidate != nil {
		// proc.Continue wants us to return one of the threads that we should stop
		// at, if the thread returned by vCont received a signal that we want to
		// propagate back to the target thread but there were also other threads
		// that we wish to stop at we should pick one of those.
		trapthread = trapthreadCandidate
	}

	if p.getCtrlC() || p.getManualStopRequested() {
		// If we request an interrupt and a target thread simultaneously receives
		// an unrelated singal debugserver will discard our interrupt request and
		// report the signal but we should stop anyway.
		shouldStop = true
	}

	return trapthread, atstart, shouldStop
}

// RequestManualStop will attempt to stop the process
// without a breakpoint or signal having been recieved.
func (p *gdbProcess) RequestManualStop() error {
	p.conn.manualStopMutex.Lock()
	p.manualStopRequested = true
	if !p.conn.running {
		p.conn.manualStopMutex.Unlock()
		return nil
	}
	p.ctrlC = true
	p.conn.manualStopMutex.Unlock()
	return p.conn.sendCtrlC()
}

// CheckAndClearManualStopRequest will check for a manual
// stop and then clear that state.
func (p *gdbProcess) CheckAndClearManualStopRequest() bool {
	p.conn.manualStopMutex.Lock()
	msr := p.manualStopRequested
	p.manualStopRequested = false
	p.conn.manualStopMutex.Unlock()
	return msr
}

func (p *gdbProcess) getManualStopRequested() bool {
	p.conn.manualStopMutex.Lock()
	msr := p.manualStopRequested
	p.conn.manualStopMutex.Unlock()
	return msr
}

func (p *gdbProcess) setCtrlC(v bool) {
	p.conn.manualStopMutex.Lock()
	p.ctrlC = v
	p.conn.manualStopMutex.Unlock()
}

func (p *gdbProcess) getCtrlC() bool {
	p.conn.manualStopMutex.Lock()
	defer p.conn.manualStopMutex.Unlock()
	return p.ctrlC
}

// Detach will detach from the target process,
// if 'kill' is true it will also kill the process.
func (p *gdbProcess) Detach(kill bool) error {
	if kill && !p.exited {
		err := p.conn.kill()
		if err != nil {
			if _, exited := err.(proc.ErrProcessExited); !exited {
				return err
			}
			p.exited = true
		}
	}
	if !p.exited {
		if err := p.conn.detach(); err != nil {
			return err
		}
	}
	if p.process != nil {
		p.process.Kill()
		<-p.waitChan
		p.process = nil
	}
	p.detached = true
	if p.onDetach != nil {
		p.onDetach()
	}
	return p.bi.Close()
}

// Restart will restart the process from the given position.
func (p *gdbProcess) Restart(pos string) error {
	if p.tracedir == "" {
		return proc.ErrNotRecorded
	}

	p.exited = false

	for _, th := range p.threads {
		th.clearBreakpointState()
	}

	p.setCtrlC(false)

	err := p.conn.restart(pos)
	if err != nil {
		return err
	}

	// for some reason we have to send a vCont;c after a vRun to make rr behave
	// properly, because that's what gdb does.
	_, _, err = p.conn.resume(nil, nil)
	if err != nil {
		return err
	}

	err = p.updateThreadList(&threadUpdater{p: p})
	if err != nil {
		return err
	}
	p.clearThreadSignals()
	p.clearThreadRegisters()

	for addr := range p.breakpoints.M {
		p.conn.setBreakpoint(addr)
	}

	return p.setCurrentBreakpoints()
}

// When executes the 'when' command for the Mozilla RR backend.
// This command will return rr's internal event number.
func (p *gdbProcess) When() (string, error) {
	if p.tracedir == "" {
		return "", proc.ErrNotRecorded
	}
	event, err := p.conn.qRRCmd("when")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(event), nil
}

const (
	checkpointPrefix = "Checkpoint "
)

// Checkpoint creates a checkpoint from which you can restart the program.
func (p *gdbProcess) Checkpoint(where string) (int, error) {
	if p.tracedir == "" {
		return -1, proc.ErrNotRecorded
	}
	resp, err := p.conn.qRRCmd("checkpoint", where)
	if err != nil {
		return -1, err
	}

	if !strings.HasPrefix(resp, checkpointPrefix) {
		return -1, fmt.Errorf("can not parse checkpoint response %q", resp)
	}

	idstr := resp[len(checkpointPrefix):]
	space := strings.Index(idstr, " ")
	if space < 0 {
		return -1, fmt.Errorf("can not parse checkpoint response %q", resp)
	}
	idstr = idstr[:space]

	cpid, err := strconv.Atoi(idstr)
	if err != nil {
		return -1, err
	}
	return cpid, nil
}

// Checkpoints returns a list of all checkpoints set.
func (p *gdbProcess) Checkpoints() ([]proc.Checkpoint, error) {
	if p.tracedir == "" {
		return nil, proc.ErrNotRecorded
	}
	resp, err := p.conn.qRRCmd("info checkpoints")
	if err != nil {
		return nil, err
	}
	lines := strings.Split(resp, "\n")
	r := make([]proc.Checkpoint, 0, len(lines)-1)
	for _, line := range lines[1:] {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			return nil, fmt.Errorf("can not parse \"info checkpoints\" output line %q", line)
		}
		cpid, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("can not parse \"info checkpoints\" output line %q: %v", line, err)
		}
		r = append(r, proc.Checkpoint{ID: cpid, When: fields[1], Where: fields[2]})
	}
	return r, nil
}

const deleteCheckpointPrefix = "Deleted checkpoint "

// ClearCheckpoint clears the checkpoint for the given ID.
func (p *gdbProcess) ClearCheckpoint(id int) error {
	if p.tracedir == "" {
		return proc.ErrNotRecorded
	}
	resp, err := p.conn.qRRCmd("delete checkpoint", strconv.Itoa(id))
	if err != nil {
		return err
	}
	if !strings.HasPrefix(resp, deleteCheckpointPrefix) {
		return errors.New(resp)
	}
	return nil
}

// ChangeDirection sets whether to run the program forwards or in reverse execution.
func (p *gdbProcess) ChangeDirection(dir proc.Direction) error {
	if p.tracedir == "" {
		if dir != proc.Forward {
			return proc.ErrNotRecorded
		}
		return nil
	}
	if p.conn.conn == nil {
		return proc.ErrProcessExited{Pid: p.conn.pid}
	}
	if p.conn.direction == dir {
		return nil
	}
	if p.Breakpoints().HasInternalBreakpoints() {
		return ErrDirChange
	}
	p.conn.direction = dir
	return nil
}

// GetDirection returns the current direction of execution.
func (p *gdbProcess) GetDirection() proc.Direction {
	return p.conn.direction
}

// Breakpoints returns the list of breakpoints currently set.
func (p *gdbProcess) Breakpoints() *proc.BreakpointMap {
	return &p.breakpoints
}

// FindBreakpoint returns the breakpoint at the given address.
func (p *gdbProcess) FindBreakpoint(pc uint64) (*proc.Breakpoint, bool) {
	// Directly use addr to lookup breakpoint.
	if bp, ok := p.breakpoints.M[pc]; ok {
		return bp, true
	}
	return nil, false
}

func (p *gdbProcess) writeBreakpoint(addr uint64) (string, int, *proc.Function, []byte, error) {
	f, l, fn := p.bi.PCToLine(uint64(addr))

	if err := p.conn.setBreakpoint(addr); err != nil {
		return "", 0, nil, nil, err
	}

	return f, l, fn, nil, nil
}

// SetBreakpoint creates a new breakpoint.
func (p *gdbProcess) SetBreakpoint(addr uint64, kind proc.BreakpointKind, cond ast.Expr) (*proc.Breakpoint, error) {
	if p.exited {
		return nil, &proc.ErrProcessExited{Pid: p.conn.pid}
	}
	return p.breakpoints.Set(addr, kind, cond, p.writeBreakpoint)
}

// ClearBreakpoint clears a breakpoint at the given address.
func (p *gdbProcess) ClearBreakpoint(addr uint64) (*proc.Breakpoint, error) {
	if p.exited {
		return nil, &proc.ErrProcessExited{Pid: p.conn.pid}
	}
	return p.breakpoints.Clear(addr, func(bp *proc.Breakpoint) error {
		return p.conn.clearBreakpoint(bp.Addr)
	})
}

// ClearInternalBreakpoints clear all internal use breakpoints like those set by 'next'.
func (p *gdbProcess) ClearInternalBreakpoints() error {
	return p.breakpoints.ClearInternalBreakpoints(func(bp *proc.Breakpoint) error {
		if err := p.conn.clearBreakpoint(bp.Addr); err != nil {
			return err
		}
		for _, thread := range p.threads {
			if thread.CurrentBreakpoint.Breakpoint == bp {
				thread.clearBreakpointState()
			}
		}
		return nil
	})
}

type threadUpdater struct {
	p    *gdbProcess
	seen map[int]bool
	done bool
}

func (tu *threadUpdater) Reset() {
	tu.done = false
	tu.seen = nil
}

func (tu *threadUpdater) Add(threads []string) error {
	if tu.done {
		panic("threadUpdater: Add after Finish")
	}
	if tu.seen == nil {
		tu.seen = map[int]bool{}
	}
	for _, threadID := range threads {
		b := threadID
		if period := strings.Index(b, "."); period >= 0 {
			b = b[period+1:]
		}
		n, err := strconv.ParseUint(b, 16, 32)
		if err != nil {
			return &GdbMalformedThreadIDError{threadID}
		}
		tid := int(n)
		tu.seen[tid] = true
		if _, found := tu.p.threads[tid]; !found {
			tu.p.threads[tid] = &gdbThread{ID: tid, strID: threadID, p: tu.p}
		}
	}
	return nil
}

func (tu *threadUpdater) Finish() {
	tu.done = true
	for threadID := range tu.p.threads {
		_, threadSeen := tu.seen[threadID]
		if threadSeen {
			continue
		}
		delete(tu.p.threads, threadID)
		if tu.p.currentThread != nil && tu.p.currentThread.ID == threadID {
			tu.p.currentThread = nil
		}
	}
	if tu.p.currentThread != nil {
		if _, exists := tu.p.threads[tu.p.currentThread.ID]; !exists {
			// current thread was removed
			tu.p.currentThread = nil
		}
	}
	if tu.p.currentThread == nil {
		for _, thread := range tu.p.threads {
			tu.p.currentThread = thread
			break
		}
	}
}

// updateThreadList retrieves the list of inferior threads from
// the stub and passes it to threadUpdater.
// Some stubs will return the list of running threads in the stop packet, if
// this happens the threadUpdater will know that we have already updated the
// thread list and the first step of updateThreadList will be skipped.
func (p *gdbProcess) updateThreadList(tu *threadUpdater) error {
	if !tu.done {
		first := true
		for {
			threads, err := p.conn.queryThreads(first)
			if err != nil {
				return err
			}
			if len(threads) == 0 {
				break
			}
			first = false
			if err := tu.Add(threads); err != nil {
				return err
			}
		}

		tu.Finish()
	}

	for _, th := range p.threads {
		if p.threadStopInfo {
			sig, reason, err := p.conn.threadStopInfo(th.strID)
			if err != nil {
				if isProtocolErrorUnsupported(err) {
					p.threadStopInfo = false
					break
				}
				return err
			}
			th.setbp = (reason == "breakpoint" || (reason == "" && sig == breakpointSignal))
			th.sig = sig
		} else {
			th.sig = 0
		}
	}

	return nil
}

// clearThreadRegisters clears the memoized thread register state.
func (p *gdbProcess) clearThreadRegisters() {
	for _, thread := range p.threads {
		thread.regs.regs = nil
	}
}

func (p *gdbProcess) clearThreadSignals() {
	for _, th := range p.threads {
		th.sig = 0
	}
}

func (p *gdbProcess) setCurrentBreakpoints() error {
	if p.threadStopInfo {
		for _, th := range p.threads {
			if th.setbp {
				err := th.SetCurrentBreakpoint(true)
				if err != nil {
					return err
				}
			}
		}
	}
	if !p.threadStopInfo {
		for _, th := range p.threads {
			if th.CurrentBreakpoint.Breakpoint == nil {
				err := th.SetCurrentBreakpoint(true)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// ReadMemory will read into 'data' memory at the address provided.
func (t *gdbThread) ReadMemory(data []byte, addr uintptr) (n int, err error) {
	err = t.p.conn.readMemory(data, addr)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

// WriteMemory will write into the memory at 'addr' the data provided.
func (t *gdbThread) WriteMemory(addr uintptr, data []byte) (written int, err error) {
	return t.p.conn.writeMemory(addr, data)
}

// Location returns the current location of this thread.
func (t *gdbThread) Location() (*proc.Location, error) {
	regs, err := t.Registers(false)
	if err != nil {
		return nil, err
	}
	if pcreg, ok := regs.(*gdbRegisters).regs[regnamePC]; !ok {
		t.p.conn.log.Errorf("thread %d could not find RIP register", t.ID)
	} else if len(pcreg.value) < t.p.bi.Arch.PtrSize() {
		t.p.conn.log.Errorf("thread %d bad length for RIP register: %d", t.ID, len(pcreg.value))
	}
	pc := regs.PC()
	f, l, fn := t.p.bi.PCToLine(pc)
	return &proc.Location{PC: pc, File: f, Line: l, Fn: fn}, nil
}

// Breakpoint returns the current active breakpoint for this thread.
func (t *gdbThread) Breakpoint() *proc.BreakpointState {
	return &t.CurrentBreakpoint
}

// ThreadID returns this threads ID.
func (t *gdbThread) ThreadID() int {
	return t.ID
}

// Registers returns the CPU registers for this thread.
func (t *gdbThread) Registers(floatingPoint bool) (proc.Registers, error) {
	if t.regs.regs == nil {
		if err := t.reloadRegisters(); err != nil {
			return nil, err
		}
	}
	return &t.regs, nil
}

// RestoreRegisters will set the CPU registers the value of those provided.
func (t *gdbThread) RestoreRegisters(savedRegs proc.Registers) error {
	copy(t.regs.buf, savedRegs.(*gdbRegisters).buf)
	return t.writeRegisters()
}

// BinInfo will return information on the binary being debugged.
func (t *gdbThread) BinInfo() *proc.BinaryInfo {
	return t.p.bi
}

// Common returns common information across Process implementations.
func (t *gdbThread) Common() *proc.CommonThread {
	return &t.common
}

func (t *gdbThread) stepInstruction(tu *threadUpdater) error {
	pc := t.regs.PC()
	if _, atbp := t.p.breakpoints.M[pc]; atbp {
		err := t.p.conn.clearBreakpoint(pc)
		if err != nil {
			return err
		}
		defer t.p.conn.setBreakpoint(pc)
	}
	// Reset thread registers so the next call to
	// Thread.Registers will not be cached.
	t.regs.regs = nil
	return t.p.conn.step(t.strID, tu, false)
}

// StepInstruction will step exactly 1 CPU instruction.
func (t *gdbThread) StepInstruction() error {
	return t.stepInstruction(&threadUpdater{p: t.p})
}

// Blocked returns true if the thread is blocked in runtime or kernel code.
func (t *gdbThread) Blocked() bool {
	regs, err := t.Registers(false)
	if err != nil {
		return false
	}
	pc := regs.PC()
	f, ln, fn := t.BinInfo().PCToLine(pc)
	if fn == nil {
		if f == "" && ln == 0 {
			return true
		}
		return false
	}
	switch fn.Name {
	case "runtime.futex", "runtime.usleep", "runtime.clone":
		return true
	case "runtime.kevent":
		return true
	case "runtime.mach_semaphore_wait", "runtime.mach_semaphore_timedwait":
		return true
	default:
		return strings.HasPrefix(fn.Name, "syscall.Syscall") || strings.HasPrefix(fn.Name, "syscall.RawSyscall")
	}
}

// loadGInstr returns the correct MOV instruction for the current
// OS/architecture that can be executed to load the address of G from an
// inferior's thread.
func (p *gdbProcess) loadGInstr() []byte {
	var op []byte
	switch p.bi.GOOS {
	case "windows", "darwin", "freebsd":
		// mov rcx, QWORD PTR gs:{uint32(off)}
		op = []byte{0x65, 0x48, 0x8b, 0x0c, 0x25}
	case "linux":
		// mov rcx,QWORD PTR fs:{uint32(off)}
		op = []byte{0x64, 0x48, 0x8B, 0x0C, 0x25}
	default:
		panic("unsupported operating system attempting to find Goroutine on Thread")
	}
	buf := &bytes.Buffer{}
	buf.Write(op)
	binary.Write(buf, binary.LittleEndian, uint32(p.bi.GStructOffset()))
	return buf.Bytes()
}

func (regs *gdbRegisters) init(regsInfo []gdbRegisterInfo) {
	regs.regs = make(map[string]gdbRegister)
	regs.regsInfo = regsInfo

	regsz := 0
	for _, reginfo := range regsInfo {
		if endoff := reginfo.Offset + (reginfo.Bitsize / 8); endoff > regsz {
			regsz = endoff
		}
	}
	regs.buf = make([]byte, regsz)
	for _, reginfo := range regsInfo {
		regs.regs[reginfo.Name] = gdbRegister{regnum: reginfo.Regnum, value: regs.buf[reginfo.Offset : reginfo.Offset+reginfo.Bitsize/8]}
	}
}

// reloadRegisters loads the current value of the thread's registers.
// It will also load the address of the thread's G.
// Loading the address of G can be done in one of two ways reloadGAlloc, if
// the stub can allocate memory, or reloadGAtPC, if the stub can't.
func (t *gdbThread) reloadRegisters() error {
	if t.regs.regs == nil {
		t.regs.init(t.p.conn.regsInfo)
	}

	if t.p.gcmdok {
		if err := t.p.conn.readRegisters(t.strID, t.regs.buf); err != nil {
			if isProtocolErrorUnsupported(err) {
				t.p.gcmdok = false
			} else {
				return err
			}
		}
	}
	if !t.p.gcmdok {
		for _, reginfo := range t.p.conn.regsInfo {
			if err := t.p.conn.readRegister(t.strID, reginfo.Regnum, t.regs.regs[reginfo.Name].value); err != nil {
				return err
			}
		}
	}

	switch t.p.bi.GOOS {
	case "linux":
		if reg, hasFsBase := t.regs.regs[regnameFsBase]; hasFsBase {
			t.regs.gaddr = 0
			t.regs.tls = binary.LittleEndian.Uint64(reg.value)
			t.regs.hasgaddr = false
			return nil
		}
	}

	if t.p.loadGInstrAddr > 0 {
		return t.reloadGAlloc()
	}
	return t.reloadGAtPC()
}

func (t *gdbThread) writeSomeRegisters(regNames ...string) error {
	if t.p.gcmdok {
		return t.p.conn.writeRegisters(t.strID, t.regs.buf)
	}
	for _, regName := range regNames {
		if err := t.p.conn.writeRegister(t.strID, t.regs.regs[regName].regnum, t.regs.regs[regName].value); err != nil {
			return err
		}
	}
	return nil
}

func (t *gdbThread) writeRegisters() error {
	if t.p.gcmdok {
		return t.p.conn.writeRegisters(t.strID, t.regs.buf)
	}
	for _, r := range t.regs.regs {
		if err := t.p.conn.writeRegister(t.strID, r.regnum, r.value); err != nil {
			return err
		}
	}
	return nil
}

func (t *gdbThread) readSomeRegisters(regNames ...string) error {
	if t.p.gcmdok {
		return t.p.conn.readRegisters(t.strID, t.regs.buf)
	}
	for _, regName := range regNames {
		err := t.p.conn.readRegister(t.strID, t.regs.regs[regName].regnum, t.regs.regs[regName].value)
		if err != nil {
			return err
		}
	}
	return nil
}

// reloadGAtPC overwrites the instruction that the thread is stopped at with
// the MOV instruction used to load current G, executes this single
// instruction and then puts everything back the way it was.
func (t *gdbThread) reloadGAtPC() error {
	movinstr := t.p.loadGInstr()

	if t.Blocked() {
		t.regs.tls = 0
		t.regs.gaddr = 0
		t.regs.hasgaddr = true
		return nil
	}

	cx := t.regs.CX()
	pc := t.regs.PC()

	// We are partially replicating the code of GdbserverThread.stepInstruction
	// here.
	// The reason is that lldb-server has a bug with writing to memory and
	// setting/clearing breakpoints to that same memory which we must work
	// around by clearing and re-setting the breakpoint in a specific sequence
	// with the memory writes.
	// Additionally all breakpoints in [pc, pc+len(movinstr)] need to be removed
	for addr := range t.p.breakpoints.M {
		if addr >= pc && addr <= pc+uint64(len(movinstr)) {
			err := t.p.conn.clearBreakpoint(addr)
			if err != nil {
				return err
			}
			defer t.p.conn.setBreakpoint(addr)
		}
	}

	savedcode := make([]byte, len(movinstr))
	_, err := t.ReadMemory(savedcode, uintptr(pc))
	if err != nil {
		return err
	}

	_, err = t.WriteMemory(uintptr(pc), movinstr)
	if err != nil {
		return err
	}

	defer func() {
		_, err0 := t.WriteMemory(uintptr(pc), savedcode)
		if err == nil {
			err = err0
		}
		t.regs.setPC(pc)
		t.regs.setCX(cx)
		err1 := t.writeSomeRegisters(regnamePC, regnameCX)
		if err == nil {
			err = err1
		}
	}()

	err = t.p.conn.step(t.strID, nil, true)
	if err != nil {
		if err == threadBlockedError {
			t.regs.tls = 0
			t.regs.gaddr = 0
			t.regs.hasgaddr = true
			return nil
		}
		return err
	}

	if err := t.readSomeRegisters(regnamePC, regnameCX); err != nil {
		return err
	}

	t.regs.gaddr = t.regs.CX()
	t.regs.hasgaddr = true

	return err
}

// reloadGAlloc makes the specified thread execute one instruction stored at
// t.p.loadGInstrAddr then restores the value of the thread's registers.
// t.p.loadGInstrAddr must point to valid memory on the inferior, containing
// a MOV instruction that loads the address of the current G in the RCX
// register.
func (t *gdbThread) reloadGAlloc() error {
	if t.Blocked() {
		t.regs.tls = 0
		t.regs.gaddr = 0
		t.regs.hasgaddr = true
		return nil
	}

	cx := t.regs.CX()
	pc := t.regs.PC()

	t.regs.setPC(t.p.loadGInstrAddr)
	if err := t.writeSomeRegisters(regnamePC); err != nil {
		return err
	}

	var err error

	defer func() {
		t.regs.setPC(pc)
		t.regs.setCX(cx)
		err1 := t.writeSomeRegisters(regnamePC, regnameCX)
		if err == nil {
			err = err1
		}
	}()

	err = t.p.conn.step(t.strID, nil, true)
	if err != nil {
		if err == threadBlockedError {
			t.regs.tls = 0
			t.regs.gaddr = 0
			t.regs.hasgaddr = true
			return nil
		}
		return err
	}

	if err := t.readSomeRegisters(regnameCX); err != nil {
		return err
	}

	t.regs.gaddr = t.regs.CX()
	t.regs.hasgaddr = true

	return err
}

func (t *gdbThread) clearBreakpointState() {
	t.setbp = false
	t.CurrentBreakpoint.Clear()
}

// SetCurrentBreakpoint will find and set the threads current breakpoint.
func (t *gdbThread) SetCurrentBreakpoint(adjustPC bool) error {
	// adjustPC is ignored, it is the stub's responsibiility to set the PC
	// address correctly after hitting a breakpoint.
	t.clearBreakpointState()
	regs, err := t.Registers(false)
	if err != nil {
		return err
	}
	pc := regs.PC()
	if bp, ok := t.p.FindBreakpoint(pc); ok {
		if t.regs.PC() != bp.Addr {
			if err := t.SetPC(bp.Addr); err != nil {
				return err
			}
		}
		t.CurrentBreakpoint = bp.CheckCondition(t)
		if t.CurrentBreakpoint.Breakpoint != nil && t.CurrentBreakpoint.Active {
			if g, err := proc.GetG(t); err == nil {
				t.CurrentBreakpoint.HitCount[g.ID]++
			}
			t.CurrentBreakpoint.TotalHitCount++
		}
	}
	return nil
}

func (regs *gdbRegisters) PC() uint64 {
	return binary.LittleEndian.Uint64(regs.regs[regnamePC].value)
}

func (regs *gdbRegisters) setPC(value uint64) {
	binary.LittleEndian.PutUint64(regs.regs[regnamePC].value, value)
}

func (regs *gdbRegisters) SP() uint64 {
	return binary.LittleEndian.Uint64(regs.regs[regnameSP].value)
}
func (regs *gdbRegisters) setSP(value uint64) {
	binary.LittleEndian.PutUint64(regs.regs[regnameSP].value, value)
}

func (regs *gdbRegisters) setDX(value uint64) {
	binary.LittleEndian.PutUint64(regs.regs[regnameDX].value, value)
}

func (regs *gdbRegisters) BP() uint64 {
	return binary.LittleEndian.Uint64(regs.regs[regnameBP].value)
}

func (regs *gdbRegisters) CX() uint64 {
	return binary.LittleEndian.Uint64(regs.regs[regnameCX].value)
}

func (regs *gdbRegisters) setCX(value uint64) {
	binary.LittleEndian.PutUint64(regs.regs[regnameCX].value, value)
}

func (regs *gdbRegisters) TLS() uint64 {
	return regs.tls
}

func (regs *gdbRegisters) GAddr() (uint64, bool) {
	return regs.gaddr, regs.hasgaddr
}

func (regs *gdbRegisters) byName(name string) uint64 {
	reg, ok := regs.regs[name]
	if !ok {
		return 0
	}
	return binary.LittleEndian.Uint64(reg.value)
}

func (regs *gdbRegisters) Get(n int) (uint64, error) {
	reg := x86asm.Reg(n)
	const (
		mask8  = 0x000f
		mask16 = 0x00ff
		mask32 = 0xffff
	)

	switch reg {
	// 8-bit
	case x86asm.AL:
		return regs.byName("rax") & mask8, nil
	case x86asm.CL:
		return regs.byName("rcx") & mask8, nil
	case x86asm.DL:
		return regs.byName("rdx") & mask8, nil
	case x86asm.BL:
		return regs.byName("rbx") & mask8, nil
	case x86asm.AH:
		return (regs.byName("rax") >> 8) & mask8, nil
	case x86asm.CH:
		return (regs.byName("rcx") >> 8) & mask8, nil
	case x86asm.DH:
		return (regs.byName("rdx") >> 8) & mask8, nil
	case x86asm.BH:
		return (regs.byName("rbx") >> 8) & mask8, nil
	case x86asm.SPB:
		return regs.byName("rsp") & mask8, nil
	case x86asm.BPB:
		return regs.byName("rbp") & mask8, nil
	case x86asm.SIB:
		return regs.byName("rsi") & mask8, nil
	case x86asm.DIB:
		return regs.byName("rdi") & mask8, nil
	case x86asm.R8B:
		return regs.byName("r8") & mask8, nil
	case x86asm.R9B:
		return regs.byName("r9") & mask8, nil
	case x86asm.R10B:
		return regs.byName("r10") & mask8, nil
	case x86asm.R11B:
		return regs.byName("r11") & mask8, nil
	case x86asm.R12B:
		return regs.byName("r12") & mask8, nil
	case x86asm.R13B:
		return regs.byName("r13") & mask8, nil
	case x86asm.R14B:
		return regs.byName("r14") & mask8, nil
	case x86asm.R15B:
		return regs.byName("r15") & mask8, nil

	// 16-bit
	case x86asm.AX:
		return regs.byName("rax") & mask16, nil
	case x86asm.CX:
		return regs.byName("rcx") & mask16, nil
	case x86asm.DX:
		return regs.byName("rdx") & mask16, nil
	case x86asm.BX:
		return regs.byName("rbx") & mask16, nil
	case x86asm.SP:
		return regs.byName("rsp") & mask16, nil
	case x86asm.BP:
		return regs.byName("rbp") & mask16, nil
	case x86asm.SI:
		return regs.byName("rsi") & mask16, nil
	case x86asm.DI:
		return regs.byName("rdi") & mask16, nil
	case x86asm.R8W:
		return regs.byName("r8") & mask16, nil
	case x86asm.R9W:
		return regs.byName("r9") & mask16, nil
	case x86asm.R10W:
		return regs.byName("r10") & mask16, nil
	case x86asm.R11W:
		return regs.byName("r11") & mask16, nil
	case x86asm.R12W:
		return regs.byName("r12") & mask16, nil
	case x86asm.R13W:
		return regs.byName("r13") & mask16, nil
	case x86asm.R14W:
		return regs.byName("r14") & mask16, nil
	case x86asm.R15W:
		return regs.byName("r15") & mask16, nil

	// 32-bit
	case x86asm.EAX:
		return regs.byName("rax") & mask32, nil
	case x86asm.ECX:
		return regs.byName("rcx") & mask32, nil
	case x86asm.EDX:
		return regs.byName("rdx") & mask32, nil
	case x86asm.EBX:
		return regs.byName("rbx") & mask32, nil
	case x86asm.ESP:
		return regs.byName("rsp") & mask32, nil
	case x86asm.EBP:
		return regs.byName("rbp") & mask32, nil
	case x86asm.ESI:
		return regs.byName("rsi") & mask32, nil
	case x86asm.EDI:
		return regs.byName("rdi") & mask32, nil
	case x86asm.R8L:
		return regs.byName("r8") & mask32, nil
	case x86asm.R9L:
		return regs.byName("r9") & mask32, nil
	case x86asm.R10L:
		return regs.byName("r10") & mask32, nil
	case x86asm.R11L:
		return regs.byName("r11") & mask32, nil
	case x86asm.R12L:
		return regs.byName("r12") & mask32, nil
	case x86asm.R13L:
		return regs.byName("r13") & mask32, nil
	case x86asm.R14L:
		return regs.byName("r14") & mask32, nil
	case x86asm.R15L:
		return regs.byName("r15") & mask32, nil

	// 64-bit
	case x86asm.RAX:
		return regs.byName("rax"), nil
	case x86asm.RCX:
		return regs.byName("rcx"), nil
	case x86asm.RDX:
		return regs.byName("rdx"), nil
	case x86asm.RBX:
		return regs.byName("rbx"), nil
	case x86asm.RSP:
		return regs.byName("rsp"), nil
	case x86asm.RBP:
		return regs.byName("rbp"), nil
	case x86asm.RSI:
		return regs.byName("rsi"), nil
	case x86asm.RDI:
		return regs.byName("rdi"), nil
	case x86asm.R8:
		return regs.byName("r8"), nil
	case x86asm.R9:
		return regs.byName("r9"), nil
	case x86asm.R10:
		return regs.byName("r10"), nil
	case x86asm.R11:
		return regs.byName("r11"), nil
	case x86asm.R12:
		return regs.byName("r12"), nil
	case x86asm.R13:
		return regs.byName("r13"), nil
	case x86asm.R14:
		return regs.byName("r14"), nil
	case x86asm.R15:
		return regs.byName("r15"), nil
	}

	return 0, proc.ErrUnknownRegister
}

// SetPC will set the value of the PC register to the given value.
func (t *gdbThread) SetPC(pc uint64) error {
	t.regs.setPC(pc)
	if t.p.gcmdok {
		return t.p.conn.writeRegisters(t.strID, t.regs.buf)
	}
	reg := t.regs.regs[regnamePC]
	return t.p.conn.writeRegister(t.strID, reg.regnum, reg.value)
}

// SetSP will set the value of the SP register to the given value.
func (t *gdbThread) SetSP(sp uint64) error {
	t.regs.setSP(sp)
	if t.p.gcmdok {
		return t.p.conn.writeRegisters(t.strID, t.regs.buf)
	}
	reg := t.regs.regs[regnameSP]
	return t.p.conn.writeRegister(t.strID, reg.regnum, reg.value)
}

// SetDX will set the value of the DX register to the given value.
func (t *gdbThread) SetDX(dx uint64) error {
	t.regs.setDX(dx)
	if t.p.gcmdok {
		return t.p.conn.writeRegisters(t.strID, t.regs.buf)
	}
	reg := t.regs.regs[regnameDX]
	return t.p.conn.writeRegister(t.strID, reg.regnum, reg.value)
}

func (regs *gdbRegisters) Slice(floatingPoint bool) []proc.Register {
	r := make([]proc.Register, 0, len(regs.regsInfo))
	for _, reginfo := range regs.regsInfo {
		if reginfo.Group == "float" && !floatingPoint {
			continue
		}
		switch {
		case reginfo.Name == "eflags":
			r = proc.AppendBytesRegister(r, "Rflags", regs.regs[reginfo.Name].value)
		case reginfo.Name == "mxcsr":
			r = proc.AppendBytesRegister(r, reginfo.Name, regs.regs[reginfo.Name].value)
		case reginfo.Bitsize == 16:
			r = proc.AppendBytesRegister(r, reginfo.Name, regs.regs[reginfo.Name].value)
		case reginfo.Bitsize == 32:
			r = proc.AppendBytesRegister(r, reginfo.Name, regs.regs[reginfo.Name].value)
		case reginfo.Bitsize == 64:
			r = proc.AppendBytesRegister(r, reginfo.Name, regs.regs[reginfo.Name].value)
		case reginfo.Bitsize == 80:
			if !floatingPoint {
				continue
			}
			idx := 0
			for _, stprefix := range []string{"stmm", "st"} {
				if strings.HasPrefix(reginfo.Name, stprefix) {
					idx, _ = strconv.Atoi(reginfo.Name[len(stprefix):])
					break
				}
			}
			r = proc.AppendBytesRegister(r, fmt.Sprintf("ST(%d)", idx), regs.regs[reginfo.Name].value)

		case reginfo.Bitsize == 128:
			if floatingPoint {
				r = proc.AppendBytesRegister(r, strings.ToUpper(reginfo.Name), regs.regs[reginfo.Name].value)
			}

		case reginfo.Bitsize == 256:
			if !strings.HasPrefix(strings.ToLower(reginfo.Name), "ymm") || !floatingPoint {
				continue
			}

			value := regs.regs[reginfo.Name].value
			xmmName := "x" + reginfo.Name[1:]
			r = proc.AppendBytesRegister(r, strings.ToUpper(xmmName), value[:16])
			r = proc.AppendBytesRegister(r, strings.ToUpper(reginfo.Name), value[16:])
		}
	}
	return r
}

func (regs *gdbRegisters) Copy() proc.Registers {
	savedRegs := &gdbRegisters{}
	savedRegs.init(regs.regsInfo)
	copy(savedRegs.buf, regs.buf)
	return savedRegs
}
