package rpccommon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"reflect"
	"runtime"
	"sync"

	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/version"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/dap"
	"github.com/go-delve/delve/service/debugger"
	"github.com/go-delve/delve/service/internal/sameuser"
	"github.com/go-delve/delve/service/rpc2"
)

//go:generate go run ../../_scripts/gen-suitablemethods.go suitablemethods

// ServerImpl implements a JSON-RPC server that can switch between two
// versions of the API.
type ServerImpl struct {
	// config is all the information necessary to start the debugger and server.
	config *service.Config
	// listener is used to serve JSON-RPC.
	listener net.Listener
	// stopChan is used to stop the listener goroutine.
	stopChan chan struct{}
	// debugger is the debugger service.
	debugger *debugger.Debugger
	// s2 is APIv2 server.
	s2 *rpc2.RPCServer
	// maps of served methods, one for each supported API.
	methodMaps []map[string]*methodType
	log        logflags.Logger
}

type RPCCallback struct {
	s              *ServerImpl
	sending        *sync.Mutex
	codec          rpc.ServerCodec
	req            rpc.Request
	setupDone      chan struct{}
	disconnectChan chan struct{}
}

var _ service.RPCCallback = &RPCCallback{}

// RPCServer implements the RPC method calls common to all versions of the API.
type RPCServer struct {
	s *ServerImpl
}

type methodType struct {
	method      reflect.Value
	ArgType     reflect.Type
	ReplyType   reflect.Type
	Synchronous bool
}

// NewServer creates a new RPCServer.
func NewServer(config *service.Config) *ServerImpl {
	logger := logflags.RPCLogger()
	if config.APIVersion < 2 {
		logger.Info("Using API v1")
	}
	if config.Debugger.Foreground {
		// Print listener address
		logflags.WriteAPIListeningMessage(config.Listener.Addr())
		logger.Debug("API server pid = ", os.Getpid())
	}
	return &ServerImpl{
		config:   config,
		listener: config.Listener,
		stopChan: make(chan struct{}),
		log:      logger,
	}
}

// Stop stops the JSON-RPC server.
func (s *ServerImpl) Stop() error {
	s.log.Debug("stopping")
	close(s.stopChan)
	if s.config.AcceptMulti {
		s.listener.Close()
	}
	if s.debugger.IsRunning() {
		s.debugger.Command(&api.DebuggerCommand{Name: api.Halt}, nil, nil, nil)
	}
	kill := s.config.Debugger.AttachPid == 0
	return s.debugger.Detach(kill)
}

// Run starts a debugger and exposes it with an JSON-RPC server. The debugger
// itself can be stopped with the `detach` API.
func (s *ServerImpl) Run() error {
	var err error

	if s.config.APIVersion == 0 {
		s.config.APIVersion = 2
	}

	if s.config.APIVersion != 2 {
		return errors.New("unknown API version")
	}

	// Create and start the debugger
	config := s.config.Debugger
	if s.debugger, err = debugger.New(&config, s.config.ProcessArgs); err != nil {
		return err
	}

	s.s2 = rpc2.NewServer(s.config, s.debugger)

	rpcServer := &RPCServer{s}

	s.methodMaps = make([]map[string]*methodType, 2)

	s.methodMaps[1] = map[string]*methodType{}

	suitableMethods2(s.s2, s.methodMaps[1])
	suitableMethodsCommon(rpcServer, s.methodMaps[1])
	finishMethodsMapInit(s.methodMaps[1])

	go func() {
		defer s.listener.Close()
		for {
			c, err := s.listener.Accept()
			if err != nil {
				select {
				case <-s.stopChan:
					// We were supposed to exit, do nothing and return
					return
				default:
					panic(err)
				}
			}

			if s.config.CheckLocalConnUser {
				if !sameuser.CanAccept(s.listener.Addr(), c.LocalAddr(), c.RemoteAddr()) {
					c.Close()
					continue
				}
			}

			go s.serveConnectionDemux(c)
			if !s.config.AcceptMulti {
				break
			}
		}
	}()
	return nil
}

type bufReadWriteCloser struct {
	*bufio.Reader
	io.WriteCloser
}

func (s *ServerImpl) serveConnectionDemux(c io.ReadWriteCloser) {
	conn := &bufReadWriteCloser{bufio.NewReader(c), c}
	b, err := conn.Peek(1)
	if err != nil {
		s.log.Warnf("error determining new connection protocol: %v", err)
		return
	}
	if b[0] == 'C' { // C is for DAP's Content-Length
		s.log.Debugf("serving DAP on new connection")
		ds := dap.NewSession(conn, &dap.Config{Config: s.config, StopTriggered: s.stopChan}, s.debugger)
		go ds.ServeDAPCodec()
	} else {
		s.log.Debugf("serving JSON-RPC on new connection")
		go s.serveJSONCodec(conn)
	}
}

func finishMethodsMapInit(methods map[string]*methodType) {
	for name, method := range methods {
		mtype := method.method.Type()
		if mtype.NumIn() != 2 {
			panic(fmt.Errorf("wrong number of inputs for method %s (%d)", name, mtype.NumIn()))
		}
		method.ArgType = mtype.In(0)
		method.ReplyType = mtype.In(1)
		method.Synchronous = method.ReplyType.String() != "service.RPCCallback"
	}
}

func (s *ServerImpl) serveJSONCodec(conn io.ReadWriteCloser) {
	clientDisconnectChan := make(chan struct{})
	defer func() {
		close(clientDisconnectChan)
		if !s.config.AcceptMulti && s.config.DisconnectChan != nil {
			close(s.config.DisconnectChan)
		}
	}()

	sending := new(sync.Mutex)
	codec := jsonrpc.NewServerCodec(conn)
	var req rpc.Request
	var resp rpc.Response
	for {
		req = rpc.Request{}
		err := codec.ReadRequestHeader(&req)
		if err != nil {
			if err != io.EOF {
				s.log.Error("rpc:", err)
			}
			break
		}

		mtype, ok := s.methodMaps[s.config.APIVersion-1][req.ServiceMethod]
		if !ok {
			s.log.Errorf("rpc: can't find method %s", req.ServiceMethod)
			s.sendResponse(sending, &req, &rpc.Response{}, nil, codec, fmt.Sprintf("unknown method: %s", req.ServiceMethod))
			continue
		}

		var argv, replyv reflect.Value

		// Decode the argument value.
		argIsValue := false // if true, need to indirect before calling.
		if mtype.ArgType.Kind() == reflect.Ptr {
			argv = reflect.New(mtype.ArgType.Elem())
		} else {
			argv = reflect.New(mtype.ArgType)
			argIsValue = true
		}
		// argv guaranteed to be a pointer now.
		if err = codec.ReadRequestBody(argv.Interface()); err != nil {
			return
		}
		if argIsValue {
			argv = argv.Elem()
		}

		if mtype.Synchronous {
			if logflags.RPC() {
				argvbytes, _ := json.Marshal(argv.Interface())
				s.log.Debugf("<- %s(%T%s)", req.ServiceMethod, argv.Interface(), argvbytes)
			}
			replyv = reflect.New(mtype.ReplyType.Elem())
			function := mtype.method
			var returnValues []reflect.Value
			var errInter interface{}
			func() {
				defer func() {
					if ierr := recover(); ierr != nil {
						errInter = newInternalError(ierr, 2)
					}
				}()
				returnValues = function.Call([]reflect.Value{argv, replyv})
				errInter = returnValues[0].Interface()
			}()

			errmsg := ""
			if errInter != nil {
				errmsg = errInter.(error).Error()
			}
			resp = rpc.Response{}
			if logflags.RPC() {
				replyvbytes, _ := json.Marshal(replyv.Interface())
				s.log.Debugf("-> %T%s error: %q", replyv.Interface(), replyvbytes, errmsg)
			}
			s.sendResponse(sending, &req, &resp, replyv.Interface(), codec, errmsg)
			if req.ServiceMethod == "RPCServer.Detach" && s.config.DisconnectChan != nil {
				close(s.config.DisconnectChan)
				s.config.DisconnectChan = nil
			}
		} else {
			if logflags.RPC() {
				argvbytes, _ := json.Marshal(argv.Interface())
				s.log.Debugf("(async %d) <- %s(%T%s)", req.Seq, req.ServiceMethod, argv.Interface(), argvbytes)
			}
			function := mtype.method
			ctl := &RPCCallback{s, sending, codec, req, make(chan struct{}), clientDisconnectChan}
			go func() {
				defer func() {
					if ierr := recover(); ierr != nil {
						ctl.Return(nil, newInternalError(ierr, 2))
					}
				}()
				function.Call([]reflect.Value{argv, reflect.ValueOf(ctl)})
			}()
			<-ctl.setupDone
		}
	}
	codec.Close()
}

// A value sent as a placeholder for the server's response value when the server
// receives an invalid request. It is never decoded by the client since the Response
// contains an error when it is used.
var invalidRequest = struct{}{}

func (s *ServerImpl) sendResponse(sending *sync.Mutex, req *rpc.Request, resp *rpc.Response, reply interface{}, codec rpc.ServerCodec, errmsg string) {
	resp.ServiceMethod = req.ServiceMethod
	if errmsg != "" {
		resp.Error = errmsg
		reply = invalidRequest
	}
	resp.Seq = req.Seq
	sending.Lock()
	defer sending.Unlock()
	err := codec.WriteResponse(resp, reply)
	if err != nil {
		s.log.Error("writing response:", err)
	}
}

func (cb *RPCCallback) Return(out interface{}, err error) {
	select {
	case <-cb.setupDone:
		// already closed
	default:
		close(cb.setupDone)
	}
	errmsg := ""
	if err != nil {
		errmsg = err.Error()
	}
	var resp rpc.Response
	if logflags.RPC() {
		outbytes, _ := json.Marshal(out)
		cb.s.log.Debugf("(async %d) -> %T%s error: %q", cb.req.Seq, out, outbytes, errmsg)
	}

	if cb.hasDisconnected() {
		return
	}

	cb.s.sendResponse(cb.sending, &cb.req, &resp, out, cb.codec, errmsg)
}

func (cb *RPCCallback) DisconnectChan() chan struct{} {
	return cb.disconnectChan
}

func (cb *RPCCallback) hasDisconnected() bool {
	select {
	case <-cb.disconnectChan:
		return true
	default:
	}

	return false
}

func (cb *RPCCallback) SetupDoneChan() chan struct{} {
	return cb.setupDone
}

// GetVersion returns the version of delve as well as the API version
// currently served.
func (s *RPCServer) GetVersion(args api.GetVersionIn, out *api.GetVersionOut) error {
	out.DelveVersion = version.DelveVersion.String()
	out.APIVersion = s.s.config.APIVersion
	return s.s.debugger.GetVersion(out)
}

// SetApiVersion changes version of the API being served.
func (s *RPCServer) SetApiVersion(args api.SetAPIVersionIn, out *api.SetAPIVersionOut) error {
	if args.APIVersion != 2 {
		return errors.New("unknown API version")
	}
	s.s.config.APIVersion = args.APIVersion
	return nil
}

type internalError struct {
	Err   interface{}
	Stack []internalErrorFrame
}

type internalErrorFrame struct {
	Pc   uintptr
	Func string
	File string
	Line int
}

func newInternalError(ierr interface{}, skip int) *internalError {
	logflags.Bug.Inc()
	r := &internalError{ierr, nil}
	for i := skip; ; i++ {
		pc, file, line, ok := runtime.Caller(i)
		if !ok {
			break
		}
		fname := "<unknown>"
		fn := runtime.FuncForPC(pc)
		if fn != nil {
			fname = fn.Name()
		}
		r.Stack = append(r.Stack, internalErrorFrame{pc, fname, file, line})
	}
	return r
}

func (err *internalError) Error() string {
	var out bytes.Buffer
	fmt.Fprintf(&out, "Internal debugger error: %v\n", err.Err)
	for _, frame := range err.Stack {
		fmt.Fprintf(&out, "%s (%#x)\n\t%s:%d\n", frame.Func, frame.Pc, frame.File, frame.Line)
	}
	return out.String()
}
