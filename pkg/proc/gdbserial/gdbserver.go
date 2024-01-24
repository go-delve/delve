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
//   https://github.com/llvm/llvm-project/blob/main/lldb/docs/lldb-gdb-remote.txt
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
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	isatty "github.com/mattn/go-isatty"

	"github.com/go-delve/delve/pkg/dwarf/op"
	"github.com/go-delve/delve/pkg/elfwriter"
	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/go-delve/delve/pkg/proc/internal/ebpf"
	"github.com/go-delve/delve/pkg/proc/linutil"
	"github.com/go-delve/delve/pkg/proc/macutil"
)

const (
	gdbWireFullStopPacket = false
	gdbWireMaxLen         = 120

	maxTransmitAttempts    = 3    // number of retransmission attempts on failed checksum
	initialInputBufferSize = 2048 // size of the input buffer for gdbConn

	debugServerEnvVar = "DELVE_DEBUGSERVER_PATH" // use this environment variable to override the path to debugserver used by Launch/Attach
)

const heartbeatInterval = 10 * time.Second

// Relative to $(xcode-select --print-path)/../
// xcode-select typically returns the path to the Developer directory, which is a sibling to SharedFrameworks.
var debugserverXcodeRelativeExecutablePath = "SharedFrameworks/LLDB.framework/Versions/A/Resources/debugserver"

var debugserverExecutablePaths = []string{
	"debugserver",
	"/Library/Developer/CommandLineTools/Library/PrivateFrameworks/LLDB.framework/Versions/A/Resources/debugserver",
	// Function returns the active developer directory provided by xcode-select to compute a debugserver path.
	func() string {
		if _, err := exec.LookPath("xcode-select"); err != nil {
			return ""
		}

		stdout, err := exec.Command("xcode-select", "--print-path").Output()
		if err != nil {
			return ""
		}

		xcodePath := strings.TrimSpace(string(stdout))
		if xcodePath == "" {
			return ""
		}

		// xcode-select prints the path to the active Developer directory, which is typically a sibling to SharedFrameworks.
		return filepath.Join(xcodePath, "..", debugserverXcodeRelativeExecutablePath)
	}(),
}

// ErrDirChange is returned when trying to change execution direction
// while there are still internal breakpoints set.
var ErrDirChange = errors.New("direction change with internal breakpoints")

// ErrStartCallInjectionBackwards is returned when trying to start a call
// injection while the recording is being run backwards.
var ErrStartCallInjectionBackwards = errors.New("can not start a call injection while running backwards")

var checkCanUnmaskSignalsOnce sync.Once
var canUnmaskSignalsCached bool

// gdbProcess implements proc.Process using a connection to a debugger stub
// that understands Gdb Remote Serial Protocol.
type gdbProcess struct {
	bi       *proc.BinaryInfo
	regnames *gdbRegnames
	conn     gdbConn

	threads       map[int]*gdbThread
	currentThread *gdbThread

	exited, detached bool
	almostExited     bool // true if 'rr' has sent its synthetic SIGKILL
	ctrlC            bool // ctrl-c was sent to stop inferior

	breakpoints proc.BreakpointMap

	gcmdok         bool   // true if the stub supports g and (maybe) G commands
	_Gcmdok        bool   // true if the stub supports G command
	threadStopInfo bool   // true if the stub supports qThreadStopInfo
	tracedir       string // if attached to rr the path to the trace directory

	loadGInstrAddr uint64 // address of the g loading instruction, zero if we couldn't allocate it

	breakpointKind int // breakpoint kind to pass to 'z' and 'Z' when creating software breakpoints

	process  *os.Process
	waitChan chan *os.ProcessState

	onDetach func() // called after a successful detach
}

var _ proc.RecordingManipulationInternal = &gdbProcess{}

// gdbThread represents an operating system thread.
type gdbThread struct {
	ID                int
	strID             string
	regs              gdbRegisters
	CurrentBreakpoint proc.BreakpointState
	p                 *gdbProcess
	sig               uint8  // signal received by thread after last stop
	setbp             bool   // thread was stopped because of a breakpoint
	watchAddr         uint64 // if > 0 this is the watchpoint address
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
	arch     *proc.Arch
	regnames *gdbRegnames
}

type gdbRegister struct {
	value  []byte
	regnum int

	ignoreOnWrite bool
}

// gdbRegname records names of important CPU registers
type gdbRegnames struct {
	PC, SP, BP, CX, FsBase string
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
			goarch:              runtime.GOARCH,
			goos:                runtime.GOOS,
		},
		threads:        make(map[int]*gdbThread),
		bi:             proc.NewBinaryInfo(runtime.GOOS, runtime.GOARCH),
		regnames:       new(gdbRegnames),
		breakpoints:    proc.NewBreakpointMap(),
		gcmdok:         true,
		threadStopInfo: true,
		process:        process,
	}

	switch p.bi.Arch.Name {
	default:
		fallthrough
	case "amd64":
		p.breakpointKind = 1
	case "arm64":
		p.breakpointKind = 4
	}

	p.regnames.PC = registerName(p.bi.Arch, p.bi.Arch.PCRegNum)
	p.regnames.SP = registerName(p.bi.Arch, p.bi.Arch.SPRegNum)
	p.regnames.BP = registerName(p.bi.Arch, p.bi.Arch.BPRegNum)

	switch p.bi.Arch.Name {
	case "arm64":
		p.regnames.BP = "fp"
		p.regnames.CX = "x0"
	case "amd64":
		p.regnames.CX = "rcx"
		p.regnames.FsBase = "fs_base"
	default:
		panic("not implemented")
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
func (p *gdbProcess) Listen(listener net.Listener, path, cmdline string, pid int, debugInfoDirs []string, stopReason proc.StopReason) (*proc.TargetGroup, error) {
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
		return p.Connect(conn, path, cmdline, pid, debugInfoDirs, stopReason)
	case status := <-p.waitChan:
		listener.Close()
		return nil, fmt.Errorf("stub exited while waiting for connection: %v", status)
	}
}

// Dial attempts to connect to the stub.
func (p *gdbProcess) Dial(addr string, path, cmdline string, pid int, debugInfoDirs []string, stopReason proc.StopReason) (*proc.TargetGroup, error) {
	for {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			return p.Connect(conn, path, cmdline, pid, debugInfoDirs, stopReason)
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
func (p *gdbProcess) Connect(conn net.Conn, path, cmdline string, pid int, debugInfoDirs []string, stopReason proc.StopReason) (*proc.TargetGroup, error) {
	p.conn.conn = conn
	p.conn.pid = pid
	err := p.conn.handshake(p.regnames)
	if err != nil {
		conn.Close()
		return nil, err
	}

	if p.conn.isDebugserver {
		// There are multiple problems with the 'g'/'G' commands on debugserver.
		// On version 902 it used to crash the server completely (https://bugs.llvm.org/show_bug.cgi?id=36968),
		// on arm64 it results in E74 being returned (https://bugs.llvm.org/show_bug.cgi?id=50169)
		// and on systems where AVX-512 is used it returns the floating point
		// registers scrambled and sometimes causes the mask registers to be
		// zeroed out (https://github.com/go-delve/delve/pull/2498).
		// All of these bugs stem from the fact that the main consumer of
		// debugserver, lldb, never uses 'g' or 'G' which would make Delve the
		// sole tester of those codepaths.
		// Therefore we disable it here. The associated code is kept around to be
		// used with Mozilla RR.
		p.gcmdok = false
	}

	tgt, err := p.initialize(path, cmdline, debugInfoDirs, stopReason)
	if err != nil {
		return nil, err
	}

	if p.bi.Arch.Name != "arm64" {
		// None of the stubs we support returns the value of fs_base or gs_base
		// along with the registers, therefore we have to resort to executing a MOV
		// instruction on the inferior to find out where the G struct of a given
		// thread is located.
		// Here we try to allocate some memory on the inferior which we will use to
		// store the MOV instruction.
		// If the stub doesn't support memory allocation reloadRegisters will
		// overwrite some existing memory to store the MOV.
		if ginstr, err := p.loadGInstr(); err == nil {
			if addr, err := p.conn.allocMemory(256); err == nil {
				if _, err := p.conn.writeMemory(addr, ginstr); err == nil {
					p.loadGInstrAddr = addr
				}
			}
		}
	}

	return tgt, nil
}

func (p *gdbProcess) SupportsBPF() bool {
	return false
}

func (p *gdbProcess) GetBufferedTracepoints() []ebpf.RawUProbeParams {
	return nil
}

func (p *gdbProcess) SetUProbe(fnName string, goidOffset int64, args []ebpf.UProbeArgMap) error {
	panic("not implemented")
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

// getDebugServerAbsolutePath returns a string of the absolute path to the debugserver binary IFF it is
// found in the system path ($PATH), the Xcode bundle or the standalone CLT location.
func getDebugServerAbsolutePath() string {
	if path := os.Getenv(debugServerEnvVar); path != "" {
		return path
	}
	for _, debugServerPath := range debugserverExecutablePaths {
		if debugServerPath == "" {
			continue
		}
		if _, err := exec.LookPath(debugServerPath); err == nil {
			return debugServerPath
		}
	}
	return ""
}

func canUnmaskSignals(debugServerExecutable string) bool {
	checkCanUnmaskSignalsOnce.Do(func() {
		buf, _ := exec.Command(debugServerExecutable, "--unmask-signals").CombinedOutput()
		canUnmaskSignalsCached = !strings.Contains(string(buf), "unrecognized option")
	})
	return canUnmaskSignalsCached
}

// commandLogger is a wrapper around the exec.Command() function to log the arguments prior to
// starting the process
func commandLogger(binary string, arguments ...string) *exec.Cmd {
	logflags.GdbWireLogger().Debugf("executing %s %v", binary, arguments)
	return exec.Command(binary, arguments...)
}

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
func LLDBLaunch(cmd []string, wd string, flags proc.LaunchFlags, debugInfoDirs []string, tty string, redirects [3]string) (*proc.TargetGroup, error) {
	if runtime.GOOS == "windows" {
		return nil, ErrUnsupportedOS
	}
	if err := macutil.CheckRosetta(); err != nil {
		return nil, err
	}

	foreground := flags&proc.LaunchForeground != 0

	var (
		isDebugserver bool
		listener      net.Listener
		port          string
		process       *exec.Cmd
		err           error
		hasRedirects  bool
	)

	if debugserverExecutable := getDebugServerAbsolutePath(); debugserverExecutable != "" {
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		ldEnvVars := getLdEnvVars()
		args := make([]string, 0, len(cmd)+4+len(ldEnvVars))
		args = append(args, ldEnvVars...)

		if tty != "" {
			args = append(args, "--stdio-path", tty)
		} else {
			found := [3]bool{}
			names := [3]string{"stdin", "stdout", "stderr"}
			for i := range redirects {
				if redirects[i] != "" {
					found[i] = true
					hasRedirects = true
					args = append(args, fmt.Sprintf("--%s-path", names[i]), redirects[i])
				}
			}

			if foreground || hasRedirects {
				for i := range found {
					if !found[i] {
						args = append(args, fmt.Sprintf("--%s-path", names[i]), "/dev/"+names[i])
					}
				}
			}
		}

		if logflags.LLDBServerOutput() {
			args = append(args, "-g", "-l", "stdout")
		}
		if flags&proc.LaunchDisableASLR != 0 {
			args = append(args, "-D")
		}
		if canUnmaskSignals(debugserverExecutable) {
			args = append(args, "--unmask-signals")
		}
		args = append(args, "-F", "-R", fmt.Sprintf("127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port), "--")
		args = append(args, cmd...)

		isDebugserver = true

		process = commandLogger(debugserverExecutable, args...)
	} else {
		if _, err = exec.LookPath("lldb-server"); err != nil {
			return nil, &ErrBackendUnavailable{}
		}
		port = unusedPort()
		args := make([]string, 0, len(cmd)+3)
		args = append(args, "gdbserver", port, "--")
		args = append(args, cmd...)

		process = commandLogger("lldb-server", args...)
	}

	if logflags.LLDBServerOutput() || logflags.GdbWire() || foreground || hasRedirects {
		process.Stdout = os.Stdout
		process.Stderr = os.Stderr
	}
	if foreground || hasRedirects {
		if isatty.IsTerminal(os.Stdin.Fd()) {
			foregroundSignalsIgnore()
		}
		process.Stdin = os.Stdin
	}
	if wd != "" {
		process.Dir = wd
	}

	if isatty.IsTerminal(os.Stdin.Fd()) {
		process.SysProcAttr = sysProcAttr(foreground)
	}

	if runtime.GOOS == "darwin" {
		process.Env = proc.DisableAsyncPreemptEnv()
		// Filter out DYLD_INSERT_LIBRARIES on Darwin.
		// This is needed since macOS Ventura, loading custom dylib into debugserver
		// using DYLD_INSERT_LIBRARIES leads to a crash.
		// This is unlike other protected processes, where they just strip it out.
		env := make([]string, 0, len(process.Env))
		for _, v := range process.Env {
			if !strings.HasPrefix(v, "DYLD_INSERT_LIBRARIES") {
				env = append(env, v)
			}
		}
		process.Env = env
	}

	if err = process.Start(); err != nil {
		return nil, err
	}

	p := newProcess(process.Process)
	p.conn.isDebugserver = isDebugserver

	var grp *proc.TargetGroup
	if listener != nil {
		grp, err = p.Listen(listener, cmd[0], strings.Join(cmd, " "), 0, debugInfoDirs, proc.StopLaunched)
	} else {
		grp, err = p.Dial(port, cmd[0], strings.Join(cmd, " "), 0, debugInfoDirs, proc.StopLaunched)
	}
	if p.conn.pid != 0 && foreground && isatty.IsTerminal(os.Stdin.Fd()) {
		// Make the target process the controlling process of the tty if it is a foreground process.
		err := tcsetpgrp(os.Stdin.Fd(), p.conn.pid)
		if err != nil {
			logflags.DebuggerLogger().Errorf("could not set controlling process: %v", err)
		}
	}
	return grp, err
}

// LLDBAttach starts an instance of lldb-server and connects to it, asking
// it to attach to the specified pid.
// Path is path to the target's executable, path only needs to be specified
// for some stubs that do not provide an automated way of determining it
// (for example debugserver).
func LLDBAttach(pid int, path string, waitFor *proc.WaitFor, debugInfoDirs []string) (*proc.TargetGroup, error) {
	if runtime.GOOS == "windows" {
		return nil, ErrUnsupportedOS
	}
	if err := macutil.CheckRosetta(); err != nil {
		return nil, err
	}

	var (
		isDebugserver bool
		process       *exec.Cmd
		listener      net.Listener
		port          string
		err           error
	)
	if debugserverExecutable := getDebugServerAbsolutePath(); debugserverExecutable != "" {
		isDebugserver = true
		listener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		args := []string{"-R", fmt.Sprintf("127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port)}

		if waitFor.Valid() {
			duration := int(waitFor.Duration.Seconds())
			if duration == 0 && waitFor.Duration != 0 {
				// If duration is below the (second) resolution of debugserver pass 1
				// second (0 means infinite).
				duration = 1
			}
			args = append(args, "--waitfor="+waitFor.Name, fmt.Sprintf("--waitfor-interval=%d", waitFor.Interval.Microseconds()), fmt.Sprintf("--waitfor-duration=%d", duration))
		} else {
			args = append(args, "--attach="+strconv.Itoa(pid))
		}

		if canUnmaskSignals(debugserverExecutable) {
			args = append(args, "--unmask-signals")
		}
		process = commandLogger(debugserverExecutable, args...)
	} else {
		if waitFor.Valid() {
			return nil, proc.ErrWaitForNotImplemented
		}
		if _, err = exec.LookPath("lldb-server"); err != nil {
			return nil, &ErrBackendUnavailable{}
		}
		port = unusedPort()
		process = commandLogger("lldb-server", "gdbserver", "--attach", strconv.Itoa(pid), port)
	}

	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	process.SysProcAttr = sysProcAttr(false)

	if err = process.Start(); err != nil {
		return nil, err
	}

	p := newProcess(process.Process)
	p.conn.isDebugserver = isDebugserver

	var grp *proc.TargetGroup
	if listener != nil {
		grp, err = p.Listen(listener, path, "", pid, debugInfoDirs, proc.StopAttached)
	} else {
		grp, err = p.Dial(port, path, "", pid, debugInfoDirs, proc.StopAttached)
	}
	return grp, err
}

// EntryPoint will return the process entry point address, useful for
// debugging PIEs.
func (p *gdbProcess) EntryPoint() (uint64, error) {
	var entryPoint uint64
	if p.bi.GOOS == "darwin" {
		// There is no auxv on darwin, however, we can get the location of the mach-o
		// header from the debugserver by going through the loaded libraries, which includes
		// the exe itself
		images, _ := p.conn.getLoadedDynamicLibraries()
		for _, image := range images {
			if image.MachHeader.FileType == macho.TypeExec {
				// This is a bit hacky. This is technically not the entrypoint,
				// but rather we use the variable to points at the mach-o header,
				// so we can get the offset in bininfo
				entryPoint = image.LoadAddress
				break
			}
		}
	} else if auxv, err := p.conn.readAuxv(); err == nil {
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
func (p *gdbProcess) initialize(path, cmdline string, debugInfoDirs []string, stopReason proc.StopReason) (*proc.TargetGroup, error) {
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
	grp, addTarget := proc.NewGroup(p, proc.NewTargetGroupConfig{
		DebugInfoDirs:       debugInfoDirs,
		DisableAsyncPreempt: runtime.GOOS == "darwin",
		StopReason:          stopReason,
		CanDump:             runtime.GOOS == "darwin",
	})
	_, err = addTarget(p, p.conn.pid, p.currentThread, path, stopReason, cmdline)
	if err != nil {
		p.Detach(p.conn.pid, true)
		return nil, err
	}
	return grp, nil
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
	return p.conn.pid
}

// Valid returns true if we are not detached
// and the process has not exited.
func (p *gdbProcess) Valid() (bool, error) {
	if p.detached {
		return false, proc.ErrProcessDetached
	}
	if p.exited {
		return false, proc.ErrProcessExited{Pid: p.Pid()}
	}
	if p.almostExited && p.conn.direction == proc.Forward {
		return false, proc.ErrProcessExited{Pid: p.Pid()}
	}
	return true, nil
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

// Memory returns the process memory.
func (p *gdbProcess) Memory() proc.MemoryReadWriter {
	return p
}

const (
	interruptSignal  = 0x2
	breakpointSignal = 0x5
	faultSignal      = 0xb
	childSignal      = 0x11
	stopSignal       = 0x13

	_SIGILL  = 0x4
	_SIGFPE  = 0x8
	_SIGKILL = 0x9

	debugServerTargetExcBadAccess      = 0x91
	debugServerTargetExcBadInstruction = 0x92
	debugServerTargetExcArithmetic     = 0x93
	debugServerTargetExcEmulation      = 0x94
	debugServerTargetExcSoftware       = 0x95
	debugServerTargetExcBreakpoint     = 0x96
)

func (p *gdbProcess) ContinueOnce(cctx *proc.ContinueOnceContext) (proc.Thread, proc.StopReason, error) {
	if p.exited {
		return nil, proc.StopExited, proc.ErrProcessExited{Pid: p.conn.pid}
	}
	if p.almostExited {
		if p.conn.direction == proc.Forward {
			return nil, proc.StopExited, proc.ErrProcessExited{Pid: p.conn.pid}
		}
		p.almostExited = false
	}

	if p.conn.direction == proc.Forward {
		// step threads stopped at any breakpoint over their breakpoint
		for _, thread := range p.threads {
			if thread.CurrentBreakpoint.Breakpoint != nil {
				if err := thread.stepInstruction(); err != nil {
					return nil, proc.StopUnknown, err
				}
			}
		}
	}

	for _, th := range p.threads {
		th.clearBreakpointState()
	}

	p.setCtrlC(cctx, false)

	// resume all threads
	var threadID string
	var trapthread *gdbThread
	var tu = threadUpdater{p: p}
	var atstart bool
continueLoop:
	for {
		tu.Reset()
		sp, err := p.conn.resume(cctx, p.threads, &tu)
		threadID = sp.threadID
		if err != nil {
			if _, exited := err.(proc.ErrProcessExited); exited {
				p.exited = true
				return nil, proc.StopExited, err
			}
			return nil, proc.StopUnknown, err
		}

		// For stubs that support qThreadStopInfo updateThreadList will
		// find out the reason why each thread stopped.
		// NOTE: because debugserver will sometimes send two stop packets after a
		// continue it is important that this is the very first thing we do after
		// resume(). See comment in threadStopInfo for an explanation.
		p.updateThreadList(&tu)

		trapthread = p.findThreadByStrID(threadID)
		if trapthread != nil && !p.threadStopInfo {
			// For stubs that do not support qThreadStopInfo we manually set the
			// reason the thread returned by resume() stopped.
			trapthread.sig = sp.sig
			trapthread.watchAddr = sp.watchAddr
		}

		var shouldStop, shouldExitErr bool
		trapthread, atstart, shouldStop, shouldExitErr = p.handleThreadSignals(cctx, trapthread)
		if shouldExitErr {
			p.almostExited = true
			return nil, proc.StopExited, proc.ErrProcessExited{Pid: p.conn.pid}
		}
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

	err := machTargetExcToError(trapthread.sig)
	if err != nil {
		// the signals that are reported here can not be propagated back to the target process.
		trapthread.sig = 0
	}
	p.currentThread = trapthread
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
func (p *gdbProcess) handleThreadSignals(cctx *proc.ContinueOnceContext, trapthread *gdbThread) (trapthreadOut *gdbThread, atstart, shouldStop, shouldExitErr bool) {
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
			if p.getCtrlC(cctx) {
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

		case _SIGKILL:
			if p.tracedir != "" {
				// RR will send a synthetic SIGKILL packet right before the program
				// exits, even if the program exited normally.
				// Treat this signal as if the process had exited because right after
				// this it is still possible to set breakpoints and rewind the process.
				shouldExitErr = true
				isStopSignal = true
			}

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

	if p.getCtrlC(cctx) || cctx.GetManualStopRequested() {
		// If we request an interrupt and a target thread simultaneously receives
		// an unrelated signal debugserver will discard our interrupt request and
		// report the signal but we should stop anyway.
		shouldStop = true
	}

	return trapthread, atstart, shouldStop, shouldExitErr
}

// RequestManualStop will attempt to stop the process
// without a breakpoint or signal having been received.
func (p *gdbProcess) RequestManualStop(cctx *proc.ContinueOnceContext) error {
	if !p.conn.running {
		return nil
	}
	p.ctrlC = true
	return p.conn.sendCtrlC()
}

func (p *gdbProcess) setCtrlC(cctx *proc.ContinueOnceContext, v bool) {
	cctx.StopMu.Lock()
	p.ctrlC = v
	cctx.StopMu.Unlock()
}

func (p *gdbProcess) getCtrlC(cctx *proc.ContinueOnceContext) bool {
	cctx.StopMu.Lock()
	defer cctx.StopMu.Unlock()
	return p.ctrlC
}

// Detach will detach from the target process,
// if 'kill' is true it will also kill the process.
// The _pid argument is unused as follow exec
// mode is not implemented with this backend.
func (p *gdbProcess) Detach(_pid int, kill bool) error {
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
	return nil
}

func (p *gdbProcess) Close() error {
	if p.onDetach != nil {
		p.onDetach()
	}
	return p.bi.Close()
}

// Restart will restart the process from the given position.
func (p *gdbProcess) Restart(cctx *proc.ContinueOnceContext, pos string) (proc.Thread, error) {
	if p.tracedir == "" {
		return nil, proc.ErrNotRecorded
	}

	p.exited = false
	p.almostExited = false

	for _, th := range p.threads {
		th.clearBreakpointState()
	}

	p.ctrlC = false

	err := p.conn.restart(pos)
	if err != nil {
		return nil, err
	}

	// for some reason we have to send a vCont;c after a vRun to make rr behave
	// properly, because that's what gdb does.
	_, err = p.conn.resume(cctx, nil, nil)
	if err != nil {
		return nil, err
	}

	err = p.updateThreadList(&threadUpdater{p: p})
	if err != nil {
		return nil, err
	}
	p.clearThreadSignals()
	p.clearThreadRegisters()

	for _, bp := range p.breakpoints.M {
		p.WriteBreakpoint(bp)
	}

	return p.currentThread, p.setCurrentBreakpoints()
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
	if p.Breakpoints().HasSteppingBreakpoints() {
		return ErrDirChange
	}
	p.conn.direction = dir
	return nil
}

// StartCallInjection notifies the backend that we are about to inject a function call.
func (p *gdbProcess) StartCallInjection() (func(), error) {
	if p.tracedir == "" {
		return func() {}, nil
	}
	if p.conn.conn == nil {
		return nil, proc.ErrProcessExited{Pid: p.conn.pid}
	}
	if p.conn.direction != proc.Forward {
		return nil, ErrStartCallInjectionBackwards
	}

	// Normally it's impossible to inject function calls in a recorded target
	// because the sequence of instructions that the target will execute is
	// predetermined.
	// RR however allows this in a "diversion". When a diversion is started rr
	// takes the current state of the process and runs it forward as a normal
	// process, not following the recording.
	// The gdb serial protocol does not have a way to start a diversion and gdb
	// (the main frontend of rr) does not know how to do it. Instead a
	// diversion is started by reading siginfo, because that's the first
	// request gdb does when starting a function call injection.

	_, err := p.conn.qXfer("siginfo", "", true)
	if err != nil {
		return nil, err
	}

	return func() {
		_ = p.conn.qXferWrite("siginfo", "") // rr always returns an error for qXfer:siginfo:write... even though it works
	}, nil
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

func watchTypeToBreakpointType(wtype proc.WatchType) breakpointType {
	switch {
	case wtype.Read() && wtype.Write():
		return accessWatchpoint
	case wtype.Write():
		return writeWatchpoint
	case wtype.Read():
		return readWatchpoint
	default:
		return swBreakpoint
	}
}

func (p *gdbProcess) WriteBreakpoint(bp *proc.Breakpoint) error {
	kind := p.breakpointKind
	if bp.WatchType != 0 {
		kind = bp.WatchType.Size()
	}
	return p.conn.setBreakpoint(bp.Addr, watchTypeToBreakpointType(bp.WatchType), kind)
}

func (p *gdbProcess) EraseBreakpoint(bp *proc.Breakpoint) error {
	kind := p.breakpointKind
	if bp.WatchType != 0 {
		kind = bp.WatchType.Size()
	}
	return p.conn.clearBreakpoint(bp.Addr, watchTypeToBreakpointType(bp.WatchType), kind)
}

// FollowExec enables (or disables) follow exec mode
func (p *gdbProcess) FollowExec(bool) error {
	return errors.New("follow exec not supported")
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
			sp, err := p.conn.threadStopInfo(th.strID)
			if err != nil {
				if isProtocolErrorUnsupported(err) {
					p.threadStopInfo = false
					break
				}
				return err
			}
			th.setbp = (sp.reason == "breakpoint" || (sp.reason == "" && sp.sig == breakpointSignal) || (sp.watchAddr > 0))
			th.sig = sp.sig
			th.watchAddr = sp.watchAddr
		} else {
			th.sig = 0
			th.watchAddr = 0
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
func (p *gdbProcess) ReadMemory(data []byte, addr uint64) (n int, err error) {
	err = p.conn.readMemory(data, addr)
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

// WriteMemory will write into the memory at 'addr' the data provided.
func (p *gdbProcess) WriteMemory(addr uint64, data []byte) (written int, err error) {
	return p.conn.writeMemory(addr, data)
}

func (p *gdbProcess) StepInstruction(threadID int) error {
	return p.threads[threadID].stepInstruction()
}

func (t *gdbThread) ProcessMemory() proc.MemoryReadWriter {
	return t.p
}

// Location returns the current location of this thread.
func (t *gdbThread) Location() (*proc.Location, error) {
	regs, err := t.Registers()
	if err != nil {
		return nil, err
	}
	if pcreg, ok := regs.(*gdbRegisters).regs[regs.(*gdbRegisters).regnames.PC]; !ok {
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
func (t *gdbThread) Registers() (proc.Registers, error) {
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

// StepInstruction will step exactly 1 CPU instruction.
func (t *gdbThread) stepInstruction() error {
	pc := t.regs.PC()
	if bp, atbp := t.p.breakpoints.M[pc]; atbp && bp.WatchType == 0 {
		err := t.p.conn.clearBreakpoint(pc, swBreakpoint, t.p.breakpointKind)
		if err != nil {
			return err
		}
		defer t.p.conn.setBreakpoint(pc, swBreakpoint, t.p.breakpointKind)
	}
	// Reset thread registers so the next call to
	// Thread.Registers will not be cached.
	t.regs.regs = nil
	return t.p.conn.step(t, &threadUpdater{p: t.p}, false)
}

// SoftExc returns true if this thread received a software exception during the last resume.
func (t *gdbThread) SoftExc() bool {
	return t.setbp
}

// Blocked returns true if the thread is blocked in runtime or kernel code.
func (t *gdbThread) Blocked() bool {
	regs, err := t.Registers()
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
func (p *gdbProcess) loadGInstr() ([]byte, error) {
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
	offset, err := p.bi.GStructOffset(p.Memory())
	if err != nil {
		return nil, err
	}
	buf := &bytes.Buffer{}
	buf.Write(op)
	binary.Write(buf, binary.LittleEndian, uint32(offset))
	return buf.Bytes(), nil
}

func (p *gdbProcess) MemoryMap() ([]proc.MemoryMapEntry, error) {
	r := []proc.MemoryMapEntry{}
	addr := uint64(0)
	for addr != ^uint64(0) {
		mri, err := p.conn.memoryRegionInfo(addr)
		if err != nil {
			return nil, err
		}
		if addr+mri.size <= addr {
			return nil, errors.New("qMemoryRegionInfo response wrapped around the address space or stuck")
		}
		if mri.permissions != "" {
			var mme proc.MemoryMapEntry

			mme.Addr = addr
			mme.Size = mri.size
			mme.Read = strings.Contains(mri.permissions, "r")
			mme.Write = strings.Contains(mri.permissions, "w")
			mme.Exec = strings.Contains(mri.permissions, "x")

			r = append(r, mme)
		}
		addr += mri.size
	}
	return r, nil
}

func (p *gdbProcess) DumpProcessNotes(notes []elfwriter.Note, threadDone func()) (threadsDone bool, out []elfwriter.Note, err error) {
	return false, notes, nil
}

func (regs *gdbRegisters) init(regsInfo []gdbRegisterInfo, arch *proc.Arch, regnames *gdbRegnames) {
	regs.arch = arch
	regs.regnames = regnames
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
		regs.regs[reginfo.Name] = regs.gdbRegisterNew(&reginfo)
	}
}

func (regs *gdbRegisters) gdbRegisterNew(reginfo *gdbRegisterInfo) gdbRegister {
	return gdbRegister{regnum: reginfo.Regnum, value: regs.buf[reginfo.Offset : reginfo.Offset+reginfo.Bitsize/8], ignoreOnWrite: reginfo.ignoreOnWrite}
}

// reloadRegisters loads the current value of the thread's registers.
// It will also load the address of the thread's G.
// Loading the address of G can be done in one of two ways reloadGAlloc, if
// the stub can allocate memory, or reloadGAtPC, if the stub can't.
func (t *gdbThread) reloadRegisters() error {
	if t.regs.regs == nil {
		t.regs.init(t.p.conn.regsInfo, t.p.bi.Arch, t.p.regnames)
	}

	if t.p.gcmdok {
		if err := t.p.conn.readRegisters(t.strID, t.regs.buf); err != nil {
			gdberr, isProt := err.(*GdbProtocolError)
			if isProtocolErrorUnsupported(err) || (t.p.conn.isDebugserver && isProt && gdberr.code == "E74") {
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

	if t.p.bi.GOOS == "linux" {
		if reg, hasFsBase := t.regs.regs[t.p.regnames.FsBase]; hasFsBase {
			t.regs.gaddr = 0
			t.regs.tls = binary.LittleEndian.Uint64(reg.value)
			t.regs.hasgaddr = false
			return nil
		}
	}

	if t.p.bi.Arch.Name == "arm64" {
		// no need to play around with the GInstr on ARM64 because
		// the G addr is stored in a register

		t.regs.gaddr = t.regs.byName("x28")
		t.regs.hasgaddr = true
		t.regs.tls = 0
	} else {
		if t.p.loadGInstrAddr > 0 {
			return t.reloadGAlloc()
		}
		return t.reloadGAtPC()
	}

	return nil
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
	if t.p.gcmdok && t.p._Gcmdok {
		err := t.p.conn.writeRegisters(t.strID, t.regs.buf)
		if isProtocolErrorUnsupported(err) {
			t.p._Gcmdok = false
		} else {
			return err
		}

	}
	for _, r := range t.regs.regs {
		if r.ignoreOnWrite {
			continue
		}
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
	movinstr, err := t.p.loadGInstr()
	if err != nil {
		return err
	}

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
	for addr, bp := range t.p.breakpoints.M {
		if bp.WatchType != 0 {
			continue
		}
		if addr >= pc && addr <= pc+uint64(len(movinstr)) {
			err := t.p.conn.clearBreakpoint(addr, swBreakpoint, t.p.breakpointKind)
			if err != nil {
				return err
			}
			defer t.p.conn.setBreakpoint(addr, swBreakpoint, t.p.breakpointKind)
		}
	}

	savedcode := make([]byte, len(movinstr))
	_, err = t.p.ReadMemory(savedcode, pc)
	if err != nil {
		return err
	}

	_, err = t.p.WriteMemory(pc, movinstr)
	if err != nil {
		return err
	}

	defer func() {
		_, err0 := t.p.WriteMemory(pc, savedcode)
		if err == nil {
			err = err0
		}
		t.regs.setPC(pc)
		t.regs.setCX(cx)
		err1 := t.writeSomeRegisters(t.p.regnames.PC, t.p.regnames.CX)
		if err == nil {
			err = err1
		}
	}()

	err = t.p.conn.step(t, nil, true)
	if err != nil {
		if err == errThreadBlocked {
			t.regs.tls = 0
			t.regs.gaddr = 0
			t.regs.hasgaddr = true
			return nil
		}
		return err
	}

	if err := t.readSomeRegisters(t.p.regnames.PC, t.p.regnames.CX); err != nil {
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
	if err := t.writeSomeRegisters(t.p.regnames.PC); err != nil {
		return err
	}

	var err error

	defer func() {
		t.regs.setPC(pc)
		t.regs.setCX(cx)
		err1 := t.writeSomeRegisters(t.p.regnames.PC, t.p.regnames.CX)
		if err == nil {
			err = err1
		}
	}()

	err = t.p.conn.step(t, nil, true)
	if err != nil {
		if err == errThreadBlocked {
			t.regs.tls = 0
			t.regs.gaddr = 0
			t.regs.hasgaddr = true
			return nil
		}
		return err
	}

	if err := t.readSomeRegisters(t.p.regnames.CX); err != nil {
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
	// adjustPC is ignored, it is the stub's responsibility to set the PC
	// address correctly after hitting a breakpoint.
	t.CurrentBreakpoint.Clear()
	if t.watchAddr > 0 {
		t.CurrentBreakpoint.Breakpoint = t.p.Breakpoints().M[t.watchAddr]
		if t.CurrentBreakpoint.Breakpoint == nil {
			return fmt.Errorf("could not find watchpoint at address %#x", t.watchAddr)
		}
		return nil
	}
	regs, err := t.Registers()
	if err != nil {
		return err
	}
	pc := regs.PC()
	if bp, ok := t.p.FindBreakpoint(pc); ok {
		if t.regs.PC() != bp.Addr {
			if err := t.setPC(bp.Addr); err != nil {
				return err
			}
		}
		t.CurrentBreakpoint.Breakpoint = bp
	}
	return nil
}

func (regs *gdbRegisters) PC() uint64 {
	return binary.LittleEndian.Uint64(regs.regs[regs.regnames.PC].value)
}

func (regs *gdbRegisters) setPC(value uint64) {
	binary.LittleEndian.PutUint64(regs.regs[regs.regnames.PC].value, value)
}

func (regs *gdbRegisters) SP() uint64 {
	return binary.LittleEndian.Uint64(regs.regs[regs.regnames.SP].value)
}

func (regs *gdbRegisters) BP() uint64 {
	return binary.LittleEndian.Uint64(regs.regs[regs.regnames.BP].value)
}

func (regs *gdbRegisters) CX() uint64 {
	return binary.LittleEndian.Uint64(regs.regs[regs.regnames.CX].value)
}

func (regs *gdbRegisters) setCX(value uint64) {
	binary.LittleEndian.PutUint64(regs.regs[regs.regnames.CX].value, value)
}

func (regs *gdbRegisters) TLS() uint64 {
	return regs.tls
}

func (regs *gdbRegisters) GAddr() (uint64, bool) {
	return regs.gaddr, regs.hasgaddr
}

func (regs *gdbRegisters) LR() uint64 {
	return binary.LittleEndian.Uint64(regs.regs["lr"].value)
}

func (regs *gdbRegisters) byName(name string) uint64 {
	reg, ok := regs.regs[name]
	if !ok {
		return 0
	}
	return binary.LittleEndian.Uint64(reg.value)
}

func (regs *gdbRegisters) FloatLoadError() error {
	return nil
}

// SetPC will set the value of the PC register to the given value.
func (t *gdbThread) setPC(pc uint64) error {
	_, _ = t.Registers() // Registers must be loaded first
	t.regs.setPC(pc)
	if t.p.gcmdok {
		return t.p.conn.writeRegisters(t.strID, t.regs.buf)
	}
	reg := t.regs.regs[t.regs.regnames.PC]
	return t.p.conn.writeRegister(t.strID, reg.regnum, reg.value)
}

// SetReg will change the value of a list of registers
func (t *gdbThread) SetReg(regNum uint64, reg *op.DwarfRegister) error {
	regName := registerName(t.p.bi.Arch, regNum)
	_, _ = t.Registers() // Registers must be loaded first
	gdbreg, ok := t.regs.regs[regName]
	if !ok && strings.HasPrefix(regName, "xmm") {
		// XMMn and YMMn are the same amd64 register (in different sizes), if we
		// don't find XMMn try YMMn or ZMMn instead.
		gdbreg, ok = t.regs.regs["y"+regName[1:]]
		if !ok {
			gdbreg, ok = t.regs.regs["z"+regName[1:]]
		}
	}
	if !ok && t.p.bi.Arch.Name == "arm64" && regName == "x30" {
		gdbreg, ok = t.regs.regs["lr"]
	}
	if !ok && regName == "rflags" {
		// rr has eflags instead of rflags
		regName = "eflags"
		gdbreg, ok = t.regs.regs[regName]
		if ok {
			reg.FillBytes()
			reg.Bytes = reg.Bytes[:4]
		}
	}
	if !ok {
		return fmt.Errorf("could not set register %s: not found", regName)
	}
	reg.FillBytes()

	wrongSizeErr := func(n int) error {
		return fmt.Errorf("could not set register %s: wrong size, expected %d got %d", regName, n, len(reg.Bytes))
	}

	if len(reg.Bytes) == len(gdbreg.value) {
		copy(gdbreg.value, reg.Bytes)
		err := t.p.conn.writeRegister(t.strID, gdbreg.regnum, gdbreg.value)
		if err != nil {
			return err
		}
		if t.p.conn.workaroundReg != nil && len(gdbreg.value) > 16 {
			// This is a workaround for a bug in debugserver where register writes (P
			// packet) on AVX-2 and AVX-512 registers are ignored unless they are
			// followed by a write to an AVX register.
			// See:
			//  Issue #2767
			//  https://bugs.llvm.org/show_bug.cgi?id=52362
			reg := t.regs.gdbRegisterNew(t.p.conn.workaroundReg)
			return t.p.conn.writeRegister(t.strID, reg.regnum, reg.value)
		}
	} else if len(reg.Bytes) == 2*len(gdbreg.value) && strings.HasPrefix(regName, "xmm") {
		// rr uses xmmN for the low part of the register and ymmNh for the high part
		gdbregh, ok := t.regs.regs["y"+regName[1:]+"h"]
		if !ok {
			return wrongSizeErr(len(gdbreg.value))
		}
		if len(reg.Bytes) != len(gdbreg.value)+len(gdbregh.value) {
			return wrongSizeErr(len(gdbreg.value) + len(gdbregh.value))
		}
		copy(gdbreg.value, reg.Bytes[:len(gdbreg.value)])
		copy(gdbregh.value, reg.Bytes[len(gdbreg.value):])
		err := t.p.conn.writeRegister(t.strID, gdbreg.regnum, gdbreg.value)
		if err != nil {
			return err
		}
		err = t.p.conn.writeRegister(t.strID, gdbregh.regnum, gdbregh.value)
		if err != nil {
			return err
		}
	} else {
		return wrongSizeErr(len(gdbreg.value))
	}
	return nil
}

func (regs *gdbRegisters) Slice(floatingPoint bool) ([]proc.Register, error) {
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
				name := reginfo.Name
				if last := name[len(name)-1]; last == 'h' || last == 'H' {
					name = name[:len(name)-1]
				}
				r = proc.AppendBytesRegister(r, strings.ToUpper(name), regs.regs[reginfo.Name].value)
			}

		case reginfo.Bitsize == 256:
			if !strings.HasPrefix(strings.ToLower(reginfo.Name), "ymm") || !floatingPoint {
				continue
			}

			value := regs.regs[reginfo.Name].value
			xmmName := "x" + reginfo.Name[1:]
			r = proc.AppendBytesRegister(r, strings.ToUpper(xmmName), value)

		case reginfo.Bitsize == 512:
			if !strings.HasPrefix(strings.ToLower(reginfo.Name), "zmm") || !floatingPoint {
				continue
			}

			value := regs.regs[reginfo.Name].value
			xmmName := "x" + reginfo.Name[1:]
			r = proc.AppendBytesRegister(r, strings.ToUpper(xmmName), value)
		}
	}
	return r, nil
}

func (regs *gdbRegisters) Copy() (proc.Registers, error) {
	savedRegs := &gdbRegisters{}
	savedRegs.init(regs.regsInfo, regs.arch, regs.regnames)
	copy(savedRegs.buf, regs.buf)
	return savedRegs, nil
}

func registerName(arch *proc.Arch, regNum uint64) string {
	regName, _, _ := arch.DwarfRegisterToString(int(regNum), nil)
	return strings.ToLower(regName)
}

func machTargetExcToError(sig uint8) error {
	switch sig {
	case 0x91:
		return errors.New("bad access")
	case 0x92:
		return errors.New("bad instruction")
	case 0x93:
		return errors.New("arithmetic exception")
	case 0x94:
		return errors.New("emulation exception")
	case 0x95:
		return errors.New("software exception")
	case 0x96:
		return errors.New("breakpoint exception")
	}
	return nil
}
