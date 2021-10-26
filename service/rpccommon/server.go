package rpccommon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"reflect"
	"runtime"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/go-delve/delve/pkg/logflags"
	"github.com/go-delve/delve/pkg/version"
	"github.com/go-delve/delve/service"
	"github.com/go-delve/delve/service/api"
	"github.com/go-delve/delve/service/dap"
	"github.com/go-delve/delve/service/debugger"
	"github.com/go-delve/delve/service/internal/sameuser"
	"github.com/go-delve/delve/service/rpc1"
	"github.com/go-delve/delve/service/rpc2"
	"github.com/sirupsen/logrus"
)

// ServerImpl implements a JSON-RPC server that can switch between two
// versions of the API.
type ServerImpl struct {
	// config is all the information necessary to start the debugger and server.
	config *service.Config
	// listener is used to serve HTTP.
	listener net.Listener
	// stopChan is used to stop the listener goroutine.
	stopChan chan struct{}
	// debugger is the debugger service.
	debugger *debugger.Debugger
	// s1 is APIv1 server.
	s1 *rpc1.RPCServer
	// s2 is APIv2 server.
	s2 *rpc2.RPCServer
	// maps of served methods, one for each supported API.
	methodMaps []map[string]*methodType
	log        *logrus.Entry
}

type RPCCallback struct {
	s         *ServerImpl
	sending   *sync.Mutex
	codec     rpc.ServerCodec
	req       rpc.Request
	setupDone chan struct{}
}

var _ service.RPCCallback = &RPCCallback{}

// RPCServer implements the RPC method calls common to all versions of the API.
type RPCServer struct {
	s *ServerImpl
}

type methodType struct {
	method      reflect.Method
	Rcvr        reflect.Value
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
		s.debugger.Command(&api.DebuggerCommand{Name: api.Halt}, nil)
	}
	kill := s.config.Debugger.AttachPid == 0
	return s.debugger.Detach(kill)
}

// Run starts a debugger and exposes it with an HTTP server. The debugger
// itself can be stopped with the `detach` API. Run blocks until the HTTP
// server stops.
func (s *ServerImpl) Run() error {
	var err error

	if s.config.APIVersion < 2 {
		s.config.APIVersion = 1
	}
	if s.config.APIVersion > 2 {
		return fmt.Errorf("unknown API version")
	}

	// Create and start the debugger
	config := s.config.Debugger
	if s.debugger, err = debugger.New(&config, s.config.ProcessArgs); err != nil {
		return err
	}

	s.s1 = rpc1.NewServer(s.config, s.debugger)
	s.s2 = rpc2.NewServer(s.config, s.debugger)

	rpcServer := &RPCServer{s}

	s.methodMaps = make([]map[string]*methodType, 2)

	s.methodMaps[0] = map[string]*methodType{}
	s.methodMaps[1] = map[string]*methodType{}
	suitableMethods(s.s1, s.methodMaps[0], s.log)
	suitableMethods(rpcServer, s.methodMaps[0], s.log)
	suitableMethods(s.s2, s.methodMaps[1], s.log)
	suitableMethods(rpcServer, s.methodMaps[1], s.log)

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

// Precompute the reflect type for error.  Can't use error directly
// because Typeof takes an empty interface value.  This is annoying.
var typeOfError = reflect.TypeOf((*error)(nil)).Elem()

// Is this an exported - upper case - name?
func isExported(name string) bool {
	ch, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(ch)
}

// Is this type exported or a builtin?
func isExportedOrBuiltinType(t reflect.Type) bool {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	// PkgPath will be non-empty even for an exported type,
	// so we need to check the type name as well.
	return isExported(t.Name()) || t.PkgPath() == ""
}

// Fills methods map with the methods of receiver that should be made
// available through the RPC interface.
// These are all the public methods of rcvr that have one of those
// two signatures:
//  func (rcvr ReceiverType) Method(in InputType, out *ReplyType) error
//  func (rcvr ReceiverType) Method(in InputType, cb service.RPCCallback)
func suitableMethods(rcvr interface{}, methods map[string]*methodType, log *logrus.Entry) {
	typ := reflect.TypeOf(rcvr)
	rcvrv := reflect.ValueOf(rcvr)
	sname := reflect.Indirect(rcvrv).Type().Name()
	if sname == "" {
		log.Debugf("rpc.Register: no service name for type %s", typ)
		return
	}
	for m := 0; m < typ.NumMethod(); m++ {
		method := typ.Method(m)
		mname := method.Name
		mtype := method.Type
		// method must be exported
		if method.PkgPath != "" {
			continue
		}
		// Method needs three ins: (receive, *args, *reply) or (receiver, *args, *RPCCallback)
		if mtype.NumIn() != 3 {
			log.Warn("method", mname, "has wrong number of ins:", mtype.NumIn())
			continue
		}
		// First arg need not be a pointer.
		argType := mtype.In(1)
		if !isExportedOrBuiltinType(argType) {
			log.Warn(mname, "argument type not exported:", argType)
			continue
		}

		replyType := mtype.In(2)
		synchronous := replyType.String() != "service.RPCCallback"

		if synchronous {
			// Second arg must be a pointer.
			if replyType.Kind() != reflect.Ptr {
				log.Warn("method", mname, "reply type not a pointer:", replyType)
				continue
			}
			// Reply type must be exported.
			if !isExportedOrBuiltinType(replyType) {
				log.Warn("method", mname, "reply type not exported:", replyType)
				continue
			}

			// Method needs one out.
			if mtype.NumOut() != 1 {
				log.Warn("method", mname, "has wrong number of outs:", mtype.NumOut())
				continue
			}
			// The return type of the method must be error.
			if returnType := mtype.Out(0); returnType != typeOfError {
				log.Warn("method", mname, "returns", returnType.String(), "not error")
				continue
			}
		} else if mtype.NumOut() != 0 {
			// Method needs zero outs.
			log.Warn("method", mname, "has wrong number of outs:", mtype.NumOut())
			continue
		}
		methods[sname+"."+mname] = &methodType{method: method, ArgType: argType, ReplyType: replyType, Synchronous: synchronous, Rcvr: rcvrv}
	}
}

func (s *ServerImpl) serveJSONCodec(conn io.ReadWriteCloser) {
	defer func() {
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
			function := mtype.method.Func
			var returnValues []reflect.Value
			var errInter interface{}
			func() {
				defer func() {
					if ierr := recover(); ierr != nil {
						errInter = newInternalError(ierr, 2)
					}
				}()
				returnValues = function.Call([]reflect.Value{mtype.Rcvr, argv, replyv})
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
			function := mtype.method.Func
			ctl := &RPCCallback{s, sending, codec, req, make(chan struct{})}
			go func() {
				defer func() {
					if ierr := recover(); ierr != nil {
						ctl.Return(nil, newInternalError(ierr, 2))
					}
				}()
				function.Call([]reflect.Value{mtype.Rcvr, argv, reflect.ValueOf(ctl)})
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
	cb.s.sendResponse(cb.sending, &cb.req, &resp, out, cb.codec, errmsg)
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
	if args.APIVersion < 2 {
		args.APIVersion = 1
	}
	if args.APIVersion > 2 {
		return fmt.Errorf("unknown API version")
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
