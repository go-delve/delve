package rpccommon

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"reflect"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/derekparker/delve/service"
	"github.com/derekparker/delve/service/api"
	"github.com/derekparker/delve/service/debugger"
	"github.com/derekparker/delve/service/rpc1"
	"github.com/derekparker/delve/service/rpc2"
	"github.com/derekparker/delve/version"
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
}

type RPCCallback struct {
	s       *ServerImpl
	sending *sync.Mutex
	codec   rpc.ServerCodec
	req     rpc.Request
}

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
func NewServer(config *service.Config, logEnabled bool) *ServerImpl {
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	if !logEnabled {
		log.SetOutput(ioutil.Discard)
	}
	if config.APIVersion < 2 {
		log.Printf("Using API v1")
	}
	return &ServerImpl{
		config:   config,
		listener: config.Listener,
		stopChan: make(chan struct{}),
	}
}

// Stop stops the JSON-RPC server.
func (s *ServerImpl) Stop(kill bool) error {
	if s.config.AcceptMulti {
		close(s.stopChan)
		s.listener.Close()
	}
	return s.debugger.Detach(kill)
}

// Restart restarts the debugger.
func (s *ServerImpl) Restart() error {
	if s.config.AttachPid != 0 {
		return errors.New("cannot restart process Delve did not create")
	}
	return s.s2.Restart(rpc2.RestartIn{}, nil)
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
	if s.debugger, err = debugger.New(&debugger.Config{
		ProcessArgs: s.config.ProcessArgs,
		AttachPid:   s.config.AttachPid,
		Wd:          s.config.Wd,
	}); err != nil {
		return err
	}

	s.s1 = rpc1.NewServer(s.config, s.debugger)
	s.s2 = rpc2.NewServer(s.config, s.debugger)

	rpcServer := &RPCServer{s}

	s.methodMaps = make([]map[string]*methodType, 2)

	s.methodMaps[0] = map[string]*methodType{}
	s.methodMaps[1] = map[string]*methodType{}
	suitableMethods(s.s1, s.methodMaps[0])
	suitableMethods(rpcServer, s.methodMaps[0])
	suitableMethods(s.s2, s.methodMaps[1])
	suitableMethods(rpcServer, s.methodMaps[1])

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
			go s.serveJSONCodec(c)
			if !s.config.AcceptMulti {
				break
			}
		}
	}()
	return nil
}

// Precompute the reflect type for error.  Can't use error directly
// because Typeof takes an empty interface value.  This is annoying.
var typeOfError = reflect.TypeOf((*error)(nil)).Elem()

// Is this an exported - upper case - name?
func isExported(name string) bool {
	rune, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(rune)
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
func suitableMethods(rcvr interface{}, methods map[string]*methodType) {
	typ := reflect.TypeOf(rcvr)
	rcvrv := reflect.ValueOf(rcvr)
	sname := reflect.Indirect(rcvrv).Type().Name()
	if sname == "" {
		log.Printf("rpc.Register: no service name for type %s", typ)
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
			log.Println("method", mname, "has wrong number of ins:", mtype.NumIn())
			continue
		}
		// First arg need not be a pointer.
		argType := mtype.In(1)
		if !isExportedOrBuiltinType(argType) {
			log.Println(mname, "argument type not exported:", argType)
			continue
		}

		replyType := mtype.In(2)
		synchronous := replyType.String() != "service.RPCCallback"

		if synchronous {
			// Second arg must be a pointer.
			if replyType.Kind() != reflect.Ptr {
				log.Println("method", mname, "reply type not a pointer:", replyType)
				continue
			}
			// Reply type must be exported.
			if !isExportedOrBuiltinType(replyType) {
				log.Println("method", mname, "reply type not exported:", replyType)
				continue
			}

			// Method needs one out.
			if mtype.NumOut() != 1 {
				log.Println("method", mname, "has wrong number of outs:", mtype.NumOut())
				continue
			}
			// The return type of the method must be error.
			if returnType := mtype.Out(0); returnType != typeOfError {
				log.Println("method", mname, "returns", returnType.String(), "not error")
				continue
			}
		} else {
			// Method needs zero outs.
			if mtype.NumOut() != 0 {
				log.Println("method", mname, "has wrong number of outs:", mtype.NumOut())
				continue
			}
		}
		methods[sname+"."+mname] = &methodType{method: method, ArgType: argType, ReplyType: replyType, Synchronous: synchronous, Rcvr: rcvrv}
	}
	return
}

func (s *ServerImpl) serveJSONCodec(conn io.ReadWriteCloser) {
	sending := new(sync.Mutex)
	codec := jsonrpc.NewServerCodec(conn)
	var req rpc.Request
	var resp rpc.Response
	for {
		req = rpc.Request{}
		err := codec.ReadRequestHeader(&req)
		if err != nil {
			if err != io.EOF {
				log.Println("rpc:", err)
			}
			break
		}

		mtype, ok := s.methodMaps[s.config.APIVersion-1][req.ServiceMethod]
		if !ok {
			log.Printf("rpc: can't find method %s", req.ServiceMethod)
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
			replyv = reflect.New(mtype.ReplyType.Elem())
			function := mtype.method.Func
			returnValues := function.Call([]reflect.Value{mtype.Rcvr, argv, replyv})
			errInter := returnValues[0].Interface()
			errmsg := ""
			if errInter != nil {
				errmsg = errInter.(error).Error()
			}
			resp = rpc.Response{}
			s.sendResponse(sending, &req, &resp, replyv.Interface(), codec, errmsg)
		} else {
			function := mtype.method.Func
			ctl := &RPCCallback{s, sending, codec, req}
			go function.Call([]reflect.Value{mtype.Rcvr, argv, reflect.ValueOf(ctl)})
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
		log.Println("rpc: writing response:", err)
	}
}

func (cb *RPCCallback) Return(out interface{}, err error) {
	errmsg := ""
	if err != nil {
		errmsg = err.Error()
	}
	var resp rpc.Response
	cb.s.sendResponse(cb.sending, &cb.req, &resp, out, cb.codec, errmsg)
}

// GetVersion returns the version of delve as well as the API version
// currently served.
func (s *RPCServer) GetVersion(args api.GetVersionIn, out *api.GetVersionOut) error {
	out.DelveVersion = version.DelveVersion.String()
	out.APIVersion = s.s.config.APIVersion
	return nil
}

// Changes version of the API being served.
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
