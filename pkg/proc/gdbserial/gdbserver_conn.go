package gdbserial

import (
	"bufio"
	"bytes"
	"debug/macho"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/proc"
	"github.com/sirupsen/logrus"
)

type gdbConn struct {
	conn net.Conn
	rdr  *bufio.Reader

	inbuf  []byte
	outbuf bytes.Buffer

	running bool

	direction proc.Direction // direction of execution

	packetSize int               // maximum packet size supported by stub
	regsInfo   []gdbRegisterInfo // list of registers

	workaroundReg *gdbRegisterInfo // used to work-around a register setting bug in debugserver, see use in gdbserver.go

	pid int // cache process id

	ack                   bool // when ack is true acknowledgment packets are enabled
	multiprocess          bool // multiprocess extensions are active
	maxTransmitAttempts   int  // maximum number of transmit or receive attempts when bad checksums are read
	threadSuffixSupported bool // thread suffix supported by stub
	isDebugserver         bool // true if the stub is debugserver
	xcmdok                bool // x command can be used to transfer memory
	goarch                string
	goos                  string

	useXcmd bool // forces writeMemory to use the 'X' command

	log *logrus.Entry
}

var ErrTooManyAttempts = errors.New("too many transmit attempts")

// GdbProtocolError is an error response (Exx) of Gdb Remote Serial Protocol
// or an "unsupported command" response (empty packet).
type GdbProtocolError struct {
	context string
	cmd     string
	code    string
}

func (err *GdbProtocolError) Error() string {
	cmd := err.cmd
	if len(cmd) > 20 {
		cmd = cmd[:20] + "..."
	}
	if err.code == "" {
		return fmt.Sprintf("unsupported packet %s during %s", cmd, err.context)
	}
	return fmt.Sprintf("protocol error %s during %s for packet %s", err.code, err.context, cmd)
}

func isProtocolErrorUnsupported(err error) bool {
	gdberr, ok := err.(*GdbProtocolError)
	if !ok {
		return false
	}
	return gdberr.code == ""
}

// GdbMalformedThreadIDError is returned when the stub responds with a
// thread ID that does not conform with the Gdb Remote Serial Protocol
// specification.
type GdbMalformedThreadIDError struct {
	tid string
}

func (err *GdbMalformedThreadIDError) Error() string {
	return fmt.Sprintf("malformed thread ID %q", err.tid)
}

const (
	qSupportedSimple       = "$qSupported:swbreak+;hwbreak+;no-resumed+;xmlRegisters=i386"
	qSupportedMultiprocess = "$qSupported:multiprocess+;swbreak+;hwbreak+;no-resumed+;xmlRegisters=i386"
)

func (conn *gdbConn) handshake(regnames *gdbRegnames) error {
	conn.ack = true
	conn.packetSize = 256
	conn.rdr = bufio.NewReader(conn.conn)

	// This first ack packet is needed to start up the connection
	conn.sendack('+')

	conn.disableAck()

	// Try to enable thread suffixes for the command 'g' and 'p'
	if _, err := conn.exec([]byte("$QThreadSuffixSupported"), "init"); err != nil {
		if isProtocolErrorUnsupported(err) {
			conn.threadSuffixSupported = false
		} else {
			return err
		}
	} else {
		conn.threadSuffixSupported = true
	}

	if !conn.threadSuffixSupported {
		features, err := conn.qSupported(true)
		if err != nil {
			return err
		}
		conn.multiprocess = features["multiprocess"]

		// for some reason gdbserver won't let us read target.xml unless first we
		// select a thread.
		if conn.multiprocess {
			conn.exec([]byte("$Hgp0.0"), "init")
		} else {
			conn.exec([]byte("$Hgp0"), "init")
		}
	} else {
		// execute qSupported with the multiprocess feature disabled (the
		// interaction of thread suffixes and multiprocess is not documented), we
		// only need this call to configure conn.packetSize.
		if _, err := conn.qSupported(false); err != nil {
			return err
		}
	}

	// Attempt to figure out the name of the processor register.
	// We either need qXfer:features:read (gdbserver/rr) or qRegisterInfo (lldb)
	regFound := map[string]bool{
		regnames.PC: false,
		regnames.SP: false,
		regnames.BP: false,
		regnames.CX: false,
	}
	if err := conn.readRegisterInfo(regFound); err != nil {
		if isProtocolErrorUnsupported(err) {
			if err := conn.readTargetXml(regFound); err != nil {
				return err
			}
		} else {
			return err
		}
	}
	for n := range regFound {
		if n == "" {
			continue
		}
		if !regFound[n] {
			return fmt.Errorf("could not find %s register", n)
		}
	}

	// We either need:
	//  * QListThreadsInStopReply + qThreadStopInfo (i.e. lldb-server/debugserver),
	//  * or a stub that runs the inferior in single threaded mode (i.e. rr).
	// Otherwise we'll have problems handling breakpoints in multithreaded programs.
	if _, err := conn.exec([]byte("$QListThreadsInStopReply"), "init"); err != nil {
		gdberr, ok := err.(*GdbProtocolError)
		if !ok {
			return err
		}
		if gdberr.code != "" {
			return err
		}
	}

	if resp, err := conn.exec([]byte("$x0,0"), "init"); err == nil && string(resp) == "OK" {
		conn.xcmdok = true
	}

	return nil
}

// qSupported interprets qSupported responses.
func (conn *gdbConn) qSupported(multiprocess bool) (features map[string]bool, err error) {
	q := qSupportedSimple
	if multiprocess {
		q = qSupportedMultiprocess
	}
	respBuf, err := conn.exec([]byte(q), "init/qSupported")
	if err != nil {
		return nil, err
	}
	resp := strings.Split(string(respBuf), ";")
	features = make(map[string]bool)
	for _, stubfeature := range resp {
		if len(stubfeature) <= 0 {
			continue
		} else if equal := strings.Index(stubfeature, "="); equal >= 0 {
			if stubfeature[:equal] == "PacketSize" {
				if n, err := strconv.ParseInt(stubfeature[equal+1:], 16, 64); err == nil {
					conn.packetSize = int(n)
				}
			}
		} else if stubfeature[len(stubfeature)-1] == '+' {
			features[stubfeature[:len(stubfeature)-1]] = true
		}
	}
	return features, nil
}

// disableAck disables protocol acks.
func (conn *gdbConn) disableAck() error {
	_, err := conn.exec([]byte("$QStartNoAckMode"), "init/disableAck")
	if err == nil {
		conn.ack = false
	}
	return err
}

// gdbTarget is a struct type used to parse target.xml
type gdbTarget struct {
	Includes  []gdbTargetInclude `xml:"xi include"`
	Registers []gdbRegisterInfo  `xml:"reg"`
}

type gdbTargetInclude struct {
	Href string `xml:"href,attr"`
}

type gdbRegisterInfo struct {
	Name    string `xml:"name,attr"`
	Bitsize int    `xml:"bitsize,attr"`
	Offset  int
	Regnum  int    `xml:"regnum,attr"`
	Group   string `xml:"group,attr"`

	ignoreOnWrite bool
}

func setRegFound(regFound map[string]bool, name string) {
	for n := range regFound {
		if name == n {
			regFound[n] = true
		}
	}
}

// readTargetXml reads target.xml file from stub using qXfer:features:read,
// then parses it requesting any additional files.
// The schema of target.xml is described by:
//
//	https://github.com/bminor/binutils-gdb/blob/61baf725eca99af2569262d10aca03dcde2698f6/gdb/features/gdb-target.dtd
func (conn *gdbConn) readTargetXml(regFound map[string]bool) (err error) {
	conn.regsInfo, err = conn.readAnnex("target.xml")
	if err != nil {
		return err
	}
	var offset int
	regnum := 0
	for i := range conn.regsInfo {
		if conn.regsInfo[i].Regnum == 0 {
			conn.regsInfo[i].Regnum = regnum
		} else {
			regnum = conn.regsInfo[i].Regnum
		}
		conn.regsInfo[i].Offset = offset
		offset += conn.regsInfo[i].Bitsize / 8

		setRegFound(regFound, conn.regsInfo[i].Name)
		regnum++
	}

	return nil
}

// readRegisterInfo uses qRegisterInfo to read register information (used
// when qXfer:feature:read is not supported).
func (conn *gdbConn) readRegisterInfo(regFound map[string]bool) (err error) {
	regnum := 0
	for {
		conn.outbuf.Reset()
		fmt.Fprintf(&conn.outbuf, "$qRegisterInfo%x", regnum)
		respbytes, err := conn.exec(conn.outbuf.Bytes(), "register info")
		if err != nil {
			if regnum == 0 {
				return err
			}
			break
		}

		var regname string
		var offset int
		var bitsize int
		var contained bool
		var ignoreOnWrite bool

		resp := string(respbytes)
		for {
			semicolon := strings.Index(resp, ";")
			keyval := resp
			if semicolon >= 0 {
				keyval = resp[:semicolon]
			}

			colon := strings.Index(keyval, ":")
			if colon >= 0 {
				name := keyval[:colon]
				value := keyval[colon+1:]

				switch name {
				case "name":
					regname = value
				case "offset":
					offset, _ = strconv.Atoi(value)
				case "bitsize":
					bitsize, _ = strconv.Atoi(value)
				case "container-regs":
					contained = true
				case "set":
					if value == "Exception State Registers" || value == "AMX Registers" {
						// debugserver doesn't like it if we try to write these
						ignoreOnWrite = true
					}
				}
			}

			if semicolon < 0 {
				break
			}
			resp = resp[semicolon+1:]
		}

		if contained {
			if regname == "xmm0" {
				conn.workaroundReg = &gdbRegisterInfo{Regnum: regnum, Name: regname, Bitsize: bitsize, Offset: offset, ignoreOnWrite: ignoreOnWrite}
			}
			regnum++
			continue
		}

		setRegFound(regFound, regname)

		conn.regsInfo = append(conn.regsInfo, gdbRegisterInfo{Regnum: regnum, Name: regname, Bitsize: bitsize, Offset: offset, ignoreOnWrite: ignoreOnWrite})

		regnum++
	}

	return nil
}

func (conn *gdbConn) readAnnex(annex string) ([]gdbRegisterInfo, error) {
	tgtbuf, err := conn.qXfer("features", annex, false)
	if err != nil {
		return nil, err
	}
	var tgt gdbTarget
	if err := xml.Unmarshal(tgtbuf, &tgt); err != nil {
		return nil, err
	}

	for _, incl := range tgt.Includes {
		regs, err := conn.readAnnex(incl.Href)
		if err != nil {
			return nil, err
		}
		tgt.Registers = append(tgt.Registers, regs...)
	}
	return tgt.Registers, nil
}

func (conn *gdbConn) readExecFile() (string, error) {
	outbuf, err := conn.qXfer("exec-file", "", true)
	if err != nil {
		return "", err
	}
	return string(outbuf), nil
}

func (conn *gdbConn) readAuxv() ([]byte, error) {
	return conn.qXfer("auxv", "", true)
}

// qXfer executes a 'qXfer' read with the specified kind (i.e. feature,
// exec-file, etc...) and annex.
func (conn *gdbConn) qXfer(kind, annex string, binary bool) ([]byte, error) {
	out := []byte{}
	for {
		cmd := []byte(fmt.Sprintf("$qXfer:%s:read:%s:%x,fff", kind, annex, len(out)))
		err := conn.send(cmd)
		if err != nil {
			return nil, err
		}
		buf, err := conn.recv(cmd, "target features transfer", binary)
		if err != nil {
			return nil, err
		}

		out = append(out, buf[1:]...)
		if buf[0] == 'l' {
			break
		}
	}
	return out, nil
}

// qXferWrite executes a 'qXfer' write with the specified kind and annex.
func (conn *gdbConn) qXferWrite(kind, annex string) error {
	conn.outbuf.Reset()
	fmt.Fprintf(&conn.outbuf, "$qXfer:%s:write:%s:0:", kind, annex)
	//TODO(aarzilli): if we ever actually need to write something with qXfer,
	//this will need to be implemented properly. At the moment it is only used
	//for a fake write to the siginfo kind, to end a diversion in 'rr'.
	_, err := conn.exec(conn.outbuf.Bytes(), "qXfer")
	return err
}

type breakpointType uint8

const (
	swBreakpoint     breakpointType = 0
	hwBreakpoint     breakpointType = 1
	writeWatchpoint  breakpointType = 2
	readWatchpoint   breakpointType = 3
	accessWatchpoint breakpointType = 4
)

// setBreakpoint executes a 'Z' (insert breakpoint) command of type '0' and kind '1' or '4'
func (conn *gdbConn) setBreakpoint(addr uint64, typ breakpointType, kind int) error {
	conn.outbuf.Reset()
	fmt.Fprintf(&conn.outbuf, "$Z%d,%x,%d", typ, addr, kind)
	_, err := conn.exec(conn.outbuf.Bytes(), "set breakpoint")
	return err
}

// clearBreakpoint executes a 'z' (remove breakpoint) command of type '0' and kind '1' or '4'
func (conn *gdbConn) clearBreakpoint(addr uint64, typ breakpointType, kind int) error {
	conn.outbuf.Reset()
	fmt.Fprintf(&conn.outbuf, "$z%d,%x,%d", typ, addr, kind)
	_, err := conn.exec(conn.outbuf.Bytes(), "clear breakpoint")
	return err
}

// kill executes a 'k' (kill) command.
func (conn *gdbConn) kill() error {
	resp, err := conn.exec([]byte{'$', 'k'}, "kill")
	if err == io.EOF {
		// The stub is allowed to shut the connection on us immediately after a
		// kill. This is not an error.
		conn.conn.Close()
		conn.conn = nil
		return proc.ErrProcessExited{Pid: conn.pid}
	}
	if err != nil {
		return err
	}
	_, _, err = conn.parseStopPacket(resp, "", nil)
	return err
}

// detach executes a 'D' (detach) command.
func (conn *gdbConn) detach() error {
	if conn.conn == nil {
		// Already detached
		return nil
	}
	_, err := conn.exec([]byte{'$', 'D'}, "detach")
	conn.conn.Close()
	conn.conn = nil
	return err
}

// readRegisters executes a 'g' (read registers) command.
func (conn *gdbConn) readRegisters(threadID string, data []byte) error {
	if !conn.threadSuffixSupported {
		if err := conn.selectThread('g', threadID, "registers read"); err != nil {
			return err
		}
	}
	conn.outbuf.Reset()
	conn.outbuf.WriteString("$g")
	conn.appendThreadSelector(threadID)
	resp, err := conn.exec(conn.outbuf.Bytes(), "registers read")
	if err != nil {
		return err
	}

	for i := 0; i < len(resp); i += 2 {
		n, _ := strconv.ParseUint(string(resp[i:i+2]), 16, 8)
		data[i/2] = uint8(n)
	}

	return nil
}

// writeRegisters executes a 'G' (write registers) command.
func (conn *gdbConn) writeRegisters(threadID string, data []byte) error {
	if !conn.threadSuffixSupported {
		if err := conn.selectThread('g', threadID, "registers write"); err != nil {
			return err
		}
	}
	conn.outbuf.Reset()
	conn.outbuf.WriteString("$G")

	for _, b := range data {
		fmt.Fprintf(&conn.outbuf, "%02x", b)
	}
	conn.appendThreadSelector(threadID)
	_, err := conn.exec(conn.outbuf.Bytes(), "registers write")
	return err
}

// readRegister executes 'p' (read register) command.
func (conn *gdbConn) readRegister(threadID string, regnum int, data []byte) error {
	if !conn.threadSuffixSupported {
		if err := conn.selectThread('g', threadID, "registers write"); err != nil {
			return err
		}
	}
	conn.outbuf.Reset()
	fmt.Fprintf(&conn.outbuf, "$p%x", regnum)
	conn.appendThreadSelector(threadID)
	resp, err := conn.exec(conn.outbuf.Bytes(), "register read")
	if err != nil {
		return err
	}

	for i := 0; i < len(resp); i += 2 {
		n, _ := strconv.ParseUint(string(resp[i:i+2]), 16, 8)
		data[i/2] = uint8(n)
	}

	return nil
}

// writeRegister executes 'P' (write register) command.
func (conn *gdbConn) writeRegister(threadID string, regnum int, data []byte) error {
	if !conn.threadSuffixSupported {
		if err := conn.selectThread('g', threadID, "registers write"); err != nil {
			return err
		}
	}
	conn.outbuf.Reset()
	fmt.Fprintf(&conn.outbuf, "$P%x=", regnum)
	for _, b := range data {
		fmt.Fprintf(&conn.outbuf, "%02x", b)
	}
	conn.appendThreadSelector(threadID)
	_, err := conn.exec(conn.outbuf.Bytes(), "register write")
	return err
}

// resume execution of the target process.
// If the current direction is proc.Backward this is done with the 'bc' command.
// If the current direction is proc.Forward this is done with the vCont command.
// The threads argument will be used to determine which signal to use to
// resume each thread. If a thread has sig == 0 the 'c' action will be used,
// otherwise the 'C' action will be used and the value of sig will be passed
// to it.
func (conn *gdbConn) resume(cctx *proc.ContinueOnceContext, threads map[int]*gdbThread, tu *threadUpdater) (stopPacket, error) {
	if conn.direction == proc.Forward {
		conn.outbuf.Reset()
		fmt.Fprintf(&conn.outbuf, "$vCont")
		for _, th := range threads {
			if th.sig != 0 {
				fmt.Fprintf(&conn.outbuf, ";C%02x:%s", th.sig, th.strID)
			}
		}
		fmt.Fprintf(&conn.outbuf, ";c")
	} else {
		if err := conn.selectThread('c', "p-1.-1", "resume"); err != nil {
			return stopPacket{}, err
		}
		conn.outbuf.Reset()
		fmt.Fprint(&conn.outbuf, "$bc")
	}
	cctx.StopMu.Lock()
	if err := conn.send(conn.outbuf.Bytes()); err != nil {
		cctx.StopMu.Unlock()
		return stopPacket{}, err
	}
	conn.running = true
	cctx.StopMu.Unlock()
	defer func() {
		cctx.StopMu.Lock()
		conn.running = false
		cctx.StopMu.Unlock()
	}()
	if cctx.ResumeChan != nil {
		close(cctx.ResumeChan)
		cctx.ResumeChan = nil
	}
	return conn.waitForvContStop("resume", "-1", tu)
}

// step executes a 'vCont' command on the specified thread with 's' action.
func (conn *gdbConn) step(th *gdbThread, tu *threadUpdater, ignoreFaultSignal bool) error {
	threadID := th.strID
	if conn.direction != proc.Forward {
		if err := conn.selectThread('c', threadID, "step"); err != nil {
			return err
		}
		conn.outbuf.Reset()
		fmt.Fprint(&conn.outbuf, "$bs")
		if err := conn.send(conn.outbuf.Bytes()); err != nil {
			return err
		}
		_, err := conn.waitForvContStop("singlestep", threadID, tu)
		return err
	}

	var _SIGBUS uint8
	switch conn.goos {
	case "linux":
		_SIGBUS = 0x7
	case "darwin":
		_SIGBUS = 0xa
	default:
		panic(fmt.Errorf("unknown GOOS %s", conn.goos))
	}

	var sig uint8 = 0
	for {
		conn.outbuf.Reset()
		if sig == 0 {
			fmt.Fprintf(&conn.outbuf, "$vCont;s:%s", threadID)
		} else {
			fmt.Fprintf(&conn.outbuf, "$vCont;S%02x:%s", sig, threadID)
		}
		if err := conn.send(conn.outbuf.Bytes()); err != nil {
			return err
		}
		if tu != nil {
			tu.Reset()
		}
		sp, err := conn.waitForvContStop("singlestep", threadID, tu)
		sig = sp.sig
		if err != nil {
			return err
		}
		switch sig {
		case faultSignal:
			if ignoreFaultSignal { // we attempting to read the TLS, a fault here should be ignored
				return nil
			}
		case _SIGILL, _SIGBUS, _SIGFPE:
			// propagate these signals to inferior immediately
		case interruptSignal, breakpointSignal, stopSignal:
			return nil
		case childSignal: // stop on debugserver but SIGCHLD on lldb-server/linux
			if conn.isDebugserver {
				return nil
			}
		case debugServerTargetExcBadAccess, debugServerTargetExcBadInstruction, debugServerTargetExcArithmetic, debugServerTargetExcEmulation, debugServerTargetExcSoftware, debugServerTargetExcBreakpoint:
			if ignoreFaultSignal {
				return nil
			}
			return machTargetExcToError(sig)
		default:
			// delay propagation of any other signal to until after the stepping is done
			th.sig = sig
			sig = 0
		}
	}
}

var errThreadBlocked = errors.New("thread blocked")

func (conn *gdbConn) waitForvContStop(context, threadID string, tu *threadUpdater) (stopPacket, error) {
	count := 0
	failed := false
	for {
		conn.conn.SetReadDeadline(time.Now().Add(heartbeatInterval))
		resp, err := conn.recv(nil, context, false)
		conn.conn.SetReadDeadline(time.Time{})
		if neterr, isneterr := err.(net.Error); isneterr && neterr.Timeout() {
			// Debugserver sometimes forgets to inform us that inferior stopped,
			// sending this status request after a timeout helps us get unstuck.
			// Debugserver will not respond to this request unless inferior is
			// already stopped.
			if conn.isDebugserver {
				conn.send([]byte("$?"))
			}
			if count > 1 && context == "singlestep" {
				failed = true
				conn.sendCtrlC()
			}
			count++
		} else if failed {
			return stopPacket{}, errThreadBlocked
		} else if err != nil {
			return stopPacket{}, err
		} else {
			repeat, sp, err := conn.parseStopPacket(resp, threadID, tu)
			if !repeat {
				return sp, err
			}
		}
	}
}

type stopPacket struct {
	threadID  string
	sig       uint8
	reason    string
	watchAddr uint64
}

// Mach exception codes used to decode metype/medata keys in stop packets (necessary to support watchpoints with debugserver).
// See:
//
//	https://opensource.apple.com/source/xnu/xnu-4570.1.46/osfmk/mach/exception_types.h.auto.html
//	https://opensource.apple.com/source/xnu/xnu-4570.1.46/osfmk/mach/i386/exception.h.auto.html
//	https://opensource.apple.com/source/xnu/xnu-4570.1.46/osfmk/mach/arm/exception.h.auto.html
const (
	_EXC_BREAKPOINT   = 6     // mach exception type for hardware breakpoints
	_EXC_I386_SGL     = 1     // mach exception code for single step on x86, for some reason this is also used for watchpoints
	_EXC_ARM_DA_DEBUG = 0x102 // mach exception code for debug fault on arm/arm64
)

// executes 'vCont' (continue/step) command
func (conn *gdbConn) parseStopPacket(resp []byte, threadID string, tu *threadUpdater) (repeat bool, sp stopPacket, err error) {
	switch resp[0] {
	case 'T':
		if len(resp) < 3 {
			return false, stopPacket{}, fmt.Errorf("malformed response for vCont %s", string(resp))
		}

		sig, err := strconv.ParseUint(string(resp[1:3]), 16, 8)
		if err != nil {
			return false, stopPacket{}, fmt.Errorf("malformed stop packet: %s", string(resp))
		}
		sp.sig = uint8(sig)

		if logflags.GdbWire() && gdbWireFullStopPacket {
			conn.log.Debugf("full stop packet: %s", string(resp))
		}

		var metype int
		var medata = make([]uint64, 0, 10)

		buf := resp[3:]
		for buf != nil {
			colon := bytes.Index(buf, []byte{':'})
			if colon < 0 {
				break
			}
			key := buf[:colon]
			buf = buf[colon+1:]

			semicolon := bytes.Index(buf, []byte{';'})
			var value []byte
			if semicolon < 0 {
				value = buf
				buf = nil
			} else {
				value = buf[:semicolon]
				buf = buf[semicolon+1:]
			}

			switch string(key) {
			case "thread":
				sp.threadID = string(value)
			case "threads":
				if tu != nil {
					tu.Add(strings.Split(string(value), ","))
					tu.Finish()
				}
			case "reason":
				sp.reason = string(value)
			case "watch", "awatch", "rwatch":
				sp.watchAddr, err = strconv.ParseUint(string(value), 16, 64)
				if err != nil {
					return false, stopPacket{}, fmt.Errorf("malformed stop packet: %s (wrong watch address)", string(resp))
				}
			case "metype":
				// mach exception type (debugserver extension)
				metype, _ = strconv.Atoi(string(value))
			case "medata":
				// mach exception data (debugserver extension)
				d, _ := strconv.ParseUint(string(value), 16, 64)
				medata = append(medata, d)
			}
		}

		// Debugserver does not report watchpoint stops in the standard way preferring
		// instead the semi-undocumented metype/medata keys.
		// These values also have different meanings depending on the CPU architecture.
		switch conn.goarch {
		case "amd64":
			if metype == _EXC_BREAKPOINT && len(medata) >= 2 && medata[0] == _EXC_I386_SGL {
				sp.watchAddr = medata[1] // this should be zero if this is really a single step stop and non-zero for watchpoints
			}
		case "arm64":
			if metype == _EXC_BREAKPOINT && len(medata) >= 2 && medata[0] == _EXC_ARM_DA_DEBUG {
				sp.watchAddr = medata[1]
			}
		}

		return false, sp, nil

	case 'W', 'X':
		// process exited, next two character are exit code

		semicolon := bytes.Index(resp, []byte{';'})

		if semicolon < 0 {
			semicolon = len(resp)
		}
		status, _ := strconv.ParseUint(string(resp[1:semicolon]), 16, 8)
		return false, stopPacket{}, proc.ErrProcessExited{Pid: conn.pid, Status: int(status)}

	case 'N':
		// we were singlestepping the thread and the thread exited
		sp.threadID = threadID
		return false, sp, nil

	case 'O':
		data := make([]byte, 0, len(resp[1:])/2)
		for i := 1; i < len(resp); i += 2 {
			n, _ := strconv.ParseUint(string(resp[i:i+2]), 16, 8)
			data = append(data, uint8(n))
		}
		os.Stdout.Write(data)
		return true, sp, nil

	default:
		return false, sp, fmt.Errorf("unexpected response for vCont %c", resp[0])
	}
}

const ctrlC = 0x03 // the ASCII character for ^C

// executes a ctrl-C on the line
func (conn *gdbConn) sendCtrlC() error {
	conn.log.Debug("<- interrupt")
	_, err := conn.conn.Write([]byte{ctrlC})
	return err
}

// queryProcessInfo executes a qProcessInfoPID (if pid != 0) or a qProcessInfo (if pid == 0)
func (conn *gdbConn) queryProcessInfo(pid int) (map[string]string, error) {
	conn.outbuf.Reset()
	if pid != 0 {
		fmt.Fprintf(&conn.outbuf, "$qProcessInfoPID:%d", pid)
	} else {
		fmt.Fprint(&conn.outbuf, "$qProcessInfo")
	}
	resp, err := conn.exec(conn.outbuf.Bytes(), "process info for pid")
	if err != nil {
		return nil, err
	}

	pi := make(map[string]string)

	for len(resp) > 0 {
		semicolon := bytes.Index(resp, []byte{';'})
		keyval := resp
		if semicolon >= 0 {
			keyval = resp[:semicolon]
			resp = resp[semicolon+1:]
		}

		colon := bytes.Index(keyval, []byte{':'})
		if colon < 0 {
			continue
		}

		key := string(keyval[:colon])
		value := string(keyval[colon+1:])

		switch key {
		case "name":
			name := make([]byte, len(value)/2)
			for i := 0; i < len(value); i += 2 {
				n, _ := strconv.ParseUint(string(value[i:i+2]), 16, 8)
				name[i/2] = byte(n)
			}
			pi[key] = string(name)

		default:
			pi[key] = value
		}
	}
	return pi, nil
}

// executes qfThreadInfo/qsThreadInfo commands
func (conn *gdbConn) queryThreads(first bool) (threads []string, err error) {
	// https://sourceware.org/gdb/onlinedocs/gdb/General-Query-Packets.html
	conn.outbuf.Reset()
	if first {
		conn.outbuf.WriteString("$qfThreadInfo")
	} else {
		conn.outbuf.WriteString("$qsThreadInfo")
	}

	resp, err := conn.exec(conn.outbuf.Bytes(), "thread info")
	if err != nil {
		return nil, err
	}

	switch resp[0] {
	case 'l':
		return nil, nil
	case 'm':
		// parse list...
	default:
		return nil, errors.New("malformed qfThreadInfo response")
	}

	var pid int
	resp = resp[1:]
	for {
		tidbuf := resp
		comma := bytes.Index(tidbuf, []byte{','})
		if comma >= 0 {
			tidbuf = tidbuf[:comma]
		}
		if conn.multiprocess && pid == 0 {
			dot := bytes.Index(tidbuf, []byte{'.'})
			if dot >= 0 {
				pid, _ = strconv.Atoi(string(tidbuf[1:dot]))
			}
		}
		threads = append(threads, string(tidbuf))
		if comma < 0 {
			break
		}
		resp = resp[comma+1:]
	}

	if conn.multiprocess && pid > 0 {
		conn.pid = pid
	}
	return threads, nil
}

func (conn *gdbConn) selectThread(kind byte, threadID string, context string) error {
	if conn.threadSuffixSupported {
		panic("selectThread when thread suffix is supported")
	}
	conn.outbuf.Reset()
	fmt.Fprintf(&conn.outbuf, "$H%c%s", kind, threadID)
	_, err := conn.exec(conn.outbuf.Bytes(), context)
	return err
}

func (conn *gdbConn) appendThreadSelector(threadID string) {
	if !conn.threadSuffixSupported {
		return
	}
	fmt.Fprintf(&conn.outbuf, ";thread:%s;", threadID)
}

func (conn *gdbConn) readMemory(data []byte, addr uint64) error {
	if conn.xcmdok && len(data) > conn.packetSize {
		return conn.readMemoryBinary(data, addr)
	}
	return conn.readMemoryHex(data, addr)
}

// executes 'm' (read memory) command
func (conn *gdbConn) readMemoryHex(data []byte, addr uint64) error {
	size := len(data)
	data = data[:0]

	for size > 0 {
		conn.outbuf.Reset()

		// gdbserver will crash if we ask too many bytes... not return an error, actually crash
		sz := size
		if dataSize := (conn.packetSize - 4) / 2; sz > dataSize {
			sz = dataSize
		}
		size = size - sz

		fmt.Fprintf(&conn.outbuf, "$m%x,%x", addr+uint64(len(data)), sz)
		resp, err := conn.exec(conn.outbuf.Bytes(), "memory read")
		if err != nil {
			return err
		}

		for i := 0; i < len(resp); i += 2 {
			n, _ := strconv.ParseUint(string(resp[i:i+2]), 16, 8)
			data = append(data, uint8(n))
		}
	}
	return nil
}

// executes 'x' (binary read memory) command
func (conn *gdbConn) readMemoryBinary(data []byte, addr uint64) error {
	size := len(data)
	data = data[:0]

	for len(data) < size {
		conn.outbuf.Reset()

		sz := size - len(data)

		fmt.Fprintf(&conn.outbuf, "$x%x,%x", addr+uint64(len(data)), sz)
		if err := conn.send(conn.outbuf.Bytes()); err != nil {
			return err
		}
		resp, err := conn.recv(conn.outbuf.Bytes(), "binary memory read", true)
		if err != nil {
			return err
		}
		data = append(data, resp...)
	}
	return nil
}

func writeAsciiBytes(w io.Writer, data []byte) {
	for _, b := range data {
		fmt.Fprintf(w, "%02x", b)
	}
}

// writeMemory writes memory using either 'M' or 'X'
func (conn *gdbConn) writeMemory(addr uint64, data []byte) (written int, err error) {
	if conn.useXcmd {
		return conn.writeMemoryBinary(addr, data)
	}
	return conn.writeMemoryHex(addr, data)
}

// executes 'M' (write memory) command
func (conn *gdbConn) writeMemoryHex(addr uint64, data []byte) (written int, err error) {
	if len(data) == 0 {
		// LLDB can't parse requests for 0-length writes and hangs if we emit them
		return 0, nil
	}
	conn.outbuf.Reset()
	//TODO(aarzilli): do not send packets larger than conn.PacketSize
	fmt.Fprintf(&conn.outbuf, "$M%x,%x:", addr, len(data))

	writeAsciiBytes(&conn.outbuf, data)

	_, err = conn.exec(conn.outbuf.Bytes(), "memory write")
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

func (conn *gdbConn) writeMemoryBinary(addr uint64, data []byte) (written int, err error) {
	conn.outbuf.Reset()
	fmt.Fprintf(&conn.outbuf, "$X%x,%x:", addr, len(data))

	for _, b := range data {
		switch b {
		case '#', '$', '}':
			conn.outbuf.WriteByte('}')
			conn.outbuf.WriteByte(b ^ escapeXor)
		default:
			conn.outbuf.WriteByte(b)
		}
	}

	_, err = conn.exec(conn.outbuf.Bytes(), "memory write")
	if err != nil {
		return 0, err
	}
	return len(data), nil
}

func (conn *gdbConn) allocMemory(sz uint64) (uint64, error) {
	conn.outbuf.Reset()
	fmt.Fprintf(&conn.outbuf, "$_M%x,rwx", sz)
	resp, err := conn.exec(conn.outbuf.Bytes(), "memory allocation")
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(string(resp), 16, 64)
}

// threadStopInfo executes a 'qThreadStopInfo' and returns the reason the
// thread stopped.
func (conn *gdbConn) threadStopInfo(threadID string) (sp stopPacket, err error) {
	conn.outbuf.Reset()
	fmt.Fprintf(&conn.outbuf, "$qThreadStopInfo%s", threadID)
	resp, err := conn.exec(conn.outbuf.Bytes(), "thread stop info")
	if err != nil {
		return stopPacket{}, err
	}
	_, sp, err = conn.parseStopPacket(resp, "", nil)
	if err != nil {
		return stopPacket{}, err
	}
	if sp.threadID != threadID {
		// When we send a ^C (manual stop request) and the process is close to
		// stopping anyway, sometimes, debugserver will send back two stop
		// packets. We need to ignore this spurious stop packet. Because the first
		// thing we do after the stop is updateThreadList, which calls this
		// function, this is relatively painless. We simply need to check that the
		// stop packet we receive is for the thread we requested, if it isn't we
		// can assume it is the spurious extra stop packet and simply ignore it.
		// An example of a problematic interaction is in the commit message for
		// this change.
		// See https://github.com/go-delve/delve/issues/3013.

		conn.conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
		resp, err = conn.recv(conn.outbuf.Bytes(), "thread stop info", false)
		conn.conn.SetReadDeadline(time.Time{})
		if err != nil {
			if neterr, isneterr := err.(net.Error); isneterr && neterr.Timeout() {
				return stopPacket{}, fmt.Errorf("qThreadStopInfo mismatch, requested %s got %s", sp.threadID, threadID)
			}
			return stopPacket{}, err
		}
		_, sp, err = conn.parseStopPacket(resp, "", nil)
		if err != nil {
			return stopPacket{}, err
		}
		if sp.threadID != threadID {
			return stopPacket{}, fmt.Errorf("qThreadStopInfo mismatch, requested %s got %s", sp.threadID, threadID)
		}
	}

	return sp, nil
}

// restart executes a 'vRun' command.
func (conn *gdbConn) restart(pos string) error {
	conn.outbuf.Reset()
	fmt.Fprint(&conn.outbuf, "$vRun;")
	if pos != "" {
		fmt.Fprint(&conn.outbuf, ";")
		writeAsciiBytes(&conn.outbuf, []byte(pos))
	}
	_, err := conn.exec(conn.outbuf.Bytes(), "restart")
	return err
}

// qRRCmd executes a qRRCmd command
func (conn *gdbConn) qRRCmd(args ...string) (string, error) {
	if len(args) == 0 {
		panic("must specify at least one argument for qRRCmd")
	}
	conn.outbuf.Reset()
	fmt.Fprint(&conn.outbuf, "$qRRCmd")
	for _, arg := range args {
		fmt.Fprint(&conn.outbuf, ":")
		writeAsciiBytes(&conn.outbuf, []byte(arg))
	}
	resp, err := conn.exec(conn.outbuf.Bytes(), "qRRCmd")
	if err != nil {
		return "", err
	}
	data := make([]byte, 0, len(resp)/2)
	for i := 0; i < len(resp); i += 2 {
		n, _ := strconv.ParseUint(string(resp[i:i+2]), 16, 8)
		data = append(data, uint8(n))
	}
	return string(data), nil
}

type imageList struct {
	Images []imageDescription `json:"images"`
}

type imageDescription struct {
	LoadAddress uint64     `json:"load_address"`
	Pathname    string     `json:"pathname"`
	MachHeader  machHeader `json:"mach_header"`
}

type machHeader struct {
	FileType macho.Type `json:"filetype"`
}

// getLoadedDynamicLibraries executes jGetLoadedDynamicLibrariesInfos which
// returns the list of loaded dynamic libraries
func (conn *gdbConn) getLoadedDynamicLibraries() ([]imageDescription, error) {
	cmd := []byte("$jGetLoadedDynamicLibrariesInfos:{\"fetch_all_solibs\":true}")
	if err := conn.send(cmd); err != nil {
		return nil, err
	}
	resp, err := conn.recv(cmd, "get dynamic libraries", true)
	if err != nil {
		return nil, err
	}
	var images imageList
	err = json.Unmarshal(resp, &images)
	return images.Images, err
}

type memoryRegionInfo struct {
	start       uint64
	size        uint64
	permissions string
	name        string
}

func decodeHexString(in []byte) (string, bool) {
	out := make([]byte, 0, len(in)/2)
	for i := 0; i < len(in); i += 2 {
		v, err := strconv.ParseUint(string(in[i:i+2]), 16, 8)
		if err != nil {
			return "", false
		}
		out = append(out, byte(v))
	}
	return string(out), true
}

func (conn *gdbConn) memoryRegionInfo(addr uint64) (*memoryRegionInfo, error) {
	conn.outbuf.Reset()
	fmt.Fprintf(&conn.outbuf, "$qMemoryRegionInfo:%x", addr)
	resp, err := conn.exec(conn.outbuf.Bytes(), "qMemoryRegionInfo")
	if err != nil {
		return nil, err
	}

	mri := &memoryRegionInfo{}

	buf := resp
	for len(buf) > 0 {
		colon := bytes.Index(buf, []byte{':'})
		if colon < 0 {
			break
		}
		key := buf[:colon]
		buf = buf[colon+1:]

		semicolon := bytes.Index(buf, []byte{';'})
		var value []byte
		if semicolon < 0 {
			value = buf
			buf = nil
		} else {
			value = buf[:semicolon]
			buf = buf[semicolon+1:]
		}

		switch string(key) {
		case "start":
			start, err := strconv.ParseUint(string(value), 16, 64)
			if err != nil {
				return nil, fmt.Errorf("malformed qMemoryRegionInfo response packet (start): %v in %s", err, string(resp))
			}
			mri.start = start
		case "size":
			size, err := strconv.ParseUint(string(value), 16, 64)
			if err != nil {
				return nil, fmt.Errorf("malformed qMemoryRegionInfo response packet (size): %v in %s", err, string(resp))
			}
			mri.size = size
		case "permissions":
			mri.permissions = string(value)
		case "name":
			namestr, ok := decodeHexString(value)
			if !ok {
				return nil, fmt.Errorf("malformed qMemoryRegionInfo response packet (name): %s", string(resp))
			}
			mri.name = namestr
		case "error":
			errstr, ok := decodeHexString(value)
			if !ok {
				return nil, fmt.Errorf("malformed qMemoryRegionInfo response packet (error): %s", string(resp))
			}
			return nil, fmt.Errorf("qMemoryRegionInfo error: %s", errstr)
		}
	}

	return mri, nil
}

// exec executes a message to the stub and reads a response.
// The details of the wire protocol are described here:
//
//	https://sourceware.org/gdb/onlinedocs/gdb/Overview.html#Overview
func (conn *gdbConn) exec(cmd []byte, context string) ([]byte, error) {
	if err := conn.send(cmd); err != nil {
		return nil, err
	}
	return conn.recv(cmd, context, false)
}

var hexdigit = []byte{'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', 'a', 'b', 'c', 'd', 'e', 'f'}

func (conn *gdbConn) send(cmd []byte) error {
	if len(cmd) == 0 || cmd[0] != '$' {
		panic("gdb protocol error: command doesn't start with '$'")
	}

	// append checksum to packet
	cmd = append(cmd, '#')
	sum := checksum(cmd)
	cmd = append(cmd, hexdigit[sum>>4], hexdigit[sum&0xf])

	attempt := 0
	for {
		if logflags.GdbWire() {
			if len(cmd) > gdbWireMaxLen {
				conn.log.Debugf("<- %s...", string(cmd[:gdbWireMaxLen]))
			} else {
				conn.log.Debugf("<- %s", string(cmd))
			}
		}
		_, err := conn.conn.Write(cmd)
		if err != nil {
			return err
		}

		if !conn.ack {
			break
		}

		if conn.readack() {
			break
		}
		if attempt > conn.maxTransmitAttempts {
			return ErrTooManyAttempts
		}
		attempt++
	}
	return nil
}

func (conn *gdbConn) recv(cmd []byte, context string, binary bool) (resp []byte, err error) {
	attempt := 0
	for {
		var err error
		resp, err = conn.rdr.ReadBytes('#')
		if err != nil {
			return nil, err
		}

		// read checksum
		_, err = io.ReadFull(conn.rdr, conn.inbuf[:2])
		if err != nil {
			return nil, err
		}
		if logflags.GdbWire() {
			out := resp
			partial := false
			if idx := bytes.Index(out, []byte{'\n'}); idx >= 0 && !binary {
				out = resp[:idx]
				partial = true
			}
			if len(out) > gdbWireMaxLen {
				out = out[:gdbWireMaxLen]
				partial = true
			}
			if !partial {
				if binary {
					conn.log.Debugf("-> %q%s", string(resp), string(conn.inbuf[:2]))
				} else {
					conn.log.Debugf("-> %s%s", string(resp), string(conn.inbuf[:2]))
				}
			} else {
				if binary {
					conn.log.Debugf("-> %q...", string(out))
				} else {
					conn.log.Debugf("-> %s...", string(out))
				}
			}
		}

		if !conn.ack {
			break
		}

		if resp[0] == '%' {
			// If the first character is a % (instead of $) the stub sent us a
			// notification packet, this is weird since we specifically claimed that
			// we don't support notifications of any kind, but it should be safe to
			// ignore regardless.
			continue
		}

		if checksumok(resp, conn.inbuf[:2]) {
			conn.sendack('+')
			break
		}
		if attempt > conn.maxTransmitAttempts {
			conn.sendack('+')
			return nil, ErrTooManyAttempts
		}
		attempt++
		conn.sendack('-')
	}

	if binary {
		conn.inbuf, resp = binarywiredecode(resp, conn.inbuf)
	} else {
		conn.inbuf, resp = wiredecode(resp, conn.inbuf)
	}

	if len(resp) == 0 || (resp[0] == 'E' && !binary) || (resp[0] == 'E' && len(resp) == 3) {
		cmdstr := ""
		if cmd != nil {
			cmdstr = string(cmd)
		}
		return nil, &GdbProtocolError{context, cmdstr, string(resp)}
	}

	return resp, nil
}

// Readack reads one byte from stub, returns true if the byte is '+'
func (conn *gdbConn) readack() bool {
	b, err := conn.rdr.ReadByte()
	if err != nil {
		return false
	}
	conn.log.Debugf("-> %s", string(b))
	return b == '+'
}

// Sendack executes an ack character, c must be either '+' or '-'
func (conn *gdbConn) sendack(c byte) {
	if c != '+' && c != '-' {
		panic(fmt.Errorf("sendack(%c)", c))
	}
	conn.conn.Write([]byte{c})
	conn.log.Debugf("<- %s", string(c))
}

// escapeXor is the value mandated by the specification to escape characters
const escapeXor byte = 0x20

// wiredecode decodes the contents of in into buf.
// If buf is nil it will be allocated ex-novo, if the size of buf is not
// enough to hold the decoded contents it will be grown.
// Returns the newly allocated buffer as newbuf and the message contents as
// msg.
func wiredecode(in, buf []byte) (newbuf, msg []byte) {
	if buf != nil {
		buf = buf[:0]
	} else {
		buf = make([]byte, 0, 256)
	}

	start := 1

	for i := 0; i < len(in); i++ {
		switch ch := in[i]; ch {
		case '{': // escape
			if i+1 >= len(in) {
				buf = append(buf, ch)
			} else {
				buf = append(buf, in[i+1]^escapeXor)
				i++
			}
		case ':':
			buf = append(buf, ch)
			if i == 3 {
				// we just read the sequence identifier
				start = i + 1
			}
		case '#': // end of packet
			return buf, buf[start:]
		case '*': // runlength encoding marker
			if i+1 >= len(in) || i == 0 {
				buf = append(buf, ch)
			} else {
				n := in[i+1] - 29
				r := buf[len(buf)-1]
				for j := uint8(0); j < n; j++ {
					buf = append(buf, r)
				}
				i++
			}
		default:
			buf = append(buf, ch)
		}
	}
	return buf, buf[start:]
}

// binarywiredecode is like wiredecode but decodes the wire encoding for
// binary packets, such as the 'x' and 'X' packets as well as all the json
// packets used by lldb/debugserver.
func binarywiredecode(in, buf []byte) (newbuf, msg []byte) {
	if buf != nil {
		buf = buf[:0]
	} else {
		buf = make([]byte, 0, 256)
	}

	start := 1

	for i := 0; i < len(in); i++ {
		switch ch := in[i]; ch {
		case '}': // escape
			if i+1 >= len(in) {
				buf = append(buf, ch)
			} else {
				buf = append(buf, in[i+1]^escapeXor)
				i++
			}
		case '#': // end of packet
			return buf, buf[start:]
		default:
			buf = append(buf, ch)
		}
	}
	return buf, buf[start:]
}

// checksumok checks that checksum is a valid checksum for packet.
func checksumok(packet, checksumBuf []byte) bool {
	if packet[0] != '$' {
		return false
	}

	sum := checksum(packet)
	tgt, err := strconv.ParseUint(string(checksumBuf), 16, 8)
	if err != nil {
		return false
	}

	tgt8 := uint8(tgt)

	return sum == tgt8
}

func checksum(packet []byte) (sum uint8) {
	for i := 1; i < len(packet); i++ {
		if packet[i] == '#' {
			return sum
		}
		sum += packet[i]
	}
	return sum
}
