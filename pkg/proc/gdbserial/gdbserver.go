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
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/arch/x86/x86asm"

	"github.com/derekparker/delve/pkg/logflags"
	"github.com/derekparker/delve/pkg/proc"
	"github.com/sirupsen/logrus"
)

const (
	gdbWireFullStopPacket = false
	gdbWireMaxLen         = 120

	maxTransmitAttempts    = 3    // number of retransmission attempts on failed checksum
	initialInputBufferSize = 2048 // size of the input buffer for gdbConn
)

const heartbeatInterval = 10 * time.Second

var ErrDirChange = errors.New("direction change with internal breakpoints")

// Process implements proc.Process using a connection to a debugger stub
// that understands Gdb Remote Serial Protocol.
type Process struct {
	bi   proc.BinaryInfo
	conn gdbConn

	threads           map[int]*Thread
	currentThread     *Thread
	selectedGoroutine *proc.G

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

	common proc.CommonProcess
}

// Thread is a thread.
type Thread struct {
	ID                int
	strID             string
	regs              gdbRegisters
	CurrentBreakpoint proc.BreakpointState
	p                 *Process
	setbp             bool // thread was stopped because of a breakpoint
	common            proc.CommonThread
}

// ErrBackendUnavailable is returned when the stub program can not be found.
type ErrBackendUnavailable struct {
}

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

// New creates a new Process instance.
// If process is not nil it is the stub's process and will be killed after
// Detach.
// Use Listen, Dial or Connect to complete connection.
func New(process *os.Process) *Process {
	logger := logrus.New().WithFields(logrus.Fields{"layer": "gdbconn"})
	logger.Logger.Level = logrus.DebugLevel
	if !logflags.GdbWire() {
		logger.Logger.Out = ioutil.Discard
	}
	p := &Process{
		conn: gdbConn{
			maxTransmitAttempts: maxTransmitAttempts,
			inbuf:               make([]byte, 0, initialInputBufferSize),
			direction:           proc.Forward,
			log:                 logger,
		},
		threads:        make(map[int]*Thread),
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
func (p *Process) Listen(listener net.Listener, path string, pid int, foreground bool) error {
	acceptChan := make(chan net.Conn)

	go func() {
		conn, _ := listener.Accept()
		acceptChan <- conn
	}()

	select {
	case conn := <-acceptChan:
		listener.Close()
		if conn == nil {
			return errors.New("could not connect")
		}
		return p.Connect(conn, path, pid, foreground)
	case status := <-p.waitChan:
		listener.Close()
		return fmt.Errorf("stub exited while waiting for connection: %v", status)
	}
}

// Dial attempts to connect to the stub.
func (p *Process) Dial(addr string, path string, pid int) error {
	for {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			return p.Connect(conn, path, pid, false)
		}
		select {
		case status := <-p.waitChan:
			return fmt.Errorf("stub exited while attempting to connect: %v", status)
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
func (p *Process) Connect(conn net.Conn, path string, pid int, foreground bool) error {
	p.conn.conn = conn

	p.conn.pid = pid
	err := p.conn.handshake()
	if err != nil {
		conn.Close()
		return err
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

	if path == "" {
		// If we are attaching to a running process and the user didn't specify
		// the executable file manually we must ask the stub for it.
		// We support both qXfer:exec-file:read:: (the gdb way) and calling
		// qProcessInfo (the lldb way).
		// Unfortunately debugserver on macOS supports neither.
		path, err = p.conn.readExecFile()
		if err != nil {
			if isProtocolErrorUnsupported(err) {
				_, path, err = p.loadProcessInfo(pid)
				if err != nil {
					conn.Close()
					return err
				}
			} else {
				conn.Close()
				return fmt.Errorf("could not determine executable path: %v", err)
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

	var wg sync.WaitGroup
	err = p.bi.LoadBinaryInfo(path, &wg)
	wg.Wait()
	if err == nil {
		err = p.bi.LoadError()
	}
	if err != nil {
		conn.Close()
		return err
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

	err = p.updateThreadList(&threadUpdater{p: p})
	if err != nil {
		conn.Close()
		p.bi.Close()
		return err
	}

	if p.conn.pid <= 0 {
		p.conn.pid, _, err = p.loadProcessInfo(0)
		if err != nil && !isProtocolErrorUnsupported(err) {
			conn.Close()
			p.bi.Close()
			return err
		}
	}

	p.selectedGoroutine, _ = proc.GetG(p.CurrentThread())

	proc.CreateUnrecoveredPanicBreakpoint(p, p.writeBreakpoint, &p.breakpoints)

	panicpc, err := proc.FindFunctionLocation(p, "runtime.startpanic", true, 0)
	if err == nil {
		bp, err := p.breakpoints.SetWithID(-1, panicpc, p.writeBreakpoint)
		if err == nil {
			bp.Name = proc.UnrecoveredPanic
			bp.Variables = []string{"runtime.curg._panic.arg"}
		}
	}

	if foreground {
		// Moves process group of the target to foreground. Here we use the PID of
		// the debugserver instance because we already asked Go to put it into a
		// new process group (Setpgid) and debugserver does not spawn the target
		// into a different process group.
		moveToForeground(p.process.Pid)
	}

	return nil
}

// unusedPort returns an unused tcp port
// This is a hack and subject to a race condition with other running
// programs, but most (all?) OS will cycle through all ephemeral ports
// before reassigning one port they just assigned, unless there's heavy
// churn in the ephemeral range this should work.
func unusedPort() string {
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return ":8081"
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	return fmt.Sprintf(":%d", port)
}

const debugserverExecutable = "/Library/Developer/CommandLineTools/Library/PrivateFrameworks/LLDB.framework/Versions/A/Resources/debugserver"

var ErrUnsupportedOS = errors.New("lldb backend not supported on windows")

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
func LLDBLaunch(cmd []string, wd string, foreground bool) (*Process, error) {
	switch runtime.GOOS {
	case "windows":
		return nil, ErrUnsupportedOS
	default:
		// check that the argument to Launch is an executable file
		if fi, staterr := os.Stat(cmd[0]); staterr == nil && (fi.Mode()&0111) == 0 {
			return nil, proc.NotExecutableErr
		}
	}

	if foreground {
		// Disable foregrounding if we can't open /dev/tty or debugserver will
		// crash. See issue #1215.
		tty, err := os.Open("/dev/tty")
		if err != nil {
			foreground = false
		} else {
			tty.Close()
		}
	}

	isDebugserver := false

	var listener net.Listener
	var port string
	var proc *exec.Cmd
	if _, err := os.Stat(debugserverExecutable); err == nil {
		listener, err = net.Listen("tcp", "localhost:0")
		if err != nil {
			return nil, err
		}
		ldEnvVars := getLdEnvVars()
		args := make([]string, 0, len(cmd)+4+len(ldEnvVars))
		args = append(args, ldEnvVars...)
		if foreground {
			args = append(args, "--stdio-path", "/dev/tty")
		}
		args = append(args, "-F", "-R", fmt.Sprintf("127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port), "--")
		args = append(args, cmd...)

		isDebugserver = true

		proc = exec.Command(debugserverExecutable, args...)
	} else {
		if _, err := exec.LookPath("lldb-server"); err != nil {
			return nil, &ErrBackendUnavailable{}
		}
		port = unusedPort()
		args := make([]string, 0, len(cmd)+3)
		args = append(args, "gdbserver")
		args = append(args, port, "--")
		args = append(args, cmd...)

		proc = exec.Command("lldb-server", args...)
	}

	if logflags.LLDBServerOutput() || logflags.GdbWire() || foreground {
		proc.Stdout = os.Stdout
		proc.Stderr = os.Stderr
	}
	if foreground {
		proc.Stdin = os.Stdin
	}
	if wd != "" {
		proc.Dir = wd
	}

	proc.SysProcAttr = backgroundSysProcAttr()

	err := proc.Start()
	if err != nil {
		return nil, err
	}

	p := New(proc.Process)
	p.conn.isDebugserver = isDebugserver

	if listener != nil {
		err = p.Listen(listener, cmd[0], 0, p.conn.isDebugserver && foreground)
	} else {
		err = p.Dial(port, cmd[0], 0)
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// LLDBAttach starts an instance of lldb-server and connects to it, asking
// it to attach to the specified pid.
// Path is path to the target's executable, path only needs to be specified
// for some stubs that do not provide an automated way of determining it
// (for example debugserver).
func LLDBAttach(pid int, path string) (*Process, error) {
	if runtime.GOOS == "windows" {
		return nil, ErrUnsupportedOS
	}

	isDebugserver := false
	var proc *exec.Cmd
	var listener net.Listener
	var port string
	if _, err := os.Stat(debugserverExecutable); err == nil {
		isDebugserver = true
		listener, err = net.Listen("tcp", "localhost:0")
		if err != nil {
			return nil, err
		}
		proc = exec.Command(debugserverExecutable, "-R", fmt.Sprintf("127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port), "--attach="+strconv.Itoa(pid))
	} else {
		if _, err := exec.LookPath("lldb-server"); err != nil {
			return nil, &ErrBackendUnavailable{}
		}
		port = unusedPort()
		proc = exec.Command("lldb-server", "gdbserver", "--attach", strconv.Itoa(pid), port)
	}

	proc.Stdout = os.Stdout
	proc.Stderr = os.Stderr

	proc.SysProcAttr = backgroundSysProcAttr()

	err := proc.Start()
	if err != nil {
		return nil, err
	}

	p := New(proc.Process)
	p.conn.isDebugserver = isDebugserver

	if listener != nil {
		err = p.Listen(listener, path, pid, false)
	} else {
		err = p.Dial(port, path, pid)
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// loadProcessInfo uses qProcessInfo to load the inferior's PID and
// executable path. This command is not supported by all stubs and not all
// stubs will report both the PID and executable path.
func (p *Process) loadProcessInfo(pid int) (int, string, error) {
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

func (p *Process) BinInfo() *proc.BinaryInfo {
	return &p.bi
}

func (p *Process) Recorded() (bool, string) {
	return p.tracedir != "", p.tracedir
}

func (p *Process) Pid() int {
	return int(p.conn.pid)
}

func (p *Process) Valid() (bool, error) {
	if p.detached {
		return false, &proc.ProcessDetachedError{}
	}
	if p.exited {
		return false, &proc.ProcessExitedError{Pid: p.Pid()}
	}
	return true, nil
}

func (p *Process) ResumeNotify(ch chan<- struct{}) {
	p.conn.resumeChan = ch
}

func (p *Process) FindThread(threadID int) (proc.Thread, bool) {
	thread, ok := p.threads[threadID]
	return thread, ok
}

func (p *Process) ThreadList() []proc.Thread {
	r := make([]proc.Thread, 0, len(p.threads))
	for _, thread := range p.threads {
		r = append(r, thread)
	}
	return r
}

func (p *Process) CurrentThread() proc.Thread {
	return p.currentThread
}

func (p *Process) Common() *proc.CommonProcess {
	return &p.common
}

func (p *Process) SelectedGoroutine() *proc.G {
	return p.selectedGoroutine
}

const (
	interruptSignal  = 0x2
	breakpointSignal = 0x5
	childSignal      = 0x11
	stopSignal       = 0x13
)

func (p *Process) ContinueOnce() (proc.Thread, error) {
	if p.exited {
		return nil, &proc.ProcessExitedError{Pid: p.conn.pid}
	}

	if p.conn.direction == proc.Forward {
		// step threads stopped at any breakpoint over their breakpoint
		for _, thread := range p.threads {
			if thread.CurrentBreakpoint.Breakpoint != nil {
				if err := thread.stepInstruction(&threadUpdater{p: p}); err != nil {
					return nil, err
				}
			}
		}
	}

	p.common.ClearAllGCache()
	for _, th := range p.threads {
		th.clearBreakpointState()
	}

	p.setCtrlC(false)

	// resume all threads
	var threadID string
	var sig uint8 = 0
	var tu = threadUpdater{p: p}
	var err error
continueLoop:
	for {
		tu.Reset()
		threadID, sig, err = p.conn.resume(sig, &tu)
		if err != nil {
			if _, exited := err.(proc.ProcessExitedError); exited {
				p.exited = true
			}
			return nil, err
		}

		// 0x5 is always a breakpoint, a manual stop either manifests as 0x13
		// (lldb), 0x11 (debugserver) or 0x2 (gdbserver).
		// Since 0x2 could also be produced by the user
		// pressing ^C (in which case it should be passed to the inferior) we need
		// the ctrlC flag to know that we are the originators.
		switch sig {
		case interruptSignal: // interrupt
			if p.getCtrlC() {
				break continueLoop
			}
		case breakpointSignal: // breakpoint
			break continueLoop
		case childSignal: // stop on debugserver but SIGCHLD on lldb-server/linux
			if p.conn.isDebugserver {
				break continueLoop
			}
		case stopSignal: // stop
			break continueLoop

		// The following are fake BSD-style signals sent by debugserver
		// Unfortunately debugserver can not convert them into signals for the
		// process so we must stop here.
		case 0x91, 0x92, 0x93, 0x94, 0x95, 0x96: /* TARGET_EXC_BAD_ACCESS */
			break continueLoop
		default:
			// any other signal is always propagated to inferior
		}
	}

	if err := p.updateThreadList(&tu); err != nil {
		return nil, err
	}

	if err := p.setCurrentBreakpoints(); err != nil {
		return nil, err
	}

	for _, thread := range p.threads {
		if thread.strID == threadID {
			var err error = nil
			switch sig {
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
			return thread, err
		}
	}

	return nil, fmt.Errorf("could not find thread %s", threadID)
}

func (p *Process) StepInstruction() error {
	thread := p.currentThread
	if p.selectedGoroutine != nil {
		if p.selectedGoroutine.Thread == nil {
			if _, err := p.SetBreakpoint(p.selectedGoroutine.PC, proc.NextBreakpoint, proc.SameGoroutineCondition(p.selectedGoroutine)); err != nil {
				return err
			}
			return proc.Continue(p)
		}
		thread = p.selectedGoroutine.Thread.(*Thread)
	}
	p.common.ClearAllGCache()
	if p.exited {
		return &proc.ProcessExitedError{Pid: p.conn.pid}
	}
	thread.clearBreakpointState()
	err := thread.StepInstruction()
	if err != nil {
		return err
	}
	err = thread.SetCurrentBreakpoint()
	if err != nil {
		return err
	}
	if g, _ := proc.GetG(thread); g != nil {
		p.selectedGoroutine = g
	}
	return nil
}

func (p *Process) SwitchThread(tid int) error {
	if p.exited {
		return proc.ProcessExitedError{Pid: p.conn.pid}
	}
	if th, ok := p.threads[tid]; ok {
		p.currentThread = th
		p.selectedGoroutine, _ = proc.GetG(p.CurrentThread())
		return nil
	}
	return fmt.Errorf("thread %d does not exist", tid)
}

func (p *Process) SwitchGoroutine(gid int) error {
	g, err := proc.FindGoroutine(p, gid)
	if err != nil {
		return err
	}
	if g == nil {
		// user specified -1 and selectedGoroutine is nil
		return nil
	}
	if g.Thread != nil {
		return p.SwitchThread(g.Thread.ThreadID())
	}
	p.selectedGoroutine = g
	return nil
}

func (p *Process) RequestManualStop() error {
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

func (p *Process) CheckAndClearManualStopRequest() bool {
	p.conn.manualStopMutex.Lock()
	msr := p.manualStopRequested
	p.manualStopRequested = false
	p.conn.manualStopMutex.Unlock()
	return msr
}

func (p *Process) setCtrlC(v bool) {
	p.conn.manualStopMutex.Lock()
	p.ctrlC = v
	p.conn.manualStopMutex.Unlock()
}

func (p *Process) getCtrlC() bool {
	p.conn.manualStopMutex.Lock()
	defer p.conn.manualStopMutex.Unlock()
	return p.ctrlC
}

func (p *Process) Detach(kill bool) error {
	if kill && !p.exited {
		err := p.conn.kill()
		if err != nil {
			if _, exited := err.(proc.ProcessExitedError); !exited {
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
	return p.bi.Close()
}

func (p *Process) Restart(pos string) error {
	if p.tracedir == "" {
		return proc.NotRecordedErr
	}

	p.exited = false

	p.common.ClearAllGCache()
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
	_, _, err = p.conn.resume(0, nil)
	if err != nil {
		return err
	}

	err = p.updateThreadList(&threadUpdater{p: p})
	if err != nil {
		return err
	}
	p.selectedGoroutine, _ = proc.GetG(p.CurrentThread())

	for addr := range p.breakpoints.M {
		p.conn.setBreakpoint(addr)
	}

	return p.setCurrentBreakpoints()
}

func (p *Process) When() (string, error) {
	if p.tracedir == "" {
		return "", proc.NotRecordedErr
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

func (p *Process) Checkpoint(where string) (int, error) {
	if p.tracedir == "" {
		return -1, proc.NotRecordedErr
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

func (p *Process) Checkpoints() ([]proc.Checkpoint, error) {
	if p.tracedir == "" {
		return nil, proc.NotRecordedErr
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
		r = append(r, proc.Checkpoint{cpid, fields[1], fields[2]})
	}
	return r, nil
}

const deleteCheckpointPrefix = "Deleted checkpoint "

func (p *Process) ClearCheckpoint(id int) error {
	if p.tracedir == "" {
		return proc.NotRecordedErr
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

func (p *Process) Direction(dir proc.Direction) error {
	if p.tracedir == "" {
		return proc.NotRecordedErr
	}
	if p.conn.conn == nil {
		return proc.ProcessExitedError{Pid: p.conn.pid}
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

func (p *Process) Breakpoints() *proc.BreakpointMap {
	return &p.breakpoints
}

func (p *Process) FindBreakpoint(pc uint64) (*proc.Breakpoint, bool) {
	// Check to see if address is past the breakpoint, (i.e. breakpoint was hit).
	if bp, ok := p.breakpoints.M[pc-uint64(p.bi.Arch.BreakpointSize())]; ok {
		return bp, true
	}
	// Directly use addr to lookup breakpoint.
	if bp, ok := p.breakpoints.M[pc]; ok {
		return bp, true
	}
	return nil, false
}

func (p *Process) writeBreakpoint(addr uint64) (string, int, *proc.Function, []byte, error) {
	f, l, fn := p.bi.PCToLine(uint64(addr))
	if fn == nil {
		return "", 0, nil, nil, proc.InvalidAddressError{Address: addr}
	}

	if err := p.conn.setBreakpoint(addr); err != nil {
		return "", 0, nil, nil, err
	}

	return f, l, fn, nil, nil
}

func (p *Process) SetBreakpoint(addr uint64, kind proc.BreakpointKind, cond ast.Expr) (*proc.Breakpoint, error) {
	if p.exited {
		return nil, &proc.ProcessExitedError{Pid: p.conn.pid}
	}
	return p.breakpoints.Set(addr, kind, cond, p.writeBreakpoint)
}

func (p *Process) ClearBreakpoint(addr uint64) (*proc.Breakpoint, error) {
	if p.exited {
		return nil, &proc.ProcessExitedError{Pid: p.conn.pid}
	}
	return p.breakpoints.Clear(addr, func(bp *proc.Breakpoint) error {
		return p.conn.clearBreakpoint(bp.Addr)
	})
}

func (p *Process) ClearInternalBreakpoints() error {
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
	p    *Process
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
			tu.p.threads[tid] = &Thread{ID: tid, strID: threadID, p: tu.p}
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
		if tu.p.currentThread.ID == threadID {
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

// updateThreadsList retrieves the list of inferior threads from the stub
// and passes it to the threadUpdater.
// Then it reloads the register information for all running threads.
// Some stubs will return the list of running threads in the stop packet, if
// this happens the threadUpdater will know that we have already updated the
// thread list and the first step of updateThreadList will be skipped.
// Registers are always reloaded.
func (p *Process) updateThreadList(tu *threadUpdater) error {
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

	if p.threadStopInfo {
		for _, th := range p.threads {
			sig, reason, err := p.conn.threadStopInfo(th.strID)
			if err != nil {
				if isProtocolErrorUnsupported(err) {
					p.threadStopInfo = false
					break
				}
				return err
			}
			th.setbp = (reason == "breakpoint" || (reason == "" && sig == breakpointSignal))
		}
	}

	for _, thread := range p.threads {
		if err := thread.reloadRegisters(); err != nil {
			return err
		}
	}
	return nil
}

func (p *Process) setCurrentBreakpoints() error {
	if p.threadStopInfo {
		for _, th := range p.threads {
			if th.setbp {
				err := th.SetCurrentBreakpoint()
				if err != nil {
					return err
				}
			}
		}
	}
	if !p.threadStopInfo {
		for _, th := range p.threads {
			if th.CurrentBreakpoint.Breakpoint == nil {
				err := th.SetCurrentBreakpoint()
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (t *Thread) ReadMemory(data []byte, addr uintptr) (n int, err error) {
	err = t.p.conn.readMemory(data, addr)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

func (t *Thread) WriteMemory(addr uintptr, data []byte) (written int, err error) {
	return t.p.conn.writeMemory(addr, data)
}

func (t *Thread) Location() (*proc.Location, error) {
	regs, err := t.Registers(false)
	if err != nil {
		return nil, err
	}
	pc := regs.PC()
	f, l, fn := t.p.bi.PCToLine(pc)
	return &proc.Location{PC: pc, File: f, Line: l, Fn: fn}, nil
}

func (t *Thread) Breakpoint() proc.BreakpointState {
	return t.CurrentBreakpoint
}

func (t *Thread) ThreadID() int {
	return t.ID
}

func (t *Thread) Registers(floatingPoint bool) (proc.Registers, error) {
	return &t.regs, nil
}

func (t *Thread) Arch() proc.Arch {
	return t.p.bi.Arch
}

func (t *Thread) BinInfo() *proc.BinaryInfo {
	return &t.p.bi
}

func (t *Thread) Common() *proc.CommonThread {
	return &t.common
}

func (t *Thread) stepInstruction(tu *threadUpdater) error {
	pc := t.regs.PC()
	if _, atbp := t.p.breakpoints.M[pc]; atbp {
		err := t.p.conn.clearBreakpoint(pc)
		if err != nil {
			return err
		}
		defer t.p.conn.setBreakpoint(pc)
	}
	_, _, err := t.p.conn.step(t.strID, tu)
	return err
}

func (t *Thread) StepInstruction() error {
	if err := t.stepInstruction(&threadUpdater{p: t.p}); err != nil {
		return err
	}
	return t.reloadRegisters()
}

func (t *Thread) Blocked() bool {
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
	return false
}

// loadGInstr returns the correct MOV instruction for the current
// OS/architecture that can be executed to load the address of G from an
// inferior's thread.
func (p *Process) loadGInstr() []byte {
	var op []byte
	switch p.bi.GOOS {
	case "windows":
		// mov rcx, QWORD PTR gs:{uint32(off)}
		op = []byte{0x65, 0x48, 0x8b, 0x0c, 0x25}
	case "linux":
		// mov rcx,QWORD PTR fs:{uint32(off)}
		op = []byte{0x64, 0x48, 0x8B, 0x0C, 0x25}
	case "darwin":
		// mov rcx,QWORD PTR gs:{uint32(off)}
		op = []byte{0x65, 0x48, 0x8B, 0x0C, 0x25}
	default:
		panic("unsupported operating system attempting to find Goroutine on Thread")
	}
	buf := &bytes.Buffer{}
	buf.Write(op)
	binary.Write(buf, binary.LittleEndian, uint32(p.bi.GStructOffset()))
	return buf.Bytes()
}

// reloadRegisters loads the current value of the thread's registers.
// It will also load the address of the thread's G.
// Loading the address of G can be done in one of two ways reloadGAlloc, if
// the stub can allocate memory, or reloadGAtPC, if the stub can't.
func (t *Thread) reloadRegisters() error {
	if t.regs.regs == nil {
		t.regs.regs = make(map[string]gdbRegister)
		t.regs.regsInfo = t.p.conn.regsInfo

		regsz := 0
		for _, reginfo := range t.p.conn.regsInfo {
			if endoff := reginfo.Offset + (reginfo.Bitsize / 8); endoff > regsz {
				regsz = endoff
			}
		}
		t.regs.buf = make([]byte, regsz)
		for _, reginfo := range t.p.conn.regsInfo {
			t.regs.regs[reginfo.Name] = gdbRegister{regnum: reginfo.Regnum, value: t.regs.buf[reginfo.Offset : reginfo.Offset+reginfo.Bitsize/8]}
		}
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

func (t *Thread) writeSomeRegisters(regNames ...string) error {
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

func (t *Thread) readSomeRegisters(regNames ...string) error {
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
func (t *Thread) reloadGAtPC() error {
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

	_, _, err = t.p.conn.step(t.strID, nil)
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
func (t *Thread) reloadGAlloc() error {
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

	_, _, err = t.p.conn.step(t.strID, nil)
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

func (t *Thread) clearBreakpointState() {
	t.setbp = false
	t.CurrentBreakpoint.Clear()
}

func (thread *Thread) SetCurrentBreakpoint() error {
	thread.clearBreakpointState()
	regs, err := thread.Registers(false)
	if err != nil {
		return err
	}
	pc := regs.PC()
	if bp, ok := thread.p.FindBreakpoint(pc); ok {
		if thread.regs.PC() != bp.Addr {
			if err := thread.regs.SetPC(thread, bp.Addr); err != nil {
				return err
			}
		}
		thread.CurrentBreakpoint = bp.CheckCondition(thread)
		if thread.CurrentBreakpoint.Breakpoint != nil && thread.CurrentBreakpoint.Active {
			if g, err := proc.GetG(thread); err == nil {
				thread.CurrentBreakpoint.HitCount[g.ID]++
			}
			thread.CurrentBreakpoint.TotalHitCount++
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

	return 0, proc.UnknownRegisterError
}

func (regs *gdbRegisters) SetPC(thread proc.Thread, pc uint64) error {
	regs.setPC(pc)
	t := thread.(*Thread)
	if t.p.gcmdok {
		return t.p.conn.writeRegisters(t.strID, t.regs.buf)
	}
	reg := regs.regs[regnamePC]
	return t.p.conn.writeRegister(t.strID, reg.regnum, reg.value)
}

func (regs *gdbRegisters) Slice() []proc.Register {
	r := make([]proc.Register, 0, len(regs.regsInfo))
	for _, reginfo := range regs.regsInfo {
		switch {
		case reginfo.Name == "eflags":
			r = proc.AppendEflagReg(r, reginfo.Name, uint64(binary.LittleEndian.Uint32(regs.regs[reginfo.Name].value)))
		case reginfo.Name == "mxcsr":
			r = proc.AppendMxcsrReg(r, reginfo.Name, uint64(binary.LittleEndian.Uint32(regs.regs[reginfo.Name].value)))
		case reginfo.Bitsize == 16:
			r = proc.AppendWordReg(r, reginfo.Name, binary.LittleEndian.Uint16(regs.regs[reginfo.Name].value))
		case reginfo.Bitsize == 32:
			r = proc.AppendDwordReg(r, reginfo.Name, binary.LittleEndian.Uint32(regs.regs[reginfo.Name].value))
		case reginfo.Bitsize == 64:
			r = proc.AppendQwordReg(r, reginfo.Name, binary.LittleEndian.Uint64(regs.regs[reginfo.Name].value))
		case reginfo.Bitsize == 80:
			idx := 0
			for _, stprefix := range []string{"stmm", "st"} {
				if strings.HasPrefix(reginfo.Name, stprefix) {
					idx, _ = strconv.Atoi(reginfo.Name[len(stprefix):])
					break
				}
			}
			value := regs.regs[reginfo.Name].value
			r = proc.AppendX87Reg(r, idx, binary.LittleEndian.Uint16(value[8:]), binary.LittleEndian.Uint64(value[:8]))

		case reginfo.Bitsize == 128:
			r = proc.AppendSSEReg(r, strings.ToUpper(reginfo.Name), regs.regs[reginfo.Name].value)

		case reginfo.Bitsize == 256:
			if !strings.HasPrefix(strings.ToLower(reginfo.Name), "ymm") {
				continue
			}

			value := regs.regs[reginfo.Name].value
			xmmName := "x" + reginfo.Name[1:]
			r = proc.AppendSSEReg(r, strings.ToUpper(xmmName), value[:16])
			r = proc.AppendSSEReg(r, strings.ToUpper(reginfo.Name), value[16:])
		}
	}
	return r
}
